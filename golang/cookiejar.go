package main

import (
	"net/url"
	"sync"
	"time"

	http "github.com/Danny-Dasilva/fhttp"
	"github.com/Danny-Dasilva/fhttp/cookiejar"
)

// recordingJar wraps an RFC 6265 cookie jar and additionally records
// the latest Set-Cookie seen per cookie name. fhttp consults a
// client's Jar on every redirect hop, so the jar both carries cookies
// forward through a redirect chain (the way a browser session jar
// does) and accumulates the state Do() reports - resp.Cookies() alone
// only reflects the final hop's headers and loses cookies set on
// intermediate redirects.
type recordingJar struct {
	*cookiejar.Jar
	mu   sync.Mutex
	seen map[string]*http.Cookie
	keys []string
}

// newRecordingJar builds a cookie jar scoped to a single request. It
// must only be attached to the per-call fhttp.Client copy - pooled
// clients are returned by value, so a jar stored on the pooled entry
// would leak cookies between unrelated requests.
func newRecordingJar() (*recordingJar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &recordingJar{Jar: jar, seen: make(map[string]*http.Cookie)}, nil
}

// SetCookies records each cookie (or its deletion) and forwards to
// the inner jar, which applies RFC 6265 domain/path/expiry rules for
// what later hops will actually send.
func (j *recordingJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	for _, c := range cookies {
		if c == nil {
			continue
		}
		if c.MaxAge < 0 || (!c.Expires.IsZero() && time.Now().After(c.Expires)) {
			delete(j.seen, c.Name)
			continue
		}
		if _, ok := j.seen[c.Name]; !ok {
			j.keys = append(j.keys, c.Name)
		}
		j.seen[c.Name] = c
	}
	j.mu.Unlock()
	j.Jar.SetCookies(u, cookies)
}

// cookies returns every cookie recorded across the chain in
// first-seen order, keyed by name with the latest hop winning.
func (j *recordingJar) cookies() []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]*http.Cookie, 0, len(j.keys))
	for _, name := range j.keys {
		if c := j.seen[name]; c != nil {
			out = append(out, c)
		}
	}
	return out
}

// mergedResponseCookies combines the cookies a request-scoped jar
// recorded across all redirect hops with the final response's own
// Set-Cookie values. The final response wins on name conflicts; that
// response's cookies are normally already inside the jar, but the
// transport-level retry path in Do() bypasses the jar entirely.
func mergedResponseCookies(jar *recordingJar, resp *http.Response) []*http.Cookie {
	seen := make(map[string]*http.Cookie)
	var order []string
	add := func(c *http.Cookie) {
		if c == nil {
			return
		}
		if _, ok := seen[c.Name]; !ok {
			order = append(order, c.Name)
		}
		seen[c.Name] = c
	}
	if jar != nil {
		for _, c := range jar.cookies() {
			add(c)
		}
	}
	if resp != nil {
		for _, c := range resp.Cookies() {
			add(c)
		}
	}
	out := make([]*http.Cookie, 0, len(order))
	for _, name := range order {
		out = append(out, seen[name])
	}
	return out
}
