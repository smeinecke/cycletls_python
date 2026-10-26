package main

import (
	"context"
	"net"
	nhttp "net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	http "github.com/Danny-Dasilva/fhttp"
)

// TestDialTLSConsumesCachedConnection pins the consume-on-hand-out
// semantics of rt.cachedConnections: the entry exists only so the
// pre-dial inside getTransport can hand its conn to the transport's
// first DialTLS call. Before this, every redial re-received the same
// socket - after the peer closed it, the pooled client failed with
// "use of closed network connection" on every subsequent request.
func TestDialTLSConsumesCachedConnection(t *testing.T) {
	rt := newRoundTripper(Browser{}).(*roundTripper)

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	rt.cachedConnections["127.0.0.1:1"] = clientConn

	// First dial hands out the cached conn and removes it.
	conn, err := rt.dialTLS(context.Background(), "tcp", "127.0.0.1:1")
	if err != nil {
		t.Fatalf("dialTLS should return the cached conn, got error: %v", err)
	}
	if conn != clientConn {
		t.Fatalf("dialTLS returned a different conn than the cached one")
	}
	if _, ok := rt.cachedConnections["127.0.0.1:1"]; ok {
		t.Fatalf("cached conn should be consumed by the first dial, still present")
	}

	// Second dial must attempt a real dial (nothing listens on :1, so
	// this fails fast with connection refused) - not return the stale
	// cached conn again.
	if _, err := rt.dialTLS(context.Background(), "tcp", "127.0.0.1:1"); err == nil {
		t.Fatalf("expected a dial error after the cached conn was consumed, got a conn")
	}
}

// TestPooledClientSurvivesPeerClosedConnection is the end-to-end
// regression for the reported symptom: a pooled client reuses its
// transport, the server closes the connection after every response
// (Connection: close), and the next request on the same client must
// redial a fresh conn instead of reusing the dead one.
func TestPooledClientSurvivesPeerClosedConnection(t *testing.T) {
	clearAllConnections()
	defer clearAllConnections()

	srv := httptest.NewTLSServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, r *nhttp.Request) {
		w.Header().Set("Connection", "close")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := CycleTLS{}
	opts := Options{
		EnableConnectionReuse: true,
		InsecureSkipVerify:    true,
		Timeout:               10,
		UserAgent:             "conn-close-regression-test",
	}

	for i := 0; i < 4; i++ {
		resp, err := client.Do(srv.URL, opts, "GET")
		if err != nil {
			t.Fatalf("request %d errored: %v", i, err)
		}
		if resp.Status != 200 {
			t.Fatalf("request %d: expected 200, got status %d body %q", i, resp.Status, resp.Body)
		}
	}
}

// TestRedirectChainCookiesCarriedAndReported exercises both halves of
// the cookie-jar fix over a real redirect chain on an HTTP/2 TLS
// server: a cookie set on the intermediate hop must reach the final
// hop on the wire, and Response.Cookies must contain cookies from
// every hop - not just the final response's Set-Cookie.
func TestRedirectChainCookiesCarriedAndReported(t *testing.T) {
	clearAllConnections()
	defer clearAllConnections()

	var sawHop1 bool
	mux := nhttp.NewServeMux()
	mux.HandleFunc("/start", func(w nhttp.ResponseWriter, r *nhttp.Request) {
		nhttp.SetCookie(w, &nhttp.Cookie{Name: "hop1", Value: "v1"})
		nhttp.Redirect(w, r, "/final", nhttp.StatusFound)
	})
	mux.HandleFunc("/final", func(w nhttp.ResponseWriter, r *nhttp.Request) {
		if c, err := r.Cookie("hop1"); err == nil && c.Value == "v1" {
			sawHop1 = true
		}
		nhttp.SetCookie(w, &nhttp.Cookie{Name: "hop2", Value: "v2"})
		w.WriteHeader(200)
		_, _ = w.Write([]byte("done"))
	})

	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	client := CycleTLS{}
	resp, err := client.Do(srv.URL+"/start", Options{
		EnableConnectionReuse: true,
		InsecureSkipVerify:    true,
		Timeout:               10,
		UserAgent:             "redirect-cookie-regression-test",
	}, "GET")
	if err != nil {
		t.Fatalf("request errored: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200 after redirect, got %d body %q", resp.Status, resp.Body)
	}
	if !sawHop1 {
		t.Fatalf("final hop did not receive the intermediate Set-Cookie via the jar")
	}

	got := map[string]string{}
	for _, c := range resp.Cookies {
		got[c.Name] = c.Value
	}
	if got["hop1"] != "v1" || got["hop2"] != "v2" {
		t.Fatalf("expected accumulated cookies hop1=v1 and hop2=v2, got %v", got)
	}
}

// TestRecordingJarDeletion checks that a Set-Cookie deleting a cookie
// mid-chain removes it from the recorded set instead of reporting a
// stale value.
func TestRecordingJarDeletion(t *testing.T) {
	jar, err := newRecordingJar()
	if err != nil {
		t.Fatalf("newRecordingJar: %v", err)
	}
	u := mustParseURL(t, "https://example.test/")
	jar.SetCookies(u, []*http.Cookie{{Name: "a", Value: "1"}, {Name: "b", Value: "2"}})
	jar.SetCookies(u, []*http.Cookie{{Name: "a", Value: "", MaxAge: -1}})

	got := map[string]string{}
	for _, c := range jar.cookies() {
		got[c.Name] = c.Value
	}
	if _, ok := got["a"]; ok {
		t.Fatalf("deleted cookie a still recorded: %v", got)
	}
	if got["b"] != "2" {
		t.Fatalf("cookie b missing from recorded set: %v", got)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("bad test URL: %v", err)
	}
	return u
}
