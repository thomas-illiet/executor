# Override these with `make <target> VAR=value` when you need a custom
# toolchain, binary name, tooling image, platform, workspace, or container name.
GO ?= go
BINARY ?= executor
DOCKERFILE ?= Dockerfile.dev
PLATFORM ?= linux/amd64
IMAGE ?= qemu-podman-proxy:dev
TOOLING_IMAGE ?= qemu-podman-proxy:tooling
WORKSPACE ?= $(CURDIR)
COMPOSE ?= docker compose
COMPOSE_FILE ?= docker-compose.yml
COMPOSE_SERVICE ?= executor-dev
CONTAINER_PORTS ?=
CONTAINER_NAME ?= qemu-podman-proxy
VM_ASSETS_DIR ?= dist/output
VM_SMOKE_ASSETS_DIR ?= .local/vm-secure-smoke-assets
VM_CONFIG_FILE ?= $(VM_ASSETS_DIR)/config.yaml
VM_ASSET_FILES := $(VM_ASSETS_DIR)/alpine-podman.qcow2 $(VM_ASSETS_DIR)/vmlinuz-virt $(VM_ASSETS_DIR)/initramfs-virt $(VM_ASSETS_DIR)/id_ed25519 $(VM_ASSETS_DIR)/id_ed25519.pub

# Host container run commands are intentionally pinned to x64. The local tooling
# image carries amd64 QEMU packages and is expected to behave the same on arm64 hosts.
DOCKER_RUN_PLATFORM := linux/amd64
COMPOSE_ENV := IMAGE="$(IMAGE)" DOCKERFILE="$(DOCKERFILE)" WORKSPACE="$(WORKSPACE)" VM_ASSETS_DIR="$(abspath $(VM_ASSETS_DIR))" DOCKER_RUN_PLATFORM="$(DOCKER_RUN_PLATFORM)"

# KVM is enabled automatically when /dev/kvm exists on the host.
KVM_ARGS ?= $(shell test -e /dev/kvm && echo --device /dev/kvm)

# Secure defaults used by the VM container targets. They mirror a restricted
# runtime: non-root user, read-only root filesystem, no extra capabilities, and
# writable tmpfs mounts for the paths the app needs at runtime.
SECURE_RUN_ARGS ?= --read-only --user appuser:appuser --cap-drop ALL --security-opt no-new-privileges
SECURE_TMPFS_ARGS ?= --tmpfs /home/appuser:rw,nosuid,nodev,uid=1000,gid=1000,mode=700,size=8g --tmpfs /tmp:rw,nosuid,nodev,uid=1000,gid=1000,mode=1777,size=1g --tmpfs /run:rw,nosuid,nodev,uid=1000,gid=1000,mode=755,size=64m

.PHONY: build test tidy clean docker-tooling-build docker-build docker-smoke docker-shell vm-asset-ready vm-asset vm-config vm-init vm-serve vm-exec vm-shutdown vm-secure-smoke help

##@ Local development

build: ## Build the local executor binary into bin/.
	$(GO) build -o bin/$(BINARY) .

test: ## Run the Go test suite.
	$(GO) test ./...

tidy: ## Tidy Go module dependencies.
	$(GO) mod tidy

clean: ## Remove generated local build artifacts.
	rm -rf bin dist .local

##@ Tooling image

docker-tooling-build:
	docker buildx build --target dev-base --platform $(PLATFORM) -f $(DOCKERFILE) --load -t $(TOOLING_IMAGE) .

docker-build: ## Build the local development/tooling image for PLATFORM.
	docker buildx build --target dev --platform $(PLATFORM) -f $(DOCKERFILE) --load -t $(IMAGE) .

docker-smoke: docker-build vm-asset-ready ## Verify the local tooling image starts executor.
	$(COMPOSE_ENV) $(COMPOSE) -f $(COMPOSE_FILE) run --rm --no-deps $(COMPOSE_SERVICE) sh -lc 'executor --version && test -s /home/appuser/.executor/alpine-podman.qcow2 && test -s /home/appuser/.executor/vmlinuz-virt && test -s /home/appuser/.executor/initramfs-virt && test -s /home/appuser/.executor/id_ed25519 && test -s /home/appuser/.executor/id_ed25519.pub'

docker-shell: docker-build vm-asset-ready ## Open an interactive shell with workspace and VM assets mounted.
	$(COMPOSE_ENV) $(COMPOSE) -f $(COMPOSE_FILE) run --rm --entrypoint /bin/bash $(COMPOSE_SERVICE)

##@ VM asset

vm-asset-ready:
	@missing=0; \
	for asset in $(VM_ASSET_FILES); do test -s "$$asset" || missing=1; done; \
	if [ "$$missing" -eq 0 ]; then \
		echo "Alpine VM assets already present in $(VM_ASSETS_DIR)."; \
	else \
		$(MAKE) vm-asset; \
	fi

vm-asset: docker-tooling-build ## Generate Alpine VM assets using the local tooling image.
	docker run --rm --platform $(DOCKER_RUN_PLATFORM) --user 0:0 \
		-v "$(WORKSPACE):/workspace" \
		-v "$(abspath $(VM_ASSETS_DIR)):/vm-assets" \
		-e EXECUTOR_VM_ASSETS_DIR=/vm-assets \
		--entrypoint /usr/local/share/executor/scripts/build-alpine.sh \
		$(TOOLING_IMAGE)

vm-config: ## Create the mounted executor config when it is missing.
	@mkdir -p "$(VM_ASSETS_DIR)"
	@if [ -f "$(VM_CONFIG_FILE)" ] && grep -Eq '^(engine:[[:space:]]*docker|docker:)' "$(VM_CONFIG_FILE)"; then \
		echo "Unsupported Docker config at $(VM_CONFIG_FILE); remove it before running vm-config." >&2; \
		exit 1; \
	fi
	@if [ ! -f "$(VM_CONFIG_FILE)" ]; then \
		{ \
			printf '%s\n' 'engine: podman'; \
			printf '%s\n' 'qemu:'; \
			printf '%s\n' '  binary: qemu-system-x86_64'; \
			printf '%s\n' '  accel: auto'; \
			printf '%s\n' '  io_profile: max'; \
			printf '%s\n' '  memory_mib: 4096'; \
			printf '%s\n' '  cpus: 4'; \
			printf '%s\n' 'host_share: 9p'; \
			printf '%s\n' 'guest_arch: amd64'; \
			printf '%s\n' 'podman:'; \
			printf '%s\n' '  registry_mirror: ""'; \
			printf '%s\n' '  data_root: /home/coder/.local/share/containers'; \
			printf '%s\n' '  disk_image: /home/appuser/.executor/podman-data.qcow2'; \
			printf '%s\n' '  disk_size: 10G'; \
			printf '%s\n' '  storage_driver: overlay'; \
			printf '%s\n' 'asset_mirror: https://github.com/thomas-illiet/executor/releases/latest/download'; \
			printf '%s\n' 'timeouts:'; \
			printf '%s\n' '  command: 2m'; \
			printf '%s\n' '  boot: 8m'; \
		} > "$(VM_CONFIG_FILE)"; \
		echo "Executor config written to $(VM_CONFIG_FILE)."; \
	else \
		echo "Executor config already present in $(VM_CONFIG_FILE)."; \
	fi

##@ VM container workflow

vm-init: ## Initialize QEMU and rootless Podman inside the running VM container.
	docker exec -it $(CONTAINER_NAME) executor init

vm-serve: docker-build vm-asset-ready vm-config ## Start the secured idle container used to host the VM.
	docker rm -f $(CONTAINER_NAME) >/dev/null 2>&1 || true
	docker run --name $(CONTAINER_NAME) --rm -it --platform $(DOCKER_RUN_PLATFORM) $(KVM_ARGS) \
		$(SECURE_RUN_ARGS) \
		$(SECURE_TMPFS_ARGS) \
		$(CONTAINER_PORTS) \
		-v "$(abspath $(VM_ASSETS_DIR)):/home/appuser/.executor" \
		-v "$(WORKSPACE):/workspace:ro" \
		$(IMAGE)

vm-exec: ## Run an executor command in the VM container, e.g. make vm-exec CMD="status".
	docker exec -it $(CONTAINER_NAME) executor $(CMD)

vm-shutdown: ## Stop Podman and the VM.
	docker exec -it $(CONTAINER_NAME) executor shutdown

vm-secure-smoke: docker-build vm-asset-ready vm-config ## Exercise the restricted container workflow end to end.
	docker rm -f $(CONTAINER_NAME)-smoke >/dev/null 2>&1 || true
	rm -rf "$(VM_SMOKE_ASSETS_DIR)"
	mkdir -p "$(VM_SMOKE_ASSETS_DIR)"
	for asset in alpine-podman.qcow2 vmlinuz-virt initramfs-virt id_ed25519 id_ed25519.pub config.yaml; do cp "$(VM_ASSETS_DIR)/$$asset" "$(VM_SMOKE_ASSETS_DIR)/$$asset"; done
	docker run --name $(CONTAINER_NAME)-smoke --rm -d --platform $(DOCKER_RUN_PLATFORM) \
		$(SECURE_RUN_ARGS) \
		$(SECURE_TMPFS_ARGS) \
		$(CONTAINER_PORTS) \
		-v "$(abspath $(VM_SMOKE_ASSETS_DIR)):/home/appuser/.executor" \
		-v "$(WORKSPACE):/workspace:ro" \
		$(IMAGE)
	docker exec $(CONTAINER_NAME)-smoke sh -lc 'test "$$(id -u)" = "1000" && touch "$$HOME"/rw-ok && ! touch /usr/local/ro-test'
	docker exec $(CONTAINER_NAME)-smoke executor init
	docker exec $(CONTAINER_NAME)-smoke sh -lc 'ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes -o ProxyCommand="nc -U $$HOME/.executor_runtime/ssh.sock" -i $$HOME/.executor/id_ed25519 coder@localhost true'
	docker exec $(CONTAINER_NAME)-smoke sh -lc '! ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes -o ProxyCommand="nc -U $$HOME/.executor_runtime/ssh.sock" -i $$HOME/.executor/id_ed25519 root@localhost true'
	docker exec $(CONTAINER_NAME)-smoke sh -lc '! ss -ltn | grep -E ":(2222|12343)[[:space:]]"'
	docker exec $(CONTAINER_NAME)-smoke executor pull alpine:3.20
	docker exec $(CONTAINER_NAME)-smoke executor run --rm alpine:3.20 echo secure-ok
	docker exec $(CONTAINER_NAME)-smoke executor compose --help
	docker exec $(CONTAINER_NAME)-smoke executor shutdown
	docker rm -f $(CONTAINER_NAME)-smoke >/dev/null
	rm -rf "$(VM_SMOKE_ASSETS_DIR)"

##@ Help

help: ## Show available targets and common override variables.
	@awk 'BEGIN {FS = ":.*## "; \
		printf "Usage:\n  make <target> [VAR=value]\n\nTargets:\n"} \
		/^##@ / {printf "\n%s\n", substr($$0, 5)} \
		/^[A-Za-z0-9_.-]+:.*## / {printf "  %-24s %s\n", $$1, $$2} \
		END {printf "\nCommon variables:\n  GO=%s\n  BINARY=%s\n  DOCKERFILE=%s\n  PLATFORM=%s\n  IMAGE=%s\n  TOOLING_IMAGE=%s\n  WORKSPACE=%s\n  VM_ASSETS_DIR=%s\n  VM_SMOKE_ASSETS_DIR=%s\n  COMPOSE_FILE=%s\n  COMPOSE_SERVICE=%s\n  CONTAINER_NAME=%s\n  CONTAINER_PORTS=%s\n", "$(GO)", "$(BINARY)", "$(DOCKERFILE)", "$(PLATFORM)", "$(IMAGE)", "$(TOOLING_IMAGE)", "$(WORKSPACE)", "$(VM_ASSETS_DIR)", "$(VM_SMOKE_ASSETS_DIR)", "$(COMPOSE_FILE)", "$(COMPOSE_SERVICE)", "$(CONTAINER_NAME)", "$(CONTAINER_PORTS)"}' $(MAKEFILE_LIST)
