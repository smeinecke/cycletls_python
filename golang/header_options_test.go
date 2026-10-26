package main

import (
	"bufio"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	http "github.com/Danny-Dasilva/fhttp"
)

// TestSetRequestHeadersPreservesCasingAndMultipleValues is the unit-level
// pin for upstream CycleTLS PR #423: headers must reach the wire with the
// caller's exact casing, and every value of a multi-value header must be
// sent. Header.Set canonicalizes keys, so setRequestHeaders assigns into
// the map directly.
func TestSetRequestHeadersPreservesCasingAndMultipleValues(t *testing.T) {
	headers := make(http.Header)
	options := Options{
		Headers: map[string]string{
			"accept": "text/plain",
		},
		HeaderValues: map[string][]string{
			"x-CUSTOM-case": []string{"one", "two"},
		},
	}

	setRequestHeaders(headers, options, false)

	if _, ok := headers["X-Custom-Case"]; ok {
		t.Fatalf("header key was canonicalized: %#v", headers)
	}
	if got := headers["x-CUSTOM-case"]; !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("x-CUSTOM-case = %#v, want [one two]", got)
	}
	if got := headers["accept"]; !reflect.DeepEqual(got, []string{"text/plain"}) {
		t.Fatalf("accept = %#v, want [text/plain]", got)
	}
}

// TestSetRequestHeadersSkipsContentLength pins the skip flag used by the
// dispatcher paths, where fhttp manages Content-Length itself.
func TestSetRequestHeadersSkipsContentLength(t *testing.T) {
	headers := make(http.Header)
	options := Options{
		HeaderValues: map[string][]string{
			"Content-Length": {"999"},
			"content-length": {"888"},
		},
	}

	setRequestHeaders(headers, options, true)

	if len(headers) != 0 {
		t.Fatalf("expected no headers, got %#v", headers)
	}
}

// TestRequestHeadersMerge pins the precedence rule: HeaderValues wins for
// keys present in both maps, Headers fills in the rest.
func TestRequestHeadersMerge(t *testing.T) {
	options := Options{
		Headers: map[string]string{
			"shared":       "from-headers",
			"only-headers": "x",
		},
		HeaderValues: map[string][]string{
			"shared": {"a", "b"},
		},
	}

	merged := options.requestHeaders()
	if got := merged["shared"]; !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("shared = %#v, want HeaderValues to win", got)
	}
	if got := merged["only-headers"]; !reflect.DeepEqual(got, []string{"x"}) {
		t.Fatalf("only-headers = %#v, want [x]", got)
	}
}

// TestHTTP1RequestHeadersOnWire is the end-to-end half of the port: a raw
// TCP listener captures what an http/1.1 request actually emits, asserting
// casing is preserved and multi-value headers repeat on the wire.
func TestHTTP1RequestHeadersOnWire(t *testing.T) {
	rawRequest := captureRawHTTP1Request(t, Options{
		Headers: map[string]string{
			"x-CUSTOM-case": "one",
		},
		HeaderValues: map[string][]string{
			"x-MULTI-value": {"one", "two"},
		},
		UserAgent: "CycleTLS-Test",
	})

	assertHeaderLine(t, rawRequest, "x-CUSTOM-case: one")
	assertNoHeaderLine(t, rawRequest, "X-Custom-Case:")
	assertHeaderLine(t, rawRequest, "x-MULTI-value: one")
	assertHeaderLine(t, rawRequest, "x-MULTI-value: two")
	if got := countHeaderLines(rawRequest, "x-MULTI-value:"); got != 2 {
		t.Fatalf("x-MULTI-value line count = %d, want 2\nraw request:\n%s", got, rawRequest)
	}
	// Do() sets PHeaderOrderKey for the h2 pseudo-header order, but on an
	// h1 transport fhttp writes a bare PHeaderOrderKey verbatim - it must
	// never reach the wire.
	assertNoHeaderLine(t, rawRequest, "PHeader-Order:")
	assertNoHeaderLine(t, rawRequest, "Header-Order:")
}

func captureRawHTTP1Request(t *testing.T, options Options) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	rawRequests := make(chan string, 1)
	serverErrors := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErrors <- err
			return
		}
		defer conn.Close()

		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			serverErrors <- err
			return
		}

		reader := bufio.NewReader(conn)
		var raw strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				serverErrors <- err
				return
			}
			raw.WriteString(line)
			if line == "\r\n" {
				break
			}
		}

		rawRequests <- raw.String()
		_, err = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nOK"))
		if err != nil {
			serverErrors <- err
		}
	}()

	client := CycleTLS{}
	resp, err := client.Do(fmt.Sprintf("http://%s/", listener.Addr().String()), options, "GET")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("status = %d, want 200, body: %q", resp.Status, resp.Body)
	}

	select {
	case rawRequest := <-rawRequests:
		return rawRequest
	case err := <-serverErrors:
		t.Fatalf("server failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for raw request")
	}

	return ""
}

func assertHeaderLine(t *testing.T, rawRequest string, line string) {
	t.Helper()
	if !strings.Contains(rawRequest, "\r\n"+line+"\r\n") {
		t.Fatalf("missing header line %q\nraw request:\n%s", line, rawRequest)
	}
}

func assertNoHeaderLine(t *testing.T, rawRequest string, linePrefix string) {
	t.Helper()
	if countHeaderLines(rawRequest, linePrefix) != 0 {
		t.Fatalf("unexpected header line prefix %q\nraw request:\n%s", linePrefix, rawRequest)
	}
}

func countHeaderLines(rawRequest string, linePrefix string) int {
	count := 0
	for _, line := range strings.Split(rawRequest, "\r\n") {
		if strings.HasPrefix(line, linePrefix) {
			count++
		}
	}
	return count
}
