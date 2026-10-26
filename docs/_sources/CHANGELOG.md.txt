# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `header_values` request parameter accepts `dict[str, list[str]]` so duplicate header names and exact casing (e.g. lowercase HTTP/2-style names) can be sent - `headers`/`Header.Set()` previously canonicalized casing and made duplicate names impossible (upstream #423).
- `response.proto` exposes the negotiated HTTP protocol (`"HTTP/1.1"`, `"HTTP/2.0"`) on every response (upstream #379).
- Every built-in fingerprint profile now has a package-level constant. The registry ships 149 captured profiles (Chrome 113-156, Chromium, Firefox 121-157, Edge/MS Edge 120-156, Safari 17-27.2, plus Brave, Opera, Samsung and Android/iOS variants); all are importable as constants like `CHROME_156`, `CHROME_156_WIN`, `FIREFOX_157_LINUX`, `SAFARI_27`, `EDGE_156_WIN`, or usable by name via `fingerprint="chrome_156_0_0_0_linux"`.

### Fixed
- Default ClientHello is no longer a stale pre-permutation Chrome hello: `DefaultChrome_JA3` is now a captured Chrome 149 fingerprint (ECH-GREASE, ALPS, X25519MLKEM768 key share), and requests that don't pass `ja3`/`ja4r` go through the same TLS-1.3-compatible spec dispatch as explicit JA3s instead of emitting a TLS 1.2-only hello. Fixes bot-check hard-rejects (e.g. Yahoo Search's `_bv` challenge returning HTTP 500 instead of the 307 cookie flow).
- Default `user_agent` is now a matching Chrome 149 Linux UA (was Firefox/87.0), so GREASE placeholders and HTTP/2 pseudo-header order are consistent with the default hello.
- A pooled client (`enable_connection_reuse=true`, the default) no longer dies permanently after the peer closes its connection. The cached connection is now a one-time hand-off to the transport's dial callback instead of being re-served on every redial; a peer close previously poisoned the pool entry with `use of closed network connection` on every subsequent request.
- `Set-Cookie` values on intermediate redirect hops are no longer dropped. Each request now carries a request-scoped RFC 6265 cookie jar (the pooled `fhttp.Client` is a struct copy, so no cookies leak between requests), redirect hops receive accumulated cookies like a browser session jar, and `response.cookies` reports the merged set from the whole chain - `Session.cookies` therefore persists cookies set mid-redirect (e.g. Yahoo's `YBV` upgrade).
- Non-pooled clients (`enable_connection_reuse=false`) now close the connections they open instead of leaking a socket per request into `cachedConnections` (upstream #426).
- The HTTP/1 wire no longer leaks the `PHeader-Order` pseudo-header; header ordering is applied at request construction instead.
- Decompression bombs are rejected: gzip/deflate/brotli bodies are capped at 64 MiB decompressed and `github.com/andybalholm/brotli` is bumped to v1.2.7 to fix a block-switch panic a hostile server could trigger via `Content-Encoding: br` or `compress_certificate` (CVE-2026-111497 / HTTP2Shell, upstream #430).

## [0.0.12] - 2026-09-29

### Fixed
- 3xx responses without a `Location` header no longer collapse to status 0 with all headers dropped; the real status, headers and body are returned for GET and POST whether or not redirect following is enabled (upstream #406).
- Fingerprint fidelity: `ja4r` profiles now emit ciphers, extensions and `supported_groups` in the browser's captured wire order (restored from the profile's `ja3`), so the emitted JA3 and JA4_r match the profile byte-for-byte instead of JA4_r's sorted order.
- Chromium-family profiles emit the GREASE placeholders real Chrome sends (cipher, supported version, group, key share and first/last extensions); Firefox stays grease-free and `disableGrease` suppresses all of them.
- TLS 1.3 `key_share` now offers an X25519MLKEM768 share, fixing handshakes against servers that select the advertised post-quantum group via HelloRetryRequest.
- `compress_certificate` uses the real uTLS extension so `CompressedCertificate` responses can actually be decoded; Firefox profiles advertise its real zlib/brotli/zstd algorithm list.

### Changed
- `ja3` is sent to the Go backend alongside `ja4r` (previously suppressed); `ja4r` remains authoritative while `ja3` provides wire-order hints.
- Removed unused `websockets` and `websocket-client` core dependencies; README now documents installation via the fork's PEP 503 index.
- CI now runs a strict-server handshake smoke test (Google, Cloudflare, Fastly, AWS) on every push/PR and daily.

## [0.0.11] - 2026-09-29

### Fixed
- Fingerprinted requests (any `ja4r` profile) no longer fail the TLS handshake against strict servers (Google, Cloudflare, Fastly): extension `0xfe0d` (ECH) is now emitted as a valid randomized GREASE ECH instead of an all-zero placeholder that triggered `tls: error decoding message` alerts.
- `supported_groups` on the JA4R path no longer lists X25519 twice; TLS 1.3 hellos now lead with the post-quantum hybrid X25519MLKEM768, matching real browser fingerprints.

### Changed
- Bumped `github.com/quic-go/quic-go` to 0.63.0, `golang.org/x/net` to 0.59.0 and `github.com/andybalholm/brotli` to 1.2.5 in the Go backend; `github.com/valyala/fasthttp` to 1.74.0 in benchmarks.
- Bulk-updated Python dependencies in `uv.lock`.
- Bumped `github/codeql-action` to 4.38.2, `astral-sh/setup-uv` to 10.2.0 and `actions/download-artifact` to v8 in CI.
- Fingerprint registry refreshed from scheduled CI captures.

## [0.0.9] - 2026-08-31

### Fixed
- `Request.to_dict()` no longer suppresses `ja3` when `http2_fingerprint` or `quic_fingerprint` is set, so built-in `fingerprint="..."` profiles now send their stored JA3.

### Changed
- Bumped `golang.org/x/net` to 0.58.0 in the Go backend.
- Bumped `github/codeql-action` to 4.37.8.
- Bumped `astral-sh/setup-uv` to 10.0.1.

## [0.0.8] - 2026-07-08

### Fixed
- Brotli decompression no longer fails when servers append trailing bytes after a valid Brotli stream (e.g. Brave Search over HTTP/2)

### Added
- `local_address` parameter to bind outgoing TCP connections to a specific local IP for outbound interface/IP selection (#65)
- Regression test for Brotli responses with trailing bytes

### Fixed
- `Do()` dropped `ServerName`, `TLS13AutoRetry`, and `DisableGrease` request fields when constructing the underlying request (#65)
- `TLS13AutoRetry` proactive upgrade corrupted JA3 `supported_groups`; the original JA3 ordering is now preserved across the retry (#65)
- `dispatchSSEAsync` could enter an infinite loop on stream cancel/EOF (#65)
