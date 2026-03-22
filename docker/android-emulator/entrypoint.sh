#!/bin/bash
set -euo pipefail

# Virtual display (same resolution as pixel_7 skin)
Xvfb :1 -screen 0 1080x2400x24 &
sleep 1

# VNC server (no password, open for local debugging)
x11vnc -display :1 -nopw -forever -shared -quiet &

# noVNC web UI at http://localhost:6080
websockify --web /usr/share/novnc 6080 localhost:5900 &

mkdir -p /root/.android

# Keep the container's adb client identity stable so in-container adb usage
# survives container recreation as long as /root/.android is persisted.
if [ ! -f /root/.android/adbkey ]; then
    adb keygen /root/.android/adbkey
fi

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
