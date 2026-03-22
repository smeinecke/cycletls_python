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
