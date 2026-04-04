#!/usr/bin/env bash
set -euo pipefail

exec docker compose -f docker-compose.android-capture.yml exec -T android adb "$@"
