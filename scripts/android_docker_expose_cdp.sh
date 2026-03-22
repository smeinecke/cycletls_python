#!/usr/bin/env bash
set -euo pipefail

HOST_PORT="${1:-9222}"
TARGET_PORT="${2:-9223}"
PID_FILE="/tmp/cdp-socat.pid"

docker compose -f docker-compose.android-capture.yml exec -T android sh -lc "
  if [ -f '${PID_FILE}' ]; then
    PID=\$(cat '${PID_FILE}' 2>/dev/null || true)
    if [ -n \"\${PID}\" ]; then
      kill \"\${PID}\" >/dev/null 2>&1 || true
    fi
    rm -f '${PID_FILE}'
  fi
"

docker compose -f docker-compose.android-capture.yml exec -d android sh -lc \
  "sh -c 'echo \$$ > ${PID_FILE}; exec socat TCP-LISTEN:${HOST_PORT},bind=0.0.0.0,fork,reuseaddr TCP:127.0.0.1:${TARGET_PORT} >/tmp/cdp-socat.log 2>&1'"
