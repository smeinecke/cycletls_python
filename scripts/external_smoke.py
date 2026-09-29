#!/usr/bin/env python3
"""External smoke test: real TLS handshakes against strict production servers.

Why this exists: CI's live tests only talk to the self-hosted
tlsfingerprint.com container, whose TLS stack is lenient. A malformed
ClientHello extension (the ECH placeholder bug fixed in 0.0.11) passed all
of that while being rejected by BoringSSL-class parsers at Google,
Cloudflare and Fastly. This job exercises real end-to-end handshakes with
ja4r-bearing fingerprint profiles so that class of regression fails CI.

Any completed HTTP response counts as pass — including redirects and 4xx —
since what we verify is that the TLS handshake itself is accepted. Only an
exception (TLS error / decode alert) or a zero status fails.
"""

import sys

import cycletls
from cycletls.fingerprints import FingerprintRegistry

# Strict parsers (BoringSSL / strict TLS termination) — these rejected the
# malformed ECH extension — plus one lenient endpoint (CloudFront) as a
# control to distinguish TLS rejection from general network failure.
TARGETS = [
    "https://www.google.com",
    "https://www.cloudflare.com",
    "https://www.fastly.com",
    "https://aws.amazon.com",
]


def newest_ja4r_profile(prefix: str) -> str:
    """Pick the lexicographically newest <prefix>_* profile carrying a ja4r."""
    candidates = [
        name
        for name, fp in FingerprintRegistry.all().items()
        if name.startswith(prefix) and getattr(fp, "ja4r", None)
    ]
    if not candidates:
        raise RuntimeError(f"no {prefix}_* profile with ja4r in registry")
    return sorted(candidates)[-1]


def main() -> int:
    profiles = [newest_ja4r_profile("chrome_"), newest_ja4r_profile("firefox_")]
    print(f"Profiles under test: {profiles}")

    client = cycletls.CycleTLS()
    failures = 0
    try:
        for fp in profiles:
            for url in TARGETS:
                try:
                    resp = client.get(url, fingerprint=fp, timeout=25)
                    ok = resp.status_code > 0
                    print(f"{fp:28s} {url:30s} -> {resp.status_code}")
                    if not ok:
                        failures += 1
                except Exception as exc:
                    failures += 1
                    print(f"{fp:28s} {url:30s} FAILED: {str(exc)[:120]}")
    finally:
        client.close()

    if failures:
        print(f"\n{failures} handshake(s) failed", file=sys.stderr)
        return 1
    print("\nAll external smoke tests passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
