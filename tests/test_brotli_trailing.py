"""Regression test for Brotli responses that contain trailing bytes.

Some servers (e.g. Brave Search over HTTP/2) send a complete Brotli stream
followed by one or more trailing bytes. The andybalholm/brotli decoder is
strict and rejects this with "brotli: excessive input". cycletls should
tolerate such responses and return the successfully decompressed body.
"""

import json
import os
import socketserver
import ssl
import subprocess
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import brotli
import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
import cycletls  # noqa: E402


def _generate_self_signed_cert(cert_path: str, key_path: str) -> None:
    subprocess.run(
        [
            "openssl",
            "req",
            "-x509",
            "-newkey",
            "rsa:2048",
            "-keyout",
            key_path,
            "-out",
            cert_path,
            "-sha256",
            "-days",
            "1",
            "-nodes",
            "-subj",
            "/CN=localhost",
            "-addext",
            "subjectAltName=IP:127.0.0.1,DNS:localhost",
        ],
        check=True,
        capture_output=True,
    )


class TrailingBrotliHandler(BaseHTTPRequestHandler):
    """Serve Brotli-compressed JSON with a trailing newline byte."""

    def do_GET(self):
        if self.path == "/brotli-trailing":
            accept_encoding = self.headers.get("Accept-Encoding", "")
            body = json.dumps(
                {
                    "brotli": True,
                    "method": "GET",
                    "headers": {"Accept-Encoding": accept_encoding},
                }
            ).encode("utf-8")
            compressed = brotli.compress(body)

            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Encoding", "br")
            self.send_header("Content-Length", str(len(compressed) + 1))
            self.end_headers()
            self.wfile.write(compressed)
            self.wfile.write(b"\n")  # trailing byte
        else:
            self.send_error(404)

    def log_message(self, format, *args):
        pass


@pytest.fixture(scope="module")
def trailing_brotli_url():
    tmpdir = tempfile.mkdtemp(prefix="cycletls_brotli_trailing_")
    cert_path = os.path.join(tmpdir, "cert.pem")
    key_path = os.path.join(tmpdir, "key.pem")
    _generate_self_signed_cert(cert_path, key_path)

    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(cert_path, key_path)

    server = ThreadingHTTPServer(("127.0.0.1", 0), TrailingBrotliHandler)
    server.socket = context.wrap_socket(server.socket, server_side=True)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()

    address = server.server_address
    assert isinstance(address, tuple) and len(address) == 2
    host, port = address
    url = f"https://{host}:{port}"
    time.sleep(0.2)

    yield f"{url}/brotli-trailing"

    server.shutdown()


def test_brotli_trailing_bytes_decompress(trailing_brotli_url):
    client = cycletls.CycleTLS()
    try:
        response = client.get(
            trailing_brotli_url,
            headers={
                "Accept-Encoding": "br",
                "User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
            },
            insecure_skip_verify=True,
            enable_connection_reuse=False,
        )

        assert response.status_code == 200
        body = response.content
        assert isinstance(body, bytes)

        # The body must be valid decompressed JSON, not raw Brotli bytes.
        data = json.loads(body.decode("utf-8"))
        assert data["brotli"] is True
        assert data["method"] == "GET"
        assert "br" in data["headers"]["Accept-Encoding"]
    finally:
        client.close()
