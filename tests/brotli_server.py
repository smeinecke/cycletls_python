#!/usr/bin/env python3
"""Minimal HTTP server that serves a brotli-compressed /brotli endpoint."""

import json
import os
import http.server
import socketserver

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


if __name__ == "__main__":
    port = int(os.environ.get("BROTLI_PORT", "8081"))
    with socketserver.TCPServer(("", port), Handler) as httpd:
        print(f"Brotli test server running on port {port}")
        httpd.serve_forever()
