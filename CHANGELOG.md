# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
