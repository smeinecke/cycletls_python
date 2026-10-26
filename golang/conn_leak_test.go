package main

import (
	"net"
	nhttp "net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"

	http "github.com/Danny-Dasilva/fhttp"
)

// countingTransport records CloseIdleConnections calls so tests can verify
// that roundTripper.CloseIdleConnections propagates into cached transports.
type countingTransport struct {
	calls int
}

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, nil
}

func (c *countingTransport) CloseIdleConnections() { c.calls++ }

// TestCloseIdleConnectionsDrainsTransports asserts that CloseIdleConnections
// also asks each cached transport to drop its idle connections - not just the
// raw conns in cachedConnections. Without propagation an HTTP/2 ClientConn's
// read/write loops would keep running on the dropped conn.
func TestCloseIdleConnectionsDrainsTransports(t *testing.T) {
	rt := newRoundTripper(Browser{}).(*roundTripper)

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	rt.cachedConnections["example.test:443"] = clientConn

	ct := &countingTransport{}
	rt.cachedTransports["example.test:443"] = ct

	rt.CloseIdleConnections()

	if ct.calls == 0 {
		t.Errorf("expected CloseIdleConnections to be called on the cached transport")
	}
	if len(rt.cachedConnections) != 0 || len(rt.cachedTransports) != 0 {
		t.Errorf("expected both caches empty; got %d conns, %d transports",
			len(rt.cachedConnections), len(rt.cachedTransports))
	}
	if _, err := clientConn.Write([]byte("x")); err == nil {
		t.Errorf("expected cached conn to be closed, write succeeded")
	}
}

// TestCloseIdleConnectionsKeepsSelectedAddr pins the keep-one-address path:
// only non-matching conns/transports are drained.
func TestCloseIdleConnectionsKeepsSelectedAddr(t *testing.T) {
	rt := newRoundTripper(Browser{}).(*roundTripper)

	s1, c1 := net.Pipe()
	s2, c2 := net.Pipe()
	defer s1.Close()
	defer s2.Close()

	rt.cachedConnections["keep.test:443"] = c1
	rt.cachedConnections["drop.test:443"] = c2
	keepTr := &countingTransport{}
	dropTr := &countingTransport{}
	rt.cachedTransports["keep.test:443"] = keepTr
	rt.cachedTransports["drop.test:443"] = dropTr

	rt.CloseIdleConnections("keep.test:443")

	if keepTr.calls != 0 {
		t.Errorf("kept transport should not be drained")
	}
	if dropTr.calls == 0 {
		t.Errorf("dropped transport should be drained")
	}
	if rt.cachedConnections["keep.test:443"] == nil || rt.cachedTransports["keep.test:443"] == nil {
		t.Errorf("kept addr lost its conn/transport")
	}
	if _, ok := rt.cachedConnections["drop.test:443"]; ok {
		t.Errorf("dropped conn still cached")
	}
	if _, ok := rt.cachedTransports["drop.test:443"]; ok {
		t.Errorf("dropped transport still cached")
	}
}

// countOpenFDs returns the number of open file descriptors for this process.
// Linux-only (/proc/self/fd); callers should skip elsewhere.
func countOpenFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("cannot read /proc/self/fd: %v", err)
	}
	return len(entries)
}

// TestDoClosesNonPooledClientConnections is the end-to-end regression for
// upstream CycleTLS PR #426: with enable_connection_reuse=false the client
// is built outside the pool and dropped after a single request - nothing
// used to close its connections. Against an HTTP/2 server every request
// leaked a socket plus the h2 read/write loop goroutines (EMFILE over time).
// HTTP/1.x was spared because DisableKeepAlives makes the inner transport
// close the conn itself; h2 has no such knob, so Do() must close them.
func TestDoClosesNonPooledClientConnections(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fd counting requires /proc/self/fd")
	}

	srv := httptest.NewUnstartedServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, r *nhttp.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	client := CycleTLS{}
	opts := Options{
		EnableConnectionReuse: false,
		InsecureSkipVerify:    true,
		Timeout:               10,
		UserAgent:             "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	}

	do := func() {
		resp, err := client.Do(srv.URL, opts, "GET")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		// Do() reports request-level failures inside the Response rather
		// than via err - require a real 200 so the test can't pass on a
		// conn that never came up.
		if resp.Status != 200 {
			t.Fatalf("expected 200, got status %d body %q", resp.Status, resp.Body)
		}
	}

	// Warmup: one request so steady-state goroutines (timers, GC helpers)
	// are spun up before we take the baseline.
	do()
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	baseG := runtime.NumGoroutine()
	baseFD := countOpenFDs(t)

	const n = 8
	for i := 0; i < n; i++ {
		do()
	}

	// Give closed conns a moment to release fds and let h2 loops exit.
	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	time.Sleep(200 * time.Millisecond)

	gotG := runtime.NumGoroutine() - baseG
	gotFD := countOpenFDs(t) - baseFD

	// Each leaked h2 conn costs ~2 goroutines and 1 fd. Allow slack for
	// unrelated runtime churn but fail if the per-request leak is present.
	if gotG >= n {
		t.Errorf("goroutine count grew by %d after %d reuse-off requests - non-pooled client conns not closed", gotG, n)
	}
	if gotFD >= n/2 {
		t.Errorf("fd count grew by %d after %d reuse-off requests - non-pooled client conns not closed", gotFD, n)
	}
}
