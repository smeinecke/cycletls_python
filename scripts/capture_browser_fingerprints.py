#!/usr/bin/env python3
"""
Capture real browser TLS fingerprints from TrackMe using Playwright.

Discovers all launchable browser targets available in the current runtime
(OS/container), captures TrackMe /api/all for each, and writes normalized
fingerprint data to a JSON file that can be loaded by
``cycletls.fingerprints.load_trackme_fingerprints``.

Output schema:
{
  "schema": "trackme_browser_fingerprints/v1",
  "fingerprints": [
    {
      "name": "firefox_135_0",
      "browser": "firefox",
      "version": "135.0",
      "ja3": "...",
      "ja4_r": "...",
      "http2": "...",
      "ua": "...",
      "header_order": ["host", "user-agent", ...]
    }
  ]
}

Usage:
    python capture_browser_fingerprints.py \
        --url https://trackme:8443 \
        --output /tmp/fingerprints/captured.json \
        [--ignore-https-errors]
"""

import argparse
import json
import re
import subprocess
import sys
import time
import urllib.request
import xml.etree.ElementTree as ET
from datetime import UTC, datetime
from pathlib import Path

from playwright.sync_api import sync_playwright

_CHROME_PACKAGE = "com.android.chrome"
_CHROME_ACTIVITY = "com.google.android.apps.chrome.Main"


def _extract_header_order(data: dict) -> list[str]:
    """Extract header order from TrackMe response, excluding pseudo-headers."""
    http2 = data.get("http2", {})
    sent_frames = http2.get("sent_frames", [])

    for frame in sent_frames:
        if frame.get("frame_type") != "HEADERS":
            continue
        headers = frame.get("headers")
        if not isinstance(headers, list):
            continue

        ordered: list[str] = []
        for raw_header in headers:
            if not isinstance(raw_header, str):
                continue
            if raw_header.startswith(":"):
                continue
            if ":" not in raw_header:
                continue
            name = raw_header.split(":", 1)[0].strip().lower()
            if name:
                ordered.append(name)
        if ordered:
            return ordered

    return []


def _extract_browser_version(browser_name: str, user_agent: str) -> str:
    """Infer browser version from user-agent for deterministic profile naming."""
    token_map = {
        "chromium": r"(?:HeadlessChrome|Chrome)/([0-9.]+)",
        "chrome": r"(?:HeadlessChrome|Chrome)/([0-9.]+)",
        "chrome-beta": r"(?:HeadlessChrome|Chrome)/([0-9.]+)",
        "msedge": r"Edg/([0-9.]+)",
        "msedge-beta": r"Edg/([0-9.]+)",
        "msedge-dev": r"Edg/([0-9.]+)",
        "firefox": r"Firefox/([0-9.]+)",
        "safari": r"Version/([0-9.]+)",
        "webkit": r"Version/([0-9.]+)",
    }
    pattern = token_map.get(browser_name)
    if not pattern:
        return "unknown"

    match = re.search(pattern, user_agent)
    if not match:
        return "unknown"
    return match.group(1)


def _platform_suffix() -> str:
    """Return a short platform tag for the current OS."""
    if sys.platform == "win32":
        return "_win"
    if sys.platform == "darwin":
        return "_mac"
    return "_linux"


def _profile_name(browser_name: str, version: str) -> str:
    safe_version = re.sub(r"[^0-9A-Za-z]+", "_", version).strip("_") or "unknown"
    return f"{browser_name}_{safe_version}{_platform_suffix()}".lower()


def _candidate_targets() -> list[dict]:
    """Potential launch targets; availability is detected at runtime."""
    return [
        {"type": "chromium", "channel": None, "profile_browser": "chromium", "label": "chromium"},
        {"type": "chromium", "channel": "chrome", "profile_browser": "chrome", "label": "chromium:chrome"},
        {
            "type": "chromium",
            "channel": "chrome-beta",
            "profile_browser": "chrome-beta",
            "label": "chromium:chrome-beta",
        },
        {"type": "chromium", "channel": "msedge", "profile_browser": "msedge", "label": "chromium:msedge"},
        {
            "type": "chromium",
            "channel": "msedge-beta",
            "profile_browser": "msedge-beta",
            "label": "chromium:msedge-beta",
        },
        {
            "type": "chromium",
            "channel": "msedge-dev",
            "profile_browser": "msedge-dev",
            "label": "chromium:msedge-dev",
        },
        # NOTE: Firefox version is tied to the Playwright release — system Firefox cannot be used
        # because Playwright's Firefox driver requires its own Juggler-patched build.
        {"type": "firefox", "channel": None, "profile_browser": "firefox", "label": "firefox"},
        # Playwright's WebKit is our Safari-equivalent capture target.
        {"type": "webkit", "channel": None, "profile_browser": "safari", "label": "webkit:safari"},
    ]


def _discover_available_targets(playwright_instance) -> tuple[list[dict], dict[str, str]]:
    available: list[dict] = []
    unavailable: dict[str, str] = {}

    for target in _candidate_targets():
        browser_type = getattr(playwright_instance, target["type"], None)
        if browser_type is None:
            unavailable[target["label"]] = "browser type not available"
            continue

        launch_kwargs = {"headless": True}
        if target["channel"]:
            launch_kwargs["channel"] = target["channel"]
        if target.get("executable_path"):
            launch_kwargs["executable_path"] = target["executable_path"]

        try:
            browser = browser_type.launch(**launch_kwargs)
            browser.close()
            available.append(target)
            print(f"[discover] available: {target['label']}", flush=True)
        except Exception as exc:  # noqa: BLE001
            unavailable[target["label"]] = str(exc)
            print(f"[discover] unavailable: {target['label']} ({exc})", flush=True)

    return available, unavailable


def capture_fingerprint(playwright_instance, target: dict, url: str, ignore_https_errors: bool) -> dict:
    browser_type = getattr(playwright_instance, target["type"])
    launch_kwargs = {"headless": True}
    if target["channel"]:
        launch_kwargs["channel"] = target["channel"]
    if target.get("executable_path"):
        launch_kwargs["executable_path"] = target["executable_path"]

    browser = browser_type.launch(**launch_kwargs)
    context = browser.new_context(ignore_https_errors=ignore_https_errors)
    page = context.new_page()

    api_url = f"{url}/api/all"
    print(f"[{target['label']}] Fetching {api_url} ...", flush=True)

    response = page.goto(api_url, wait_until="domcontentloaded", timeout=30_000)
    if response is None or response.status != 200:
        status = response.status if response else "no response"
        raise RuntimeError(f"[{target['label']}] GET {api_url} returned status {status}")

    body = page.inner_text("body")
    data = json.loads(body)

    browser.close()

    tls = data.get("tls", {})
    http2 = data.get("http2", {})
    user_agent = data.get("user_agent") or ""

    profile_browser = target["profile_browser"]
    version = _extract_browser_version(profile_browser, user_agent)

    result = {
        "name": _profile_name(profile_browser, version),
        "browser": profile_browser,
        "version": version,
        "ja3": tls.get("ja3"),
        "ja4_r": tls.get("ja4_r"),
        "http2": http2.get("akamai_fingerprint"),
        "ua": user_agent,
        "header_order": _extract_header_order(data),
    }

    print(
        f"[{target['label']}] name={result['name']} ja3={bool(result['ja3'])} "
        f"http2={bool(result['http2'])} headers={len(result['header_order'])}",
        flush=True,
    )
    return result


def _wait_for_cdp(port: int, timeout: float = 45.0) -> None:
    """Block until Chrome's CDP HTTP endpoint at /json responds or timeout expires."""
    deadline = time.monotonic() + timeout
    url = f"http://localhost:{port}/json"
    while time.monotonic() < deadline:
        try:
            urllib.request.urlopen(url, timeout=2)
            return
        except Exception:  # noqa: BLE001
            time.sleep(1)
    raise RuntimeError(f"Chrome CDP endpoint {url} did not respond within {timeout:.0f}s")


def _adb_devices() -> list[str]:
    """Return serials of connected ADB devices (state == 'device')."""
    result = subprocess.run(["adb", "devices"], capture_output=True, text=True, timeout=15)
    serials: list[str] = []
    for line in result.stdout.splitlines()[1:]:
        parts = line.split()
        if len(parts) >= 2 and parts[1] == "device":
            serials.append(parts[0])
    return serials


def _write_chrome_cmdline_flags(serial: str, label: str) -> None:
    """Write Chrome command-line flags to the well-known location.

    Chrome on Android reads /data/local/tmp/chrome-command-line when the build
    has ro.debuggable=1 (google_apis emulators) or the CHROME_COMMAND_LINE
    feature is enabled.  On google_apis_playstore (user build) this has no
    effect but it is harmless.
    """
    flags = "chrome --disable-fre --no-first-run --no-default-browser-check"
    result = subprocess.run(
        ["adb", "-s", serial, "shell",
         f"echo '{flags}' > /data/local/tmp/chrome-command-line"
         " && chmod 664 /data/local/tmp/chrome-command-line"],
        capture_output=True, text=True, timeout=10,
    )
    if result.returncode == 0:
        print(f"[{label}] Wrote Chrome command-line flags file", flush=True)
    else:
        print(f"[{label}] Could not write Chrome flags file (non-fatal): {result.stderr.strip()}", flush=True)


def _dismiss_chrome_fre_ui(serial: str, label: str) -> bool:
    """Tap the first 'Accept'/'Continue'/'Agree' button found in the UI hierarchy.

    Uses uiautomator dump so it works regardless of screen resolution or exact
    button placement.  Returns True if a button was found and tapped.
    """
    _UI_DUMP = "/data/local/tmp/ui_dump.xml"
    # Dump the live UI hierarchy to a file on device.
    dump = subprocess.run(
        ["adb", "-s", serial, "shell", "uiautomator", "dump", _UI_DUMP],
        capture_output=True, text=True, timeout=20,
    )
    if dump.returncode != 0:
        print(f"[{label}] uiautomator dump failed: {dump.stderr.strip()!r}", flush=True)
        return False

    xml_result = subprocess.run(
        ["adb", "-s", serial, "shell", "cat", _UI_DUMP],
        capture_output=True, text=True, timeout=15,
    )
    xml_text = xml_result.stdout.strip()
    if not xml_text:
        print(f"[{label}] UI dump empty", flush=True)
        return False

    try:
        root = ET.fromstring(xml_text)
    except ET.ParseError as exc:
        print(f"[{label}] UI dump XML parse error: {exc}", flush=True)
        return False

    _ACCEPT_KEYWORDS = ("accept", "continue", "agree", "yes, i'm in", "got it", "next")
    for node in root.iter("node"):
        text = (node.get("text") or "").strip().lower()
        if not text:
            continue
        if any(kw in text for kw in _ACCEPT_KEYWORDS):
            bounds = node.get("bounds", "")
            m = re.match(r"\[(\d+),(\d+)\]\[(\d+),(\d+)\]", bounds)
            if not m:
                continue
            x = (int(m.group(1)) + int(m.group(3))) // 2
            y = (int(m.group(2)) + int(m.group(4))) // 2
            print(f"[{label}] Tapping FRE button '{text}' at ({x}, {y})", flush=True)
            subprocess.run(
                ["adb", "-s", serial, "shell", "input", "tap", str(x), str(y)],
                capture_output=True, timeout=5,
            )
            return True

    print(f"[{label}] No FRE button found in UI dump (Chrome may already be past FRE)", flush=True)
    return False


def _log_chrome_sockets(serial: str, label: str) -> None:
    """Print Chrome-related abstract Unix sockets for diagnostics."""
    # Read /proc/net/unix directly (no pipe to avoid shell timeout issues).
    result = subprocess.run(
        ["adb", "-s", serial, "shell", "cat", "/proc/net/unix"],
        capture_output=True, text=True, timeout=20,
    )
    chrome_lines = [
        ln for ln in result.stdout.splitlines() if "chrome" in ln.lower()
    ]
    if chrome_lines:
        print(f"[{label}] Chrome abstract sockets:\n" + "\n".join(chrome_lines), flush=True)
    else:
        print(f"[{label}] No Chrome abstract sockets found yet", flush=True)


def _capture_android_cdp(serial: str, url: str, ignore_https_errors: bool, local_port: int = 9222) -> dict:
    """Capture TLS fingerprint from an Android device via ADB port-forward + Playwright CDP."""
    label = f"android:{serial}"

    # Verify Chrome is installed before attempting to start it.
    pkg_check = subprocess.run(
        ["adb", "-s", serial, "shell", "pm", "list", "packages", _CHROME_PACKAGE],
        capture_output=True, text=True, timeout=15,
    )
    if _CHROME_PACKAGE not in pkg_check.stdout:
        raise RuntimeError(
            f"Chrome ({_CHROME_PACKAGE}) not installed on {serial}. "
            f"pm output: {pkg_check.stdout.strip()!r}"
        )

    # Try writing Chrome command-line flags (works on ro.debuggable=1 builds).
    _write_chrome_cmdline_flags(serial, label)

    # Force-stop any previous Chrome session so we start fresh.
    subprocess.run(
        ["adb", "-s", serial, "shell", "am", "force-stop", _CHROME_PACKAGE],
        capture_output=True, timeout=10,
    )
    time.sleep(1)

    print(f"[{label}] Starting Chrome (about:blank) ...", flush=True)
    start_result = subprocess.run(
        [
            "adb", "-s", serial, "shell",
            "am", "start",
            "-n", f"{_CHROME_PACKAGE}/{_CHROME_ACTIVITY}",
            "-a", "android.intent.action.VIEW",
            "-d", "about:blank",
            "--activity-clear-task",
            # Pass disable-fre via intent extra (Chrome release builds may honor this).
            "--es", "commandLineFlags", "--disable-fre --no-first-run",
        ],
        capture_output=True, text=True, timeout=20,
    )
    if start_result.stdout.strip():
        print(f"[{label}] am start: {start_result.stdout.strip()}", flush=True)
    if "Error" in start_result.stdout or start_result.returncode != 0:
        raise RuntimeError(f"am start failed: {start_result.stdout.strip()}")

    # Allow Chrome time to reach the FRE dialog before we try to dismiss it.
    print(f"[{label}] Waiting for Chrome to initialize (10 s) ...", flush=True)
    time.sleep(10)

    # Try the testing broadcast first (works on Chromium test builds).
    fre_bcast = subprocess.run(
        ["adb", "-s", serial, "shell", "am", "broadcast",
         "-a", "com.google.chrome.testing.ACCEPT_TERMS_OF_SERVICE"],
        capture_output=True, text=True, timeout=10,
    )
    print(f"[{label}] FRE broadcast: {fre_bcast.stdout.strip()}", flush=True)
    time.sleep(1)

    # Use uiautomator to find and tap the actual FRE accept button (up to 3 attempts).
    for attempt in range(1, 4):
        tapped = _dismiss_chrome_fre_ui(serial, label)
        if tapped:
            time.sleep(2)
            break
        if attempt < 3:
            time.sleep(3)

    print(f"[{label}] Forwarding CDP port {local_port} ...", flush=True)
    subprocess.run(
        ["adb", "-s", serial, "forward", f"tcp:{local_port}", "localabstract:chrome_devtools_remote"],
        check=True, timeout=10, capture_output=True,
    )

    _log_chrome_sockets(serial, label)

    print(f"[{label}] Waiting for Chrome DevTools to be ready ...", flush=True)
    _wait_for_cdp(local_port, timeout=90.0)

    data: dict = {}
    try:
        with sync_playwright() as pw:
            print(f"[{label}] Connecting via CDP ...", flush=True)
            browser = pw.chromium.connect_over_cdp(f"http://localhost:{local_port}")
            context = browser.contexts[0] if browser.contexts else browser.new_context()
            page = context.new_page()

            if ignore_https_errors:
                cdp = context.new_cdp_session(page)
                cdp.send("Security.setIgnoreCertificateErrors", {"ignore": True})

            api_url = f"{url}/api/all"
            print(f"[{label}] Fetching {api_url} ...", flush=True)
            response = page.goto(api_url, wait_until="domcontentloaded", timeout=60_000)
            if response is None or response.status != 200:
                status = response.status if response else "no response"
                raise RuntimeError(f"GET {api_url} returned status {status}")

            body = page.inner_text("body")
            data = json.loads(body)
            browser.close()
    finally:
        subprocess.run(
            ["adb", "-s", serial, "forward", "--remove", f"tcp:{local_port}"],
            capture_output=True, timeout=10,
        )

    tls = data.get("tls", {})
    http2 = data.get("http2", {})
    user_agent = data.get("user_agent") or ""

    version = _extract_browser_version("chrome", user_agent)
    safe_version = re.sub(r"[^0-9A-Za-z]+", "_", version).strip("_") or "unknown"

    result = {
        "name": f"chrome_android_{safe_version}_android",
        "browser": "chrome_android",
        "version": version,
        "ja3": tls.get("ja3"),
        "ja4_r": tls.get("ja4_r"),
        "http2": http2.get("akamai_fingerprint"),
        "ua": user_agent,
        "header_order": _extract_header_order(data),
    }
    print(
        f"[{label}] name={result['name']} ja3={bool(result['ja3'])} "
        f"http2={bool(result['http2'])} headers={len(result['header_order'])}",
        flush=True,
    )
    return result


def _main_android(args, output_path: Path) -> int:
    """Capture fingerprints from connected Android devices via ADB + CDP."""
    fingerprints: list[dict] = []
    errors: dict[str, str] = {}

    try:
        serials = _adb_devices()
    except Exception as exc:  # noqa: BLE001
        print(f"ERROR: ADB device discovery failed: {exc}", file=sys.stderr, flush=True)
        serials = []
        errors["adb_discovery"] = str(exc)

    if not serials:
        if "adb_discovery" not in errors:
            errors["adb_discovery"] = "No Android devices found via ADB"
            print("ERROR: no Android devices found via ADB", file=sys.stderr, flush=True)
    else:
        for serial in serials:
            label = f"android:{serial}"
            try:
                fp = _capture_android_cdp(serial, args.url, args.ignore_https_errors)
                fingerprints.append(fp)
            except Exception as exc:  # noqa: BLE001
                print(f"ERROR capturing {label}: {exc}", file=sys.stderr, flush=True)
                errors[label] = str(exc)

    payload = {
        "schema": "trackme_browser_fingerprints/v1",
        "captured_at": datetime.now(UTC).isoformat(),
        "source": {
            "type": "trackme",
            "url": args.url,
            "discovery": {"type": "android_adb"},
        },
        "fingerprints": fingerprints,
    }
    if errors:
        payload["errors"] = errors

    output_path.write_text(json.dumps(payload, indent=2))
    print(f"\nWrote Android fingerprints to {output_path}", flush=True)

    if errors:
        print(f"Android capture errors: {sorted(errors)}", file=sys.stderr)
        return 1
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description="Capture browser fingerprints via Playwright")
    parser.add_argument("--url", default="https://localhost:8443", help="TrackMe base URL")
    parser.add_argument("--output", required=True, help="Output JSON file path")
    parser.add_argument(
        "--android-only",
        action="store_true",
        help="Capture from connected Android devices via ADB only (skips desktop browsers). "
        "Use --url https://10.0.2.2:8443 when targeting an Android emulator.",
    )
    parser.add_argument(
        "--discover-only",
        action="store_true",
        help="Only detect available browser targets; do not call TrackMe",
    )
    parser.add_argument(
        "--require-browsers",
        default="",
        help="Comma-separated profile browser names required to be available "
        "(e.g. chrome,msedge,safari)",
    )
    parser.add_argument(
        "--ignore-https-errors",
        action="store_true",
        default=True,
        help="Ignore HTTPS certificate errors (default: True for self-signed certs)",
    )
    args = parser.parse_args()

    output_path = Path(args.output)
    output_path.parent.mkdir(parents=True, exist_ok=True)

    if args.android_only:
        return _main_android(args, output_path)

    fingerprints: list[dict] = []
    errors: dict[str, str] = {}

    with sync_playwright() as pw:
        available_targets, unavailable_targets = _discover_available_targets(pw)
        if not available_targets:
            print("ERROR: no playable browser targets found", file=sys.stderr, flush=True)
            payload = {
                "schema": "trackme_browser_fingerprints/v1",
                "captured_at": datetime.now(UTC).isoformat(),
                "source": {"type": "trackme", "url": args.url},
                "fingerprints": [],
                "errors": unavailable_targets,
            }
            output_path.write_text(json.dumps(payload, indent=2))
            return 1

        available_browsers = sorted({t["profile_browser"] for t in available_targets})
        required = [
            item.strip().lower()
            for item in args.require_browsers.split(",")
            if item.strip()
        ]
        missing_required = sorted(set(required) - set(available_browsers))
        if missing_required:
            errors["required_browsers"] = (
                f"Missing required browsers: {missing_required}; available={available_browsers}"
            )

        if args.discover_only:
            payload = {
                "schema": "trackme_browser_fingerprints/v1",
                "captured_at": datetime.now(UTC).isoformat(),
                "source": {
                    "type": "playwright-discovery",
                    "url": args.url,
                    "discovery": {
                        "available_targets": [t["label"] for t in available_targets],
                        "available_browsers": available_browsers,
                    },
                },
                "fingerprints": [],
            }
            if unavailable_targets:
                payload["unavailable_targets"] = unavailable_targets
            if errors:
                payload["errors"] = errors
            output_path.write_text(json.dumps(payload, indent=2))
            print(f"\nWrote discovery results to {output_path}", flush=True)
            return 1 if errors else 0

        for target in available_targets:
            label = target["label"]
            try:
                fp = capture_fingerprint(pw, target, args.url, args.ignore_https_errors)
                fingerprints.append(fp)
            except Exception as exc:  # noqa: BLE001
                print(f"ERROR capturing {label}: {exc}", file=sys.stderr, flush=True)
                errors[label] = str(exc)

    payload = {
        "schema": "trackme_browser_fingerprints/v1",
        "captured_at": datetime.now(UTC).isoformat(),
        "source": {
            "type": "trackme",
            "url": args.url,
            "discovery": {
                "available_targets": [t["label"] for t in available_targets],
                "failed_targets": sorted(set(errors) | set(unavailable_targets)),
            },
        },
        "fingerprints": fingerprints,
    }
    if unavailable_targets:
        payload["unavailable_targets"] = unavailable_targets
    if errors:
        payload["errors"] = errors

    output_path.write_text(json.dumps(payload, indent=2))
    print(f"\nWrote fingerprints to {output_path}", flush=True)

    if errors:
        print(f"Failed captures: {sorted(errors)}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
