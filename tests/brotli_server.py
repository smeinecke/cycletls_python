#!/usr/bin/env python3
"""Minimal HTTP server that serves a brotli-compressed /brotli endpoint over HTTPS."""

import json
import os
import http.server
import socketserver
import ssl
import subprocess
import tempfile

import brotli

# Pre-compute brotli-compressed JSON {"brotli": true}
_BODY = json.dumps({"brotli": True}).encode("utf-8")
_COMPRESSED = brotli.compress(_BODY)


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/brotli":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Encoding", "br")
            self.end_headers()
            self.wfile.write(_COMPRESSED)
        else:
            self.send_error(404)

    def log_message(self, format, *args):
        pass  # suppress logging


def _generate_self_signed_cert(cert_path: str, key_path: str) -> None:
    subprocess.run(
        [
            "openssl", "req", "-x509", "-newkey", "rsa:4096",
            "-keyout", key_path,
            "-out", cert_path,
            "-sha256", "-days", "1", "-nodes",
            "-subj", "/CN=localhost",
            "-addext", "subjectAltName=IP:127.0.0.1,DNS:localhost",
        ],
        check=True,
        capture_output=True,
    )


if __name__ == "__main__":
    port = int(os.environ.get("BROTLI_PORT", "8081"))
    cert_path = os.environ.get("BROTLI_CERT")
    key_path = os.environ.get("BROTLI_KEY")

    if not cert_path or not key_path:
        tmpdir = tempfile.mkdtemp(prefix="brotli_server_")
        cert_path = os.path.join(tmpdir, "cert.pem")
        key_path = os.path.join(tmpdir, "key.pem")
        _generate_self_signed_cert(cert_path, key_path)

    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(cert_path, key_path)

    with socketserver.TCPServer(("", port), Handler) as httpd:
        httpd.socket = context.wrap_socket(httpd.socket, server_side=True)
        print(f"Brotli test server running on https://localhost:{port}")
        httpd.serve_forever()
