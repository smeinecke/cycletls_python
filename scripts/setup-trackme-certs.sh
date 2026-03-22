#!/usr/bin/env bash
# Generates self-signed TLS certs for the local TrackMe test instance.
# Usage:
#   ./scripts/setup-trackme-certs.sh              # generate certs only
#   ./scripts/setup-trackme-certs.sh --install-ca  # generate + install CA into system trust store

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CERT_DIR="$SCRIPT_DIR/../docker/trackme/certs"

mkdir -p "$CERT_DIR"

if [ ! -f "$CERT_DIR/chain.pem" ] || [ ! -f "$CERT_DIR/key.pem" ]; then
    echo "Generating self-signed TLS certificates for TrackMe..."
    openssl req -x509 -newkey rsa:2048 \
        -keyout "$CERT_DIR/key.pem" \
        -out "$CERT_DIR/chain.pem" \
        -days 365 -nodes \
        -subj "/CN=localhost" \
        -addext "subjectAltName=IP:127.0.0.1,DNS:localhost"
    echo "Certificates written to $CERT_DIR/"
else
    echo "Certificates already exist in $CERT_DIR/, skipping generation."
fi

if [ "${1:-}" = "--install-ca" ]; then
    echo "Installing CA certificate into system trust store..."
    sudo cp "$CERT_DIR/chain.pem" /usr/local/share/ca-certificates/trackme-test.crt
    sudo update-ca-certificates
    echo "CA certificate installed. Go clients will now trust the local TrackMe instance."
fi

# Always print the SSL_CERT_FILE hint for no-sudo usage
COMBINED="/tmp/combined-trackme-cas.crt"
cat /etc/ssl/certs/ca-certificates.crt "$CERT_DIR/chain.pem" > "$COMBINED"
echo ""
echo "To run tests without sudo, set SSL_CERT_FILE before pytest:"
echo "  SSL_CERT_FILE=$COMBINED TRACKME_URL=https://localhost:8443 uv run pytest -m blocking tests/"
