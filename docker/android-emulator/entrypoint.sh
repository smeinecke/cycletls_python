#!/bin/bash
set -euo pipefail

# Virtual display (same resolution as pixel_7 skin)
Xvfb :1 -ac -screen 0 1080x2400x24 &

for _ in $(seq 1 20); do
    if [ -S /tmp/.X11-unix/X1 ]; then
        break
    fi
    sleep 0.5
done

# VNC server (no password, open for local debugging)
x11vnc -display :1 -nopw -forever -shared -quiet &

# noVNC web UI at http://localhost:6080
websockify --web /usr/share/novnc 6080 localhost:5900 &

# Local bridge target for adb-reversed HTTPS traffic from the emulator.
socat TCP-LISTEN:8443,bind=127.0.0.1,fork,reuseaddr TCP4:trackme-proxy:8443 &

mkdir -p /root/.android

# Reuse the host adb identity when available so host-side `adb connect` talks
# to the emulator with the same key the emulator sees during boot.
if [ -f /tmp/host-android/adbkey ] && [ -f /tmp/host-android/adbkey.pub ]; then
    cp /tmp/host-android/adbkey /root/.android/adbkey
    cp /tmp/host-android/adbkey.pub /root/.android/adbkey.pub
    chmod 600 /root/.android/adbkey
    chmod 644 /root/.android/adbkey.pub
elif [ ! -f /root/.android/adbkey ]; then
    # Fall back to a container-local adb identity when no host key is mounted.
    adb keygen /root/.android/adbkey
fi

# The persisted AVD volume can retain stale lock files after an unclean stop.
# Clear them before boot so a fresh container can reuse the same AVD safely.
find /root/.android/avd -maxdepth 3 \( -name "*.lock" -o -name "hardware-qemu.ini.lock" \) -print -delete 2>/dev/null || true

echo "[android-emu] Starting emulator ..."
# No -no-window: we want the screen rendered to Xvfb so noVNC shows the emulator UI.
emulator -port 5554 -avd test \
    -gpu swiftshader_indirect \
    -no-snapshot \
    -noaudio \
    -no-boot-anim \
    -metrics-collection &

echo "[android-emu] Waiting for boot (can take 3-5 min) ..."
adb -s emulator-5554 wait-for-device
until adb -s emulator-5554 shell getprop sys.boot_completed 2>/dev/null | tr -d '\r' | grep -q "^1$"; do
    sleep 3
done

echo "[android-emu] Emulator booted. ADB ready on port 5555."
echo "[android-emu] VNC viewer: http://localhost:6080"

# Keep the container alive
wait
