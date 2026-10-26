package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	stdhttp "net/http"
	"strings"
	"sync"
	"time"

	http "github.com/Danny-Dasilva/fhttp"
	http2 "github.com/Danny-Dasilva/fhttp/http2"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	uquic "github.com/refraction-networking/uquic"
	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/proxy"
)

var errProtocolNegotiated = errors.New("protocol negotiated")

type roundTripper struct {
	sync.Mutex

	// Per-address mutexes for preventing concurrent transport creation
	addressMutexes   map[string]*sync.Mutex
	addressMutexLock sync.Mutex

	// TLS fingerprinting options
	JA3              string
	JA4r             string // JA4 raw format with explicit cipher/extension values
	HTTP2Fingerprint string
	QUICFingerprint  string
	USpec            *uquic.QUICSpec // UQuic QUIC specification for HTTP3 fingerprinting
	DisableGrease    bool

	// Browser identification
	UserAgent   string
	HeaderOrder []string

	// Connection options
	TLSConfig          *utls.Config
	InsecureSkipVerify bool
	ServerName         string
	Cookies            []Cookie
	ForceHTTP1         bool
	ForceHTTP3         bool

	// LocalAddress is the local IP to bind outbound sockets to. Used by
	// http3Dial for the UDP ListenPacket address; for TCP dials it is
	// applied via the net.Dialer in client.go / connect.go.
	LocalAddress string

	// DisableKeepAlives, when true, sets DisableKeepAlives on every
	// inner http.Transport that this roundTripper constructs. Wired
	// from Browser.DisableKeepAlives, which getOrCreateClient sets when
	// the caller passes enable_connection_reuse=false.
	DisableKeepAlives bool

	// TLS 1.3 specific options
	TLS13AutoRetry bool

	// Caching
	cachedConnections map[string]net.Conn
	cachedTransports  map[string]http.RoundTripper

	dialer proxy.ContextDialer

	// Parsed HTTP/2 fingerprint, populated lazily because newRoundTripper
	// cannot return an error.
	h2FingerprintOnce   sync.Once
	parsedH2Fingerprint *HTTP2Fingerprint
	h2FingerprintErr    error
}

func (rt *roundTripper) getH2Fingerprint() (*HTTP2Fingerprint, error) {
	rt.h2FingerprintOnce.Do(func() {
		if rt.HTTP2Fingerprint != "" {
			rt.parsedH2Fingerprint, rt.h2FingerprintErr = NewHTTP2Fingerprint(rt.HTTP2Fingerprint)
		}
	})
	return rt.parsedH2Fingerprint, rt.h2FingerprintErr
}

func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Apply cookies to the request
	for _, properties := range rt.Cookies {
		cookie := &http.Cookie{
			Name:       properties.Name,
			Value:      properties.Value,
			Path:       properties.Path,
			Domain:     properties.Domain,
			Expires:    properties.JSONExpires.Time,
			RawExpires: properties.RawExpires,
			MaxAge:     properties.MaxAge,
			HttpOnly:   properties.HTTPOnly,
			Secure:     properties.Secure,
			Raw:        properties.Raw,
			Unparsed:   properties.Unparsed,
		}
		req.AddCookie(cookie)
	}

	// Apply user agent
	req.Header.Set("User-Agent", rt.UserAgent)

	// Apply header order if specified (for regular headers, not pseudo-headers)
	if len(rt.HeaderOrder) > 0 {
		req.Header = ConvertHttpHeader(MarshalHeader(req.Header, rt.HeaderOrder))

		// Note: rt.HeaderOrder contains regular headers like "cache-control", "accept", etc.
		// The pseudo-header order is set below from the HTTP/2 fingerprint or UserAgent.
	}

	// Apply HTTP/2 pseudo-header order from the fingerprint when one is provided,
	// overriding the UserAgent-based order set by the caller.
	if rt.HTTP2Fingerprint != "" {
		h2fp, err := rt.getH2Fingerprint()
		if err != nil {
			return nil, fmt.Errorf("failed to parse HTTP/2 fingerprint: %w", err)
		}
		if h2fp != nil {
			if pseudoOrder := h2fp.PseudoHeaderOrder(); len(pseudoOrder) > 0 {
				req.Header[http.PHeaderOrderKey] = pseudoOrder
			}
		}
	}

	// Get address for dialing
	addr := rt.getDialTLSAddr(req)

	// Check if we need HTTP/3 - matches reference implementation pattern
	if rt.ForceHTTP3 {
		// Extract host and port from request
		host := req.URL.Hostname()
		port := req.URL.Port()
		if port == "" {
			port = "443" // Default HTTPS port
		}

		// Check for USpec (matches reference implementation logic)
		if rt.USpec != nil {
			// Use UQuic-based HTTP/3 dialing
			conn, err := rt.uhttp3Dial(req.Context(), rt.USpec, host, port)
			if err != nil {
				return nil, fmt.Errorf("uhttp3 dial failed: %w", err)
			}
			defer func() {
				if conn.RawConn != nil {
					conn.RawConn.Close()
				}
				// Close the QUIC connection based on its type
				if conn.QuicConn != nil {
					if conn.IsUQuic {
						if uquicConn, ok := conn.QuicConn.(interface{ CloseWithError(uint64, string) error }); ok {
							uquicConn.CloseWithError(0, "request completed")
						}
					} else {
						if quicConn, ok := conn.QuicConn.(interface{ CloseWithError(uint64, string) error }); ok {
							quicConn.CloseWithError(0, "request completed")
						}
					}
				}
			}()

			// Use the HTTP/3 connection to make the request
			return rt.makeHTTP3Request(req, conn)
		}

		// Fall back to standard HTTP/3 dialing
		conn, err := rt.ghttp3Dial(req.Context(), host, port)
		if err != nil {
			return nil, fmt.Errorf("ghttp3 dial failed: %w", err)
		}
		defer func() {
			if conn.RawConn != nil {
				conn.RawConn.Close()
			}
			// Close the QUIC connection based on its type
			if conn.QuicConn != nil {
				if conn.IsUQuic {
					if uquicConn, ok := conn.QuicConn.(interface{ CloseWithError(uint64, string) error }); ok {
						uquicConn.CloseWithError(0, "request completed")
					}
				} else {
					if quicConn, ok := conn.QuicConn.(interface{ CloseWithError(uint64, string) error }); ok {
						quicConn.CloseWithError(0, "request completed")
					}
				}
			}
		}()

		// Use the HTTP/3 connection to make the request
		return rt.makeHTTP3Request(req, conn)
	}

	// Use cached transport if available, otherwise create a new one
	if _, ok := rt.cachedTransports[addr]; !ok {
		if err := rt.getTransport(req, addr); err != nil {
			return nil, err
		}
	}

	transport := rt.cachedTransports[addr]
	if _, isH2 := transport.(*http2.Transport); !isH2 && req.Header[http.PHeaderOrderKey] != nil {
		// PHeaderOrderKey is an h2 pseudo-header ordering hint. fhttp's
		// h1 writer only excludes it when HeaderOrderKey is also set, so
		// a bare PHeaderOrderKey would hit the wire verbatim - a header
		// real browsers never send. Clone so the caller's Header map
		// keeps the key for a later h2 hop (redirects re-clone headers).
		req = req.Clone(req.Context())
		delete(req.Header, http.PHeaderOrderKey)
	}

	// Perform the request
	return transport.RoundTrip(req)
}

func (rt *roundTripper) getTransport(req *http.Request, addr string) error {
	switch strings.ToLower(req.URL.Scheme) {
	case "http":
		// Allow connection reuse with optimized connection pooling
		rt.cachedTransports[addr] = &http.Transport{
			DialContext:           rt.dialer.DialContext,
			MaxIdleConns:          100,
			MaxConnsPerHost:       100,
			MaxIdleConnsPerHost:   100, // Go default is 2, which causes 98% of connections to close
			IdleConnTimeout:       60 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			DisableKeepAlives:     rt.DisableKeepAlives,
		}
		return nil
	case "https":
	default:
		return fmt.Errorf("invalid URL scheme: [%v]", req.URL.Scheme)
	}

	// Use per-address mutex to serialize transport creation and avoid races
	addressMutex := rt.getAddressMutex(addr)
	addressMutex.Lock()
	defer addressMutex.Unlock()

	// Double-check if transport was created while we were waiting for the lock
	if _, exists := rt.cachedTransports[addr]; exists {
		// Another goroutine already created the transport
		return nil
	}

	// Establish TLS connection
	_, err := rt.dialTLS(req.Context(), "tcp", addr)
	switch err {
	case errProtocolNegotiated:
		// Transport and connection have been negotiated and cached by dialTLS
		// Nothing else to do here.
	case nil:
		// A cached connection/transport already exists (e.g., created by another goroutine).
		// Treat as success instead of panicking.
		// No action needed; RoundTrip will use the cached transport.
	default:
		return err
	}

	return nil
}

func (rt *roundTripper) dialTLS(ctx context.Context, network, addr string) (net.Conn, error) {
	rt.Lock()
	defer rt.Unlock()

	// Hand a cached connection to the transport exactly once. The map
	// entry is only a hand-off from the pre-dial below (which must
	// handshake up front to learn the negotiated protocol) to the
	// transport's own dial callback. Re-serving it on later redials
	// hands back the same socket, so once the peer closes it the pooled
	// client fails with "use of closed network connection" forever.
	if conn := rt.cachedConnections[addr]; conn != nil {
		delete(rt.cachedConnections, addr)
		return conn, nil
	}

	// Establish raw connection
	rawConn, err := rt.dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	// Extract host from address
	var host string
	if host, _, err = net.SplitHostPort(addr); err != nil {
		host = addr
	}
	// Determine SNI to use (custom serverName takes precedence)
	serverName := host
	if rt.ServerName != "" {
		serverName = rt.ServerName
	}

	var spec *utls.ClientHelloSpec
	var proactivelyUpgraded bool // Track if we proactively upgraded TLS 1.2 to 1.3

	// Determine which fingerprint to use
	if rt.QUICFingerprint != "" {
		// Use QUIC fingerprint
		spec, err = QUICStringToSpec(rt.QUICFingerprint, rt.UserAgent, rt.ForceHTTP1)
		if err != nil {
			return nil, err
		}
	} else if rt.JA4r != "" {
		// JA4r takes precedence over JA3 when both are provided. The JA3
		// string is still passed along: its cipher/extension lists preserve
		// the browser's wire order, which JA4r loses to sorting.
		spec, err = JA4RStringToSpec(rt.JA4r, rt.UserAgent, rt.ForceHTTP1, rt.DisableGrease, serverName, rt.JA3)
		if err != nil {
			return nil, err
		}
	} else {
		// Fall back to the default Chrome fingerprint when no JA3 was
		// provided, and run it through the same dispatch as an explicit
		// JA3 - otherwise the default emitted a TLS 1.2-only ClientHello
		// (no TLS13AutoRetry upgrade), which anti-bot systems flag.
		ja3 := rt.JA3
		if ja3 == "" {
			ja3 = DefaultChrome_JA3
		}
		// Check if we should proactively upgrade TLS 1.2 to TLS 1.3
		if rt.TLS13AutoRetry && strings.HasPrefix(ja3, "771,") {
			// Use TLS 1.3 compatible spec to avoid retry cycle
			spec, err = StringToTLS13CompatibleSpec(ja3, rt.UserAgent, rt.ForceHTTP1)
			proactivelyUpgraded = true
		} else {
			// Use original JA3 fingerprint
			spec, err = StringToSpec(ja3, rt.UserAgent, rt.ForceHTTP1)
		}
		if err != nil {
			return nil, err
		}
	}

	// Create TLS client
	conn := utls.UClient(rawConn, &utls.Config{
		ServerName:         serverName,
		OmitEmptyPsk:       true,
		InsecureSkipVerify: rt.InsecureSkipVerify,
	}, utls.HelloCustom)

	// Apply TLS fingerprint
	if err := conn.ApplyPreset(spec); err != nil {
		return nil, err
	}

	// Perform TLS handshake
	if err = conn.Handshake(); err != nil {
		_ = conn.Close()

		if err.Error() == "tls: CurvePreferences includes unsupported curve" {
			// Check if TLS 1.3 retry is enabled
			if rt.TLS13AutoRetry {
				// Automatically retry with TLS 1.3 compatible curves
				return rt.retryWithTLS13CompatibleCurves(ctx, network, addr, host)
			}
			return nil, fmt.Errorf("conn.Handshake() error for TLS 1.3 (retry disabled): %+v", err)
		}

		// If we proactively upgraded to TLS 1.3 and it failed, try falling back to original TLS 1.2 JA3
		if proactivelyUpgraded && rt.JA3 != "" {
			return rt.retryWithOriginalTLS12JA3(ctx, network, addr, host)
		}

		return nil, fmt.Errorf("uTlsConn.Handshake() error: %+v", err)
	}

	// If transport already exists, return connection
	if rt.cachedTransports[addr] != nil {
		return conn, nil
	}

	// Create appropriate transport based on negotiated protocol
	switch conn.ConnectionState().NegotiatedProtocol {
	case http2.NextProtoTLS:
		// HTTP/2 transport
		parsedUserAgent := parseUserAgent(rt.UserAgent)

		// Use HTTP/2 fingerprint if specified
		var http2Transport http2.Transport
		if rt.HTTP2Fingerprint != "" {
			h2Fingerprint, err := rt.getH2Fingerprint()
			if err != nil {
				return nil, fmt.Errorf("failed to parse HTTP/2 fingerprint: %v", err)
			}

			http2Transport = http2.Transport{
				DialTLS:     rt.dialTLSHTTP2,
				PushHandler: &http2.DefaultPushHandler{},
			}

			// Apply HTTP/2 fingerprint settings and use the fingerprint to
			// guess the remaining fhttp defaults (e.g. stream ID when no
			// explicit priority frames are provided).
			if h2Fingerprint != nil {
				h2Fingerprint.Apply(&http2Transport)
				http2Transport.Navigator = h2Fingerprint.Navigator()
			}
		} else {
			http2Transport = http2.Transport{
				DialTLS:     rt.dialTLSHTTP2,
				PushHandler: &http2.DefaultPushHandler{},
				Navigator:   parsedUserAgent.UserAgent,
			}
		}

		rt.cachedTransports[addr] = &http2Transport
	default:
		// HTTP/1.x transport - optimized for connection reuse
		rt.cachedTransports[addr] = &http.Transport{
			DialTLSContext:      rt.dialTLS,
			MaxIdleConns:        100,
			MaxConnsPerHost:     100,
			MaxIdleConnsPerHost: 100, // Go default is 2, which causes 98% of connections to close
			IdleConnTimeout:     60 * time.Second,
			DisableKeepAlives:   rt.DisableKeepAlives, // Bound to enable_connection_reuse=False
		}
	}

	// Cache the connection for future use
	rt.cachedConnections[addr] = conn

	return nil, errProtocolNegotiated
}

// retryWithTLS13CompatibleCurves retries the TLS connection with TLS 1.3 compatible curves
func (rt *roundTripper) retryWithTLS13CompatibleCurves(ctx context.Context, network, addr, host string) (net.Conn, error) {
	// Establish raw connection for retry
	rawConn, err := rt.dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	var spec *utls.ClientHelloSpec

	// Use TLS 1.3 compatible spec based on the original fingerprint type
	if rt.QUICFingerprint != "" {
		// For QUIC, we'll use the original spec but this could be enhanced
		spec, err = QUICStringToSpec(rt.QUICFingerprint, rt.UserAgent, rt.ForceHTTP1)
		if err != nil {
			return nil, fmt.Errorf("failed to create QUIC spec for TLS 1.3 retry: %v", err)
		}
	} else if rt.JA4r != "" {
		// For JA4r, we'll use a fallback to default Chrome with TLS 1.3 compatible curves.
		// Checked before JA3 like the main dispatch: JA3 may be present alongside JA4r
		// as an ordering hint, and its curve list can contain groups this spec
		// cannot generate key shares for.
		spec, err = StringToTLS13CompatibleSpec(DefaultChrome_JA3, rt.UserAgent, rt.ForceHTTP1)
		if err != nil {
			return nil, fmt.Errorf("failed to create TLS 1.3 compatible JA4 fallback spec: %v", err)
		}
	} else if rt.JA3 != "" {
		// Use TLS 1.3 compatible JA3 spec
		spec, err = StringToTLS13CompatibleSpec(rt.JA3, rt.UserAgent, rt.ForceHTTP1)
		if err != nil {
			return nil, fmt.Errorf("failed to create TLS 1.3 compatible JA3 spec: %v", err)
		}
	} else {
		// Default to TLS 1.3 compatible Chrome fingerprint
		spec, err = StringToTLS13CompatibleSpec(DefaultChrome_JA3, rt.UserAgent, rt.ForceHTTP1)
		if err != nil {
			return nil, fmt.Errorf("failed to create TLS 1.3 compatible default spec: %v", err)
		}
	}

	// Create TLS client for retry
	conn := utls.UClient(rawConn, &utls.Config{
		ServerName:         host,
		OmitEmptyPsk:       true,
		InsecureSkipVerify: rt.InsecureSkipVerify,
	}, utls.HelloCustom)

	// Apply TLS 1.3 compatible fingerprint
	if err := conn.ApplyPreset(spec); err != nil {
		return nil, fmt.Errorf("failed to apply TLS 1.3 compatible preset: %v", err)
	}

	// Perform TLS handshake for retry
	if err = conn.Handshake(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("TLS 1.3 compatible handshake failed: %+v", err)
	}

	// If transport already exists (e.g., called from dialTLSHTTP2 after aliveness check),
	// just cache the connection and return it directly.
	transportAlreadyExists := rt.cachedTransports[addr] != nil

	// Create appropriate transport based on negotiated protocol
	switch conn.ConnectionState().NegotiatedProtocol {
	case http2.NextProtoTLS:
		// HTTP/2 transport
		parsedUserAgent := parseUserAgent(rt.UserAgent)

		var http2Transport http2.Transport
		if rt.HTTP2Fingerprint != "" {
			h2Fingerprint, err := rt.getH2Fingerprint()
			if err != nil {
				return nil, fmt.Errorf("failed to parse HTTP/2 fingerprint for TLS 1.3 retry: %v", err)
			}

			http2Transport = http2.Transport{
				DialTLS:     rt.dialTLSHTTP2,
				PushHandler: &http2.DefaultPushHandler{},
			}

			if h2Fingerprint != nil {
				h2Fingerprint.Apply(&http2Transport)
				http2Transport.Navigator = h2Fingerprint.Navigator()
			}
		} else {
			http2Transport = http2.Transport{
				DialTLS:     rt.dialTLSHTTP2,
				PushHandler: &http2.DefaultPushHandler{},
				Navigator:   parsedUserAgent.UserAgent,
			}
		}

		if !transportAlreadyExists {
			rt.cachedTransports[addr] = &http2Transport
		}
	default:
		// HTTP/1.x transport - optimized for connection reuse
		if !transportAlreadyExists {
			rt.cachedTransports[addr] = &http.Transport{
				DialTLSContext:      rt.dialTLS,
				MaxIdleConns:        100,
				MaxConnsPerHost:     100,
				MaxIdleConnsPerHost: 100, // Go default is 2, which causes 98% of connections to close
				IdleConnTimeout:     60 * time.Second,
				DisableKeepAlives:   rt.DisableKeepAlives, // Bound to enable_connection_reuse=False
			}
		}
	}

	// On a redial the existing transport receives this conn from its dial
	// callback - return it without caching so a dead conn is never
	// re-served. Otherwise cache it as the same one-time hand-off
	// dialTLS uses.
	if transportAlreadyExists {
		return conn, nil
	}
	rt.cachedConnections[addr] = conn
	return nil, errProtocolNegotiated
}

// retryWithOriginalTLS12JA3 retries the TLS connection with the original TLS 1.2 JA3
func (rt *roundTripper) retryWithOriginalTLS12JA3(ctx context.Context, network, addr, host string) (net.Conn, error) {
	// Establish raw connection for fallback to original TLS 1.2 JA3
	rawConn, err := rt.dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	// Use original TLS 1.2 JA3 spec (no upgrade)
	spec, err := StringToSpec(rt.JA3, rt.UserAgent, rt.ForceHTTP1)
	if err != nil {
		return nil, fmt.Errorf("failed to create original TLS 1.2 JA3 spec: %v", err)
	}

	// Create TLS client for fallback
	conn := utls.UClient(rawConn, &utls.Config{
		ServerName:         host,
		OmitEmptyPsk:       true,
		InsecureSkipVerify: rt.InsecureSkipVerify,
	}, utls.HelloCustom)

	// Apply original TLS 1.2 fingerprint
	if err := conn.ApplyPreset(spec); err != nil {
		return nil, fmt.Errorf("failed to apply original TLS 1.2 preset: %v", err)
	}

	// Perform TLS handshake for fallback
	if err = conn.Handshake(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("original TLS 1.2 handshake failed: %+v", err)
	}

	// If transport already exists (e.g., called from dialTLSHTTP2 after aliveness check),
	// just cache the connection and return it directly.
	transportAlreadyExists := rt.cachedTransports[addr] != nil

	// Create appropriate transport based on negotiated protocol
	switch conn.ConnectionState().NegotiatedProtocol {
	case http2.NextProtoTLS:
		// HTTP/2 transport
		parsedUserAgent := parseUserAgent(rt.UserAgent)

		var http2Transport http2.Transport
		if rt.HTTP2Fingerprint != "" {
			h2Fingerprint, err := rt.getH2Fingerprint()
			if err != nil {
				return nil, fmt.Errorf("failed to parse HTTP/2 fingerprint for TLS 1.2 fallback: %v", err)
			}

			http2Transport = http2.Transport{
				DialTLS:     rt.dialTLSHTTP2,
				PushHandler: &http2.DefaultPushHandler{},
			}

			if h2Fingerprint != nil {
				h2Fingerprint.Apply(&http2Transport)
				http2Transport.Navigator = h2Fingerprint.Navigator()
			}
		} else {
			http2Transport = http2.Transport{
				DialTLS:     rt.dialTLSHTTP2,
				PushHandler: &http2.DefaultPushHandler{},
				Navigator:   parsedUserAgent.UserAgent,
			}
		}

		if !transportAlreadyExists {
			rt.cachedTransports[addr] = &http2Transport
		}
	default:
		// HTTP/1.x transport - optimized for connection reuse
		if !transportAlreadyExists {
			rt.cachedTransports[addr] = &http.Transport{
				DialTLSContext:      rt.dialTLS,
				MaxIdleConns:        100,
				MaxConnsPerHost:     100,
				MaxIdleConnsPerHost: 100, // Go default is 2, which causes 98% of connections to close
				IdleConnTimeout:     60 * time.Second,
				DisableKeepAlives:   rt.DisableKeepAlives, // Bound to enable_connection_reuse=False
			}
		}
	}

	// Same as the TLS 1.3 retry path: on a redial the conn goes straight
	// to the existing transport's dial callback and must not be cached.
	if transportAlreadyExists {
		return conn, nil
	}
	rt.cachedConnections[addr] = conn
	return nil, errProtocolNegotiated
}

func (rt *roundTripper) dialTLSHTTP2(network, addr string, _ *utls.Config) (net.Conn, error) {
	return rt.dialTLS(context.Background(), network, addr)
}

func (rt *roundTripper) getDialTLSAddr(req *http.Request) string {
	host, port, err := net.SplitHostPort(req.URL.Host)
	if err == nil {
		return net.JoinHostPort(host, port)
	}
	return net.JoinHostPort(req.URL.Host, "443") // Default HTTPS port
}

// getAddressMutex returns a mutex for the specific address to serialize transport creation
func (rt *roundTripper) getAddressMutex(addr string) *sync.Mutex {
	rt.addressMutexLock.Lock()
	defer rt.addressMutexLock.Unlock()

	if rt.addressMutexes == nil {
		rt.addressMutexes = make(map[string]*sync.Mutex)
	}

	if mu, exists := rt.addressMutexes[addr]; exists {
		return mu
	}

	mu := &sync.Mutex{}
	rt.addressMutexes[addr] = mu
	return mu
}

// CloseIdleConnections closes connections that have been idle for too long
// If selectedAddr is provided, only close connections not matching this address
func (rt *roundTripper) CloseIdleConnections(selectedAddr ...string) {
	rt.Lock()
	defer rt.Unlock()

	keep := ""
	if len(selectedAddr) > 0 {
		keep = selectedAddr[0]
	}

	// Let each cached transport drain its own idle connections first - on
	// HTTP/2 this performs a graceful GOAWAY instead of just cutting the
	// underlying socket.
	for trAddr, tr := range rt.cachedTransports {
		if trAddr == keep {
			continue
		}
		if idleCloser, ok := tr.(interface{ CloseIdleConnections() }); ok {
			idleCloser.CloseIdleConnections()
		}
		delete(rt.cachedTransports, trAddr)
	}

	for connAddr, conn := range rt.cachedConnections {
		if connAddr == keep {
			continue
		}
		if conn != nil {
			// Ignore errors from closing - connection might already be closed
			_ = conn.Close()
		}
		delete(rt.cachedConnections, connAddr)
	}
}

func newRoundTripper(browser Browser, dialer ...proxy.ContextDialer) http.RoundTripper {
	var contextDialer proxy.ContextDialer
	if len(dialer) > 0 {
		contextDialer = dialer[0]
	} else {
		contextDialer = proxy.Direct
	}

	return &roundTripper{
		dialer:             contextDialer,
		JA3:                browser.JA3,
		JA4r:               browser.JA4r,
		HTTP2Fingerprint:   browser.HTTP2Fingerprint,
		QUICFingerprint:    browser.QUICFingerprint,
		USpec:              browser.USpec, // Add USpec field initialization
		DisableGrease:      browser.DisableGrease,
		UserAgent:          browser.UserAgent,
		HeaderOrder:        browser.HeaderOrder,
		TLSConfig:          browser.TLSConfig,
		ServerName:         browser.ServerName,
		Cookies:            browser.Cookies,
		cachedTransports:   make(map[string]http.RoundTripper),
		cachedConnections:  make(map[string]net.Conn),
		InsecureSkipVerify: browser.InsecureSkipVerify,
		ForceHTTP1:         browser.ForceHTTP1,
		ForceHTTP3:         browser.ForceHTTP3,
		LocalAddress:       browser.LocalAddress,
		DisableKeepAlives:  browser.DisableKeepAlives,

		// TLS 1.3 specific options
		TLS13AutoRetry: browser.TLS13AutoRetry,
	}
}

// makeHTTP3Request performs an HTTP/3 request using the provided HTTP/3 connection
func (rt *roundTripper) makeHTTP3Request(req *http.Request, conn *HTTP3Connection) (*http.Response, error) {
	// Create HTTP/3 RoundTripper with custom dial function that uses our established connection
	tlsConfig := ConvertUtlsConfig(rt.TLSConfig)
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	}
	// Apply custom SNI if provided
	if rt.ServerName != "" {
		tlsConfig.ServerName = rt.ServerName
	}

	// Create HTTP/3 Transport - let it establish its own connections for now
	roundTripper := &http3.Transport{
		TLSClientConfig: tlsConfig,
		QUICConfig: &quic.Config{
			HandshakeIdleTimeout:           30 * time.Second,
			MaxIdleTimeout:                 90 * time.Second,
			KeepAlivePeriod:                15 * time.Second,
			InitialStreamReceiveWindow:     512 * 1024,      // 512 KB
			MaxStreamReceiveWindow:         2 * 1024 * 1024, // 2 MB
			InitialConnectionReceiveWindow: 1024 * 1024,     // 1 MB
			MaxConnectionReceiveWindow:     4 * 1024 * 1024, // 4 MB
			MaxIncomingStreams:             100,
			MaxIncomingUniStreams:          100,
			EnableDatagrams:                false,
			DisablePathMTUDiscovery:        false,
			Allow0RTT:                      false,
		},
	}

	// Convert fhttp.Request to net/http.Request
	stdReq := &stdhttp.Request{
		Method:           req.Method,
		URL:              req.URL,
		Proto:            req.Proto,
		ProtoMajor:       req.ProtoMajor,
		ProtoMinor:       req.ProtoMinor,
		Header:           ConvertFhttpHeader(req.Header),
		Body:             req.Body,
		GetBody:          req.GetBody,
		ContentLength:    req.ContentLength,
		TransferEncoding: req.TransferEncoding,
		Close:            req.Close,
		Host:             req.Host,
		Form:             req.Form,
		PostForm:         req.PostForm,
		MultipartForm:    req.MultipartForm,
		Trailer:          ConvertFhttpHeader(req.Trailer),
		RemoteAddr:       req.RemoteAddr,
		RequestURI:       req.RequestURI,
		TLS:              nil,
		Cancel:           req.Cancel,
		Response:         nil,
	}

	// Use the RoundTripper to make the request
	stdResp, err := roundTripper.RoundTrip(stdReq)
	if err != nil {
		return nil, err
	}

	// Convert back to fhttp.Response
	return &http.Response{
		Status:           stdResp.Status,
		StatusCode:       stdResp.StatusCode,
		Proto:            stdResp.Proto,
		ProtoMajor:       stdResp.ProtoMajor,
		ProtoMinor:       stdResp.ProtoMinor,
		Header:           ConvertHttpHeader(stdResp.Header),
		Body:             stdResp.Body,
		ContentLength:    stdResp.ContentLength,
		TransferEncoding: stdResp.TransferEncoding,
		Close:            stdResp.Close,
		Uncompressed:     stdResp.Uncompressed,
		Trailer:          ConvertHttpHeader(stdResp.Trailer),
		Request:          req,
		TLS:              nil,
	}, nil
}

// Default JA3 fingerprint for Chrome (captured Chrome 149 on Linux).
// Includes ECH-GREASE (65037), ALPS (17613) and an X25519MLKEM768 (4588)
// key-share group - the previous pre-permutation Chrome hello this
// replaced is a widely-blocklisted scraper signature.
const DefaultChrome_JA3 = "771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,65037-5-65281-16-45-10-13-0-18-23-35-43-11-27-51-17613,4588-29-23-24,0"
