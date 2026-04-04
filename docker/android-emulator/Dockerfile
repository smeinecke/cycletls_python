FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive
ENV ANDROID_SDK_ROOT=/opt/android-sdk
ENV ANDROID_HOME=/opt/android-sdk
ENV PATH=$PATH:$ANDROID_SDK_ROOT/emulator:$ANDROID_SDK_ROOT/platform-tools:$ANDROID_SDK_ROOT/cmdline-tools/latest/bin
ENV DISPLAY=:1

# ── system dependencies ───────────────────────────────────────────────────────
RUN apt-get update && apt-get install -y --no-install-recommends \
    openjdk-17-jdk-headless \
    wget unzip curl \
    # emulator graphics / audio stubs
    libpulse0 libgl1 libx11-6 libxtst6 libxrender1 libxi6 \
    # virtual display + VNC
    xvfb x11vnc \
    # noVNC web UI
    novnc websockify \
    socat \
    # ADB dependencies
    udev \
    && rm -rf /var/lib/apt/lists/*

# ── Android SDK command-line tools ───────────────────────────────────────────
# Pin to the same version used by GitHub-hosted ubuntu-latest runners (9.0 / r9)
RUN mkdir -p $ANDROID_SDK_ROOT/cmdline-tools && \
    wget -q https://dl.google.com/android/repository/commandlinetools-linux-11076708_latest.zip \
         -O /tmp/ct.zip && \
    unzip -q /tmp/ct.zip -d /tmp && \
    mv /tmp/cmdline-tools $ANDROID_SDK_ROOT/cmdline-tools/latest && \
    rm /tmp/ct.zip

# ── SDK packages (same as android-emulator-runner in CI) ─────────────────────
RUN yes | sdkmanager --licenses > /dev/null 2>&1 && \
    sdkmanager \
        'build-tools;36.0.0' \
        'platform-tools' \
        'platforms;android-34' \
        'emulator' \
        'system-images;android-34;google_apis_playstore;x86_64'

# ── AVD (pixel_7 profile, same as CI) ────────────────────────────────────────
RUN echo no | avdmanager create avd --force -n test \
        --package 'system-images;android-34;google_apis_playstore;x86_64' \
        --device 'pixel_7' && \
    printf 'hw.cpu.ncore=2\n' >> /root/.android/avd/test.avd/config.ini

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# ADB (5554/5555), noVNC web UI (6080), CDP bridge (9222)
EXPOSE 5554 5555 6080 9222

ENTRYPOINT ["/entrypoint.sh"]
