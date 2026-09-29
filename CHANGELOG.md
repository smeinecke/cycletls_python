# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
