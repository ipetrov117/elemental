GINKGO       ?= github.com/onsi/ginkgo/v2/ginkgo
GINKGO_ARGS  ?= -v --fail-fast -r --timeout=3h

IMG_NAME?=elemental-os-image-$(ARCH)

### Elemental install specific variables ###
IMG?=$(BUILD_DIR)/$(IMG_NAME)
DISK?=$(realpath $(IMG).qcow2)
DISKSIZE?=20G
OS_REPO?=registry.opensuse.org/devel/unifiedcore/tumbleweed/containers/uc-base-os-kernel-default
OS_VERSION?=latest
DOCKER_SOCK?=/var/run/docker.sock

### Elemental customize specific variables ###
RELEASE_MANIFEST?=registry.opensuse.org/devel/unifiedcore/tumbleweed/containers/release-manifest:latest
CONFIG_DIR?=./tests/testdata/config-dir
CUSTOMIZED_WORKDIR_NAME=customized
CUSTOMIZED_WORKDIR_PATH?=$(CONFIG_DIR)/$(CUSTOMIZED_WORKDIR_NAME)
CUSTOMIZED_IMG_MAC=FE:C4:05:42:8B:AB
CUSTOMIZED_ISO_PREFIX=$(IMG_NAME)-test-customize-iso-
CUSTOMIZED_RAW_PREFIX=$(IMG_NAME)-test-customize-raw-

$(CONFIG_DIR)/release.yaml:
	@echo "manifestURI: oci://$(RELEASE_MANIFEST)" > "$@"

define CUSTOMIZE
	$(DOCKER) run --rm \
		--volume "$(CONFIG_DIR):/config" \
		--volume /run/podman/podman.sock:/var/run/docker.sock \
		"$(ELEMENTAL_IMAGE_REPO):$(VERSION)" \
		--debug customize \
		--type "$(MEDIA_TYPE)" \
		--output "/config/$(CUSTOMIZED_WORKDIR_NAME)/$(IMG_NAME).$(MEDIA_TYPE)" \
		--local
endef

# Use the same shell for all commands in a target, useful for the build mainly
.ONESHELL:

.PHONY: build-disk
build-disk: RUNNER := runner-elemental3ctl
build-disk: $(BUILD_DIR) image
	qemu-img create -f raw $(IMG).raw $(DISKSIZE)
	TARGET=$$(sudo losetup -f --show $(IMG).raw)
	cp examples/elemental/install/config.sh $(BUILD_DIR)
	$(DOCKER) run --rm \
		--volume $(DOCKER_SOCK):$(DOCKER_SOCK) \
		--volume $(BUILD_DIR):/build \
		--volume /dev:/dev \
		--volume /run/udev:/run/udev:ro \
		--privileged \
		$(ELEMENTAL_IMAGE_REPO):$(VERSION) \
		--debug install --os-image $(OS_REPO):$(OS_VERSION) --target $${TARGET} --cmdline "console=ttyS0,115200" --config /build/config.sh
	BUILD_ERR=$$?
	test $${BUILD_ERR} -eq 0 && qemu-img convert -c -p -O qcow2 $(IMG).raw $(IMG).qcow2
	sudo losetup -d $${TARGET}
	exit $${BUILD_ERR}

.PHONY: customize-raw
customize-raw: RUNNER := runner-elemental3
customize-raw: MEDIA_TYPE := raw
customize-raw: image $(CONFIG_DIR)/release.yaml
	$(CUSTOMIZE)

.PHONY: customize-iso
customize-iso: RUNNER := runner-elemental3
customize-iso: MEDIA_TYPE := iso
customize-iso: image $(CONFIG_DIR)/release.yaml
	$(CUSTOMIZE)

.PHONY: test-installer
test-installer: build-disk
	@scripts/run_vm.sh start "$(DISK)"
	@echo "VM started from $(DISK)"

	VM_PID=$$(scripts/run_vm.sh vmpid) go run $(GINKGO) $(GINKGO_ARGS) ./tests/installer

.PHONY: test-customize-raw test-customize-iso
test-customize-raw: CUSTOMIZED_IMAGE := $(CUSTOMIZED_WORKDIR_PATH)/$(IMG_NAME).raw
test-customize-raw: customize-raw
	@ELMNTL_PREFIX=$(CUSTOMIZED_RAW_PREFIX) ELMNTL_MAC=$(CUSTOMIZED_IMG_MAC) scripts/run_vm.sh start "$(CUSTOMIZED_IMAGE)"
	@echo "VM started from $(CUSTOMIZED_IMAGE)"
	VM_PID=$$(scripts/run_vm.sh vmpid) go run $(GINKGO) $(GINKGO_ARGS) ./tests/customize

.PHONY: test-customize-iso
test-customize-iso: CUSTOMIZED_IMAGE := $(CUSTOMIZED_WORKDIR_PATH)/$(IMG_NAME).iso
test-customize-iso: customize-iso
	@ELMNTL_PREFIX=$(CUSTOMIZED_ISO_PREFIX) ELMNTL_MAC=$(CUSTOMIZED_IMG_MAC) scripts/run_vm.sh start "$(CUSTOMIZED_IMAGE)"
	@echo "VM started from $(CUSTOMIZED_IMAGE)"
	VM_PID=$$(scripts/run_vm.sh vmpid) go run $(GINKGO) $(GINKGO_ARGS) ./tests/customize

.PHONY: cleanup-integration-test
cleanup-integration-test:
	@rm -rf "$(CUSTOMIZED_WORKDIR_PATH)"
	rm -f  "$(CONFIG_DIR)/release.yaml"

	for prefix in "" \
		"$(CUSTOMIZED_RAW_PREFIX)" \
		"$(CUSTOMIZED_ISO_PREFIX)"; do
		
		pid="$$(ELMNTL_PREFIX="$$prefix" scripts/run_vm.sh vmpid || true)"
		if [ -n "$$pid" ]; then
			ELMNTL_PREFIX="$$prefix" scripts/run_vm.sh clean
			ELMNTL_PREFIX="$$prefix" scripts/run_vm.sh stop
		fi
	done
