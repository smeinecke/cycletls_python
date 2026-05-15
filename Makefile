SHELL := /bin/bash
DOCKER_ANDROID_ADB := ./scripts/android_docker_adb.sh
DOCKER_ANDROID_EXPOSE_CDP := ./scripts/android_docker_expose_cdp.sh

.PHONY : docs
docs :
	rm -rf docs/build/
	sphinx-autobuild -b html --watch cycletls/ docs/source/ docs/build/

.PHONY : run-checks
run-checks :
	uv run ruff check cycletls
	uv run ruff format --check cycletls
	uv run pyright cycletls
	uv run pytest -v --color=yes tests/

# Local tlsfingerprint.com server setup
TLSFP_SERVER_DIR ?= .tlsfingerprint-server
TLSFP_CERT_BUNDLE ?= /tmp/cycletls-test-cas.crt

# Clone/update Danny-Dasilva/tlsfingerprint.com locally (mirrors CI checkout step)
.PHONY : tlsfingerprint-server
tlsfingerprint-server :
	@if [ ! -d "$(TLSFP_SERVER_DIR)" ]; then \
		echo "==> Cloning Danny-Dasilva/tlsfingerprint.com ..."; \
		git clone https://github.com/Danny-Dasilva/tlsfingerprint.com.git "$(TLSFP_SERVER_DIR)"; \
	else \
		echo "==> Updating Danny-Dasilva/tlsfingerprint.com ..."; \
		cd "$(TLSFP_SERVER_DIR)" && git pull; \
	fi

# Generate self-signed TLS certs for tlsfingerprint.com (skips if chain.pem already exists)
.PHONY : tlsfingerprint-certs
tlsfingerprint-certs : tlsfingerprint-server
	@mkdir -p "$(TLSFP_SERVER_DIR)/certs"
	@if [ ! -f "$(TLSFP_SERVER_DIR)/certs/chain.pem" ]; then \
		echo "==> Generating TLS certificates ..."; \
		openssl req -x509 -newkey rsa:4096 \
			-keyout "$(TLSFP_SERVER_DIR)/certs/key.pem" \
			-out "$(TLSFP_SERVER_DIR)/certs/chain.pem" \
			-sha256 -days 365 -nodes \
			-subj "/CN=localhost" \
			-addext "subjectAltName=IP:127.0.0.1,DNS:localhost"; \
	else \
		echo "Certs already exist ($(TLSFP_SERVER_DIR)/certs/chain.pem). Delete to regenerate."; \
	fi
	@echo "==> Generating config.json ..."
	@jq '.log_to_db = false | .mongo_url = "" | .device = ""' \
		"$(TLSFP_SERVER_DIR)/config.example.json" > "$(TLSFP_SERVER_DIR)/config.json"

# Legacy alias (kept for muscle memory; new code should use tlsfingerprint-certs)
.PHONY : trackme-certs
trackme-certs : tlsfingerprint-certs
	@echo "Notice: trackme-certs is deprecated, use tlsfingerprint-certs instead."

# Run Android Chrome fingerprint capture locally using Docker (budtmo/docker-android).
# Requires: Docker with KVM access (/dev/kvm), adb on PATH.
#
# The Android emulator persists its userdata in a named Docker volume so the
# Play Store image keeps its ADB trust state between runs. On first use, accept
# the ADB authorization dialog in the emulator and check "Always allow".
# A noVNC web UI is available at http://localhost:6080 to watch/interact with the
# emulator (useful for debugging Chrome's First Run Experience).
#
# Usage:
#   make android-capture-docker           # start emulator + capture
#   make android-capture-docker-stop      # tear down

# First run builds the image (downloads ~3 GB of SDK + system image — takes ~15 min).
# Subsequent runs reuse the cached image and start in ~3-5 min.
# Open http://localhost:6080 during the run to watch the emulator screen via noVNC.
.PHONY : android-capture-docker
android-capture-docker : tlsfingerprint-certs
	@echo "==> Tearing down any previous run ..."
	docker compose -f docker-compose.android-capture.yml down 2>/dev/null || true
	docker compose -f docker-compose.fingerprint-tests.yml down -v 2>/dev/null || true
	-$(DOCKER_ANDROID_ADB) disconnect emulator-5554 2>/dev/null || true
	adb disconnect localhost:5555 2>/dev/null || true
	@echo "==> Starting tlsfingerprint.com ..."
	docker compose -f docker-compose.fingerprint-tests.yml up -d --build tlsfingerprint
	@for i in $$(seq 1 40); do \
		ID=$$(docker compose -f docker-compose.fingerprint-tests.yml ps -q tlsfingerprint 2>/dev/null); \
		STATUS=$$(docker inspect --format '{{.State.Health.Status}}' $$ID 2>/dev/null || true); \
		if [ "$$STATUS" = "healthy" ]; then echo "tlsfingerprint.com is ready."; break; fi; \
		if [ $$i -eq 40 ]; then echo "tlsfingerprint.com did not become ready."; exit 1; fi; \
		sleep 3; \
	done

	@echo "==> Building + starting Android emulator (first run downloads ~3 GB) ..."
	@echo "    noVNC → http://localhost:6080"
	docker compose -f docker-compose.android-capture.yml up -d --build
	@echo "Waiting for emulator to boot (3-5 min) ..."
	@for i in $$(seq 1 120); do \
		BOOT=$$($(DOCKER_ANDROID_ADB) -s emulator-5554 shell getprop sys.boot_completed 2>/dev/null | tr -d '\r\n'); \
		if [ "$$BOOT" = "1" ]; then echo "Emulator booted."; break; fi; \
		if [ $$i -eq 120 ]; then echo "Emulator did not boot in time."; exit 1; fi; \
		sleep 5; \
	done

	@echo "==> Running Android capture ..."
	mkdir -p $(FINGERPRINT_ARTIFACTS_DIR)
	ADB_BIN="$(DOCKER_ANDROID_ADB)" ADB_REVERSE_TCP_PORTS="443" ANDROID_CDP_EXPOSE_CMD="$(DOCKER_ANDROID_EXPOSE_CDP)" uv run python scripts/capture_browser_fingerprints.py \
		--android-only \
		--adb-serial emulator-5554 \
		--url https://127.0.0.1 \
		--output $(FINGERPRINT_ARTIFACTS_DIR)/captured-android.json \
		--ignore-https-errors
	@echo "--- Captured Android fingerprints ---"
	@cat $(FINGERPRINT_ARTIFACTS_DIR)/captured-android.json

.PHONY : android-capture-docker-stop
android-capture-docker-stop :
	docker compose -f docker-compose.android-capture.yml down || true
	docker compose -f docker-compose.fingerprint-tests.yml down -v || true
	-$(DOCKER_ANDROID_ADB) disconnect emulator-5554 2>/dev/null || true
	adb disconnect localhost:5555 2>/dev/null || true

.PHONY : android-capture-docker-reset
android-capture-docker-reset :
	docker compose -f docker-compose.android-capture.yml down -v || true
	-$(DOCKER_ANDROID_ADB) disconnect emulator-5554 2>/dev/null || true
	adb disconnect localhost:5555 2>/dev/null || true

# Run Android Chrome fingerprint capture against a locally connected ADB device.
# Works with a real Android device (USB) or a local Android Studio AVD.
# Requires: adb on PATH, at least one device shown in `adb devices`.
#
# Usage:
#   make android-capture-local
#   make android-capture-local ADB_SERIAL=emulator-5554   # target a specific device

.PHONY : android-capture-local
android-capture-local : tlsfingerprint-certs
	@echo "==> Connected ADB devices:"
	@adb devices
	@echo ""
	@echo "==> Starting tlsfingerprint.com (port-mapped, accessible at 10.0.2.2:443 from emulator)..."
	docker compose -f docker-compose.fingerprint-tests.yml up -d --build tlsfingerprint
	@for i in $$(seq 1 40); do \
		ID=$$(docker compose -f docker-compose.fingerprint-tests.yml ps -q tlsfingerprint 2>/dev/null); \
		STATUS=$$(docker inspect --format '{{.State.Health.Status}}' $$ID 2>/dev/null || true); \
		if [ "$$STATUS" = "healthy" ]; then echo "tlsfingerprint.com is ready."; break; fi; \
		if [ $$i -eq 40 ]; then echo "tlsfingerprint.com did not become ready."; exit 1; fi; \
		sleep 3; \
	done
	@echo "==> Running Android capture..."
	mkdir -p $(FINGERPRINT_ARTIFACTS_DIR)
	uv run python scripts/capture_browser_fingerprints.py \
		--android-only \
		--url https://10.0.2.2 \
		--output $(FINGERPRINT_ARTIFACTS_DIR)/captured-android.json \
		--ignore-https-errors
	@echo "--- Captured Android fingerprints ---"
	@cat $(FINGERPRINT_ARTIFACTS_DIR)/captured-android.json
	@docker compose -f docker-compose.fingerprint-tests.yml down -v || true

# Run fingerprint replication tests locally (desktop browsers only, no Android).
# Steps mirror the CI fingerprint-tests job:
#   1. Start tlsfingerprint.com on a bridge network (reachable by the Playwright Docker container)
#      Port 443 is mapped to the host so CycleTLS tests on localhost also work.
#   2. Capture real browser fingerprints via Playwright
#   3. Run the fingerprint integration tests against https://localhost
#   4. Clean up
#
# Requires: Docker, uv, Go shared lib (run `make build-go-lib` first if missing)
FINGERPRINT_ARTIFACTS_DIR ?= tests/integration/artifacts

.PHONY : fingerprint-tests
fingerprint-tests : tlsfingerprint-certs
	@echo "==> Building combined CA bundle..."
	@cat /etc/ssl/certs/ca-certificates.crt "$(TLSFP_SERVER_DIR)/certs/chain.pem" > $(TLSFP_CERT_BUNDLE)
	@mkdir -p $(FINGERPRINT_ARTIFACTS_DIR)

	@echo "==> Starting tlsfingerprint.com (bridge network with port 443 mapped)..."
	@docker compose -f docker-compose.fingerprint-tests.yml up -d --build tlsfingerprint
	@echo "Waiting for tlsfingerprint.com to become healthy (up to 120s)..."
	@for i in $$(seq 1 40); do \
		ID=$$(docker compose -f docker-compose.fingerprint-tests.yml ps -q tlsfingerprint 2>/dev/null); \
		STATUS=$$(docker inspect --format '{{.State.Health.Status}}' $$ID 2>/dev/null || true); \
		if [ "$$STATUS" = "healthy" ]; then echo "tlsfingerprint.com is ready."; break; fi; \
		if [ $$i -eq 40 ]; then \
			echo "tlsfingerprint.com did not become ready. Logs:"; \
			docker compose -f docker-compose.fingerprint-tests.yml logs tlsfingerprint; \
			docker compose -f docker-compose.fingerprint-tests.yml down -v; \
			exit 1; \
		fi; \
		sleep 3; \
	done

	@echo "==> Capturing browser fingerprints via Playwright..."
	@docker compose -f docker-compose.fingerprint-tests.yml run --rm playwright-capture
	@echo "Playwright capture done."
	@echo "--- Captured fingerprints ---"
	@cat $(FINGERPRINT_ARTIFACTS_DIR)/captured.json

	@echo "==> Running fingerprint replication tests..."
	@TLSFP_URL=https://localhost \
	SSL_CERT_FILE=$(TLSFP_CERT_BUNDLE) \
	FINGERPRINT_FILE=$(FINGERPRINT_ARTIFACTS_DIR)/captured.json \
	uv run pytest -v --color=yes -m "fingerprint" \
		tests/integration/test_browser_fingerprint_replication.py; \
	EXIT_CODE=$$?; \
	docker compose -f docker-compose.fingerprint-tests.yml down -v || true; \
	exit $$EXIT_CODE

# Local package publishing
# Configure via environment variables:
#   PYPI_REPOSITORY_URL (required)
#   TWINE_USERNAME / TWINE_PASSWORD (optional, depends on local index auth)
#
# Go shared library build modes:
#   GO_LIB_BUILD=host                     -> use local Go toolchain (default)
#   GO_LIB_BUILD=docker-linux-glibc217   -> build Linux lib in Docker using Zig
#                                            targeting glibc 2.17 (as in release.yml)
GO_LIB_BUILD ?= docker-linux-glibc217
GO_DOCKER_IMAGE ?= golang:1.26-bookworm
ZIG_VERSION ?= 0.13.0
BUILD_DIR ?= build


.PHONY : clean
clean :
	rm -rf dist/ "$(BUILD_DIR)"/ docs/build/ cycletls.egg-info/
	rm -f cycletls/dist/*.so cycletls/dist/*.dylib cycletls/dist/*.dll cycletls/dist/*.h
	rm -rf zig-linux-x86_64-* zig-linux-aarch64-*
	rm -f zig-linux-*.tar.xz*

.PHONY : build-go-lib
build-go-lib :
	@case "$(GO_LIB_BUILD)" in \
		host) $(MAKE) build-go-lib-host ;; \
		docker-linux-glibc217) $(MAKE) build-go-lib-linux-glibc217 ;; \
		*) echo "Unknown GO_LIB_BUILD='$(GO_LIB_BUILD)' (expected: host | docker-linux-glibc217)"; exit 1 ;; \
	esac

.PHONY : build-go-lib-host
build-go-lib-host :
	chmod +x scripts/build_shared_lib.sh
	./scripts/build_shared_lib.sh

.PHONY : build-go-lib-linux-glibc217
build-go-lib-linux-glibc217 :
	docker run --rm \
		-v "$$(pwd):/work" \
		-w /work \
		-e HOST_UID="$$(id -u)" \
		-e HOST_GID="$$(id -g)" \
		"$(GO_DOCKER_IMAGE)" \
		bash -lc 'set -euo pipefail; \
			export PATH="/usr/local/go/bin:$$PATH"; \
			mkdir -p "/work/$(BUILD_DIR)/zig"; \
			apt-get update; \
			apt-get install -y --no-install-recommends wget xz-utils ca-certificates; \
			ZIG_VERSION="$(ZIG_VERSION)"; \
			ZIG_TAR="/work/$(BUILD_DIR)/zig/zig-linux-x86_64-$${ZIG_VERSION}.tar.xz"; \
			wget -q -O "$${ZIG_TAR}" "https://ziglang.org/download/$${ZIG_VERSION}/zig-linux-x86_64-$${ZIG_VERSION}.tar.xz"; \
			tar -C "/work/$(BUILD_DIR)/zig" -xf "$${ZIG_TAR}"; \
			ZIG_BIN="/work/$(BUILD_DIR)/zig/zig-linux-x86_64-$${ZIG_VERSION}/zig"; \
			printf '\''#!/bin/sh\nexec "%s" cc -target x86_64-linux-gnu.2.17 "$$@"\n'\'' "$${ZIG_BIN}" > /usr/local/bin/zcc; \
			chmod +x /usr/local/bin/zcc; \
			export CC=zcc; \
			export GOFLAGS="-buildvcs=false"; \
			chmod +x scripts/build_shared_lib.sh; \
			./scripts/build_shared_lib.sh; \
			cp -f cycletls/dist/libcycletls-linux-x64.so cycletls/dist/libcycletls.so; \
			if [ -f cycletls/dist/libcycletls-linux-x64.h ]; then cp -f cycletls/dist/libcycletls-linux-x64.h cycletls/dist/libcycletls.h; fi; \
			chown -R "$${HOST_UID}:$${HOST_GID}" cycletls/dist "/work/$(BUILD_DIR)"'

.PHONY : build-local
build-local :
	rm -rf dist/ "$(BUILD_DIR)"/
	$(MAKE) build-go-lib
	uv build --wheel

.PHONY : publish-local
publish-local : build-local
	@test -n "$$PYPI_REPOSITORY_URL" || (echo "PYPI_REPOSITORY_URL is not set"; exit 1)
	uv run --with twine twine upload --repository-url "$$PYPI_REPOSITORY_URL" dist/*
