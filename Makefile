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

# Generate self-signed TLS certs for TrackMe (skips if chain.pem already exists)
.PHONY : trackme-certs
trackme-certs :
	@if [ -f docker/trackme/certs/chain.pem ]; then \
		echo "Certs already exist (docker/trackme/certs/chain.pem). Delete to regenerate."; \
	else \
		mkdir -p docker/trackme/certs; \
		openssl req -x509 -newkey rsa:2048 \
			-keyout docker/trackme/certs/key.pem \
			-out docker/trackme/certs/chain.pem \
			-days 365 -nodes \
			-subj "/CN=trackme" \
			-addext "subjectAltName=DNS:trackme,DNS:localhost,IP:127.0.0.1,IP:10.0.2.2"; \
		echo "Certs generated."; \
	fi

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
android-capture-docker : trackme-certs
	@echo "==> Tearing down any previous run ..."
	docker compose -f docker-compose.android-capture.yml down 2>/dev/null || true
	docker compose -f docker-compose.fingerprint-tests.yml down -v 2>/dev/null || true
	-$(DOCKER_ANDROID_ADB) disconnect emulator-5554 2>/dev/null || true
	adb disconnect localhost:5555 2>/dev/null || true
	@echo "==> Starting TrackMe ..."
	docker compose -f docker-compose.fingerprint-tests.yml up -d --build trackme
	@for i in $$(seq 1 40); do \
		ID=$$(docker compose -f docker-compose.fingerprint-tests.yml ps -q trackme 2>/dev/null); \
		STATUS=$$(docker inspect --format '{{.State.Health.Status}}' $$ID 2>/dev/null || true); \
		if [ "$$STATUS" = "healthy" ]; then echo "TrackMe is ready."; break; fi; \
		if [ $$i -eq 40 ]; then echo "TrackMe did not become ready."; exit 1; fi; \
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
	ADB_BIN="$(DOCKER_ANDROID_ADB)" ADB_REVERSE_TCP_PORTS="8443" ANDROID_CDP_EXPOSE_CMD="$(DOCKER_ANDROID_EXPOSE_CDP)" uv run python scripts/capture_browser_fingerprints.py \
		--android-only \
		--adb-serial emulator-5554 \
		--url https://127.0.0.1:8443 \
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
android-capture-local : trackme-certs
	@echo "==> Connected ADB devices:"
	@adb devices
	@echo ""
	@echo "==> Starting TrackMe (port-mapped, accessible at 10.0.2.2:8443 from emulator)..."
	docker compose -f docker-compose.fingerprint-tests.yml up -d --build trackme
	@for i in $$(seq 1 40); do \
		ID=$$(docker compose -f docker-compose.fingerprint-tests.yml ps -q trackme 2>/dev/null); \
		STATUS=$$(docker inspect --format '{{.State.Health.Status}}' $$ID 2>/dev/null || true); \
		if [ "$$STATUS" = "healthy" ]; then echo "TrackMe is ready."; break; fi; \
		if [ $$i -eq 40 ]; then echo "TrackMe did not become ready."; exit 1; fi; \
		sleep 3; \
	done
	@echo "==> Running Android capture..."
	mkdir -p $(FINGERPRINT_ARTIFACTS_DIR)
	uv run python scripts/capture_browser_fingerprints.py \
		--android-only \
		--url https://10.0.2.2:8443 \
		--output $(FINGERPRINT_ARTIFACTS_DIR)/captured-android.json \
		--ignore-https-errors
	@echo "--- Captured Android fingerprints ---"
	@cat $(FINGERPRINT_ARTIFACTS_DIR)/captured-android.json
	@docker compose -f docker-compose.fingerprint-tests.yml down -v || true

# Run fingerprint replication tests locally (desktop browsers only, no Android).
# Steps mirror the CI fingerprint-tests job:
#   1. Start TrackMe on a bridge network (reachable by the Playwright Docker container)
#   2. Capture real browser fingerprints via Playwright
#   3. Switch TrackMe to host-network mode (reachable by CycleTLS on localhost)
#   4. Run the fingerprint integration tests
#   5. Clean up
#
# Requires: Docker, uv, Go shared lib (run `make build-go-lib` first if missing)
FINGERPRINT_ARTIFACTS_DIR ?= tests/integration/artifacts
SSL_CERT_BUNDLE ?= /tmp/cycletls-test-cas.crt

.PHONY : fingerprint-tests
fingerprint-tests : trackme-certs
	@echo "==> Building combined CA bundle..."
	cat /etc/ssl/certs/ca-certificates.crt docker/trackme/certs/chain.pem > $(SSL_CERT_BUNDLE)
	mkdir -p $(FINGERPRINT_ARTIFACTS_DIR)

	@echo "==> Starting TrackMe (bridge network)..."
	docker compose -f docker-compose.fingerprint-tests.yml up -d --build trackme
	@echo "Waiting for TrackMe to become healthy (up to 120s)..."
	@for i in $$(seq 1 40); do \
		ID=$$(docker compose -f docker-compose.fingerprint-tests.yml ps -q trackme 2>/dev/null); \
		STATUS=$$(docker inspect --format '{{.State.Health.Status}}' $$ID 2>/dev/null || true); \
		if [ "$$STATUS" = "healthy" ]; then echo "TrackMe is ready."; break; fi; \
		if [ $$i -eq 40 ]; then \
			echo "TrackMe did not become ready. Logs:"; \
			docker compose -f docker-compose.fingerprint-tests.yml logs trackme; \
			docker compose -f docker-compose.fingerprint-tests.yml down -v; \
			exit 1; \
		fi; \
		sleep 3; \
	done

	@echo "==> Capturing browser fingerprints via Playwright..."
	docker compose -f docker-compose.fingerprint-tests.yml run --rm playwright-capture
	@echo "Playwright capture done."
	@echo "--- Captured fingerprints ---"
	@cat $(FINGERPRINT_ARTIFACTS_DIR)/captured.json

	@echo "==> Switching TrackMe to host-network mode..."
	docker compose -f docker-compose.fingerprint-tests.yml rm -f -s trackme
	docker compose -f docker-compose.test.yml up -d --build
	@echo "Waiting for TrackMe (host-mode) to become healthy (up to 90s)..."
	@for i in $$(seq 1 30); do \
		STATUS=$$(docker inspect --format '{{.State.Health.Status}}' cycletls_python-trackme-1 2>/dev/null || true); \
		if [ "$$STATUS" = "healthy" ]; then echo "TrackMe (host-mode) is ready."; break; fi; \
		if [ $$i -eq 30 ]; then \
			echo "TrackMe (host-mode) did not become ready. Logs:"; \
			docker logs cycletls_python-trackme-1; \
			docker compose -f docker-compose.fingerprint-tests.yml down -v; \
			docker compose -f docker-compose.test.yml down; \
			exit 1; \
		fi; \
		sleep 3; \
	done

	@echo "==> Running fingerprint replication tests..."
	TRACKME_URL=https://localhost:8443 \
	SSL_CERT_FILE=$(SSL_CERT_BUNDLE) \
	FINGERPRINT_FILE=$(FINGERPRINT_ARTIFACTS_DIR)/captured.json \
	uv run pytest -v --color=yes -m "fingerprint" \
		tests/integration/test_browser_fingerprint_replication.py; \
	EXIT_CODE=$$?; \
	docker compose -f docker-compose.fingerprint-tests.yml down -v || true; \
	docker compose -f docker-compose.test.yml down || true; \
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
