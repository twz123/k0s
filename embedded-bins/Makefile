include Makefile.variables

DOCKER ?= docker

# https://reproducible-builds.org/docs/source-date-epoch/#makefile
# https://reproducible-builds.org/docs/source-date-epoch/#git
SOURCE_DATE_EPOCH ?= $(shell git log -1 --pretty=%ct || date -u +%s)

linux_images   := containerd etcd iptables keepalived kine konnectivity kubernetes runc
windows_images := containerd                                            kubernetes

linux_arches   := amd64 arm64 arm riscv64
windows_arches := amd64 arm64

PHONY: .no_default
.no_default:
	$(error no default target)

# Declare assets: containerd
containerd_linux_assets   := containerd containerd-shim containerd-shim-runc-v2
containerd_windows_assets := containerd containerd-shim-runhcs-v1
all_targets               += \
	$(foreach a,$(linux_arches),   $(foreach e,$(containerd_linux_assets),    staging/linux/$(a)/$(e)    )) \
	$(foreach a,$(windows_arches), $(foreach e,$(containerd_windows_assets),staging/windows/$(a)/$(e).exe))
$(foreach a,$(linux_arches),   $(foreach e,$(containerd_linux_assets),  $(eval   staging/linux/$(a)/$(e):       staging/linux/$(a)/.containerd.cid)))
$(foreach a,$(windows_arches), $(foreach e,$(containerd_windows_assets),$(eval staging/windows/$(a)/$(e).exe: staging/windows/$(a)/.containerd.cid)))
$(foreach a,$(linux_arches),    staging/linux/$(a)/.containerd.iid): EXTRA_BUILD_ARGS += --build-arg COMMANDS='$(containerd_linux_assets)'
$(foreach a,$(windows_arches),staging/windows/$(a)/.containerd.iid): EXTRA_BUILD_ARGS += --build-arg COMMANDS='$(containerd_windows_assets)'

# Targets to extract all declared assets from their Docker containers.
# - The cid file is expected to be the first prerequisite.
$(all_targets):
	tardir=$$(mktemp -d -- 'staging/.$(notdir $@).tmp.XXXXXX') \
	  && trap "rm -rf -- $$tardir" EXIT \
	  && $(DOCKER) export "$$(cat $<)" \
	  | tar -C "$$tardir" -x bin/$(notdir $@) \
	  && touch -c -- "$$tardir"/bin/$(notdir $@) \
	  && mv "$$tardir"/bin/$(notdir $@) $@

# All the image directories. A helper for the next targets.
all_image_dirs := \
	$(foreach a,$(linux_arches),  $(foreach i,$(linux_images),    staging/linux/$(a)/$(i))) \
	$(foreach a,$(windows_arches),$(foreach i,$(windows_images),staging/windows/$(a)/$(i)))

# The container image targets: staging/$OS/$ARCH/.$IMAGE.iid
# - Those depend on $IMAGE/**/*, i.e. `find $IMAGE -type f`
$(foreach i,$(all_image_dirs),$(eval $(dir $(i)).$(notdir $(i)).iid: $(shell find $(notdir $(i)) -type f)))
$(foreach i,$(all_image_dirs),$(dir $(i)).$(notdir $(i)).iid):
	$(eval image := $(word 1,$(subst ., ,$(notdir $@))))
	mkdir -p -- '$(dir $@)'
	$(DOCKER) build --progress=plain --iidfile '$@' \
	  --build-arg BUILDKIT_DOCKERFILE_CHECK=skip=InvalidDefaultArgInFrom \
	  --build-arg TARGET_OS=$(word 2,$(subst /, ,$@)) \
	  --build-arg TARGET_ARCH=$(word 3,$(subst /, ,$@)) \
	  --build-arg VERSION=$($(image)_version) \
	  --build-arg SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) \
	  --build-arg BUILDIMAGE=$($(image)_buildimage) \
	  --build-arg BUILD_GO_TAGS=$($(image)_build_go_tags) \
	  --build-arg BUILD_GO_CGO_ENABLED=$($(image)_build_go_cgo_enabled) \
	  --build-arg BUILD_GO_CGO_CFLAGS='$($(image)_build_go_cgo_cflags)' \
	  --build-arg BUILD_SHIM_GO_CGO_ENABLED=$($(image)_build_shim_go_cgo_enabled) \
	  --build-arg BUILD_GO_FLAGS=$($(image)_build_go_flags) \
	  --build-arg BUILD_GO_LDFLAGS=$($(image)_build_go_ldflags) \
	  --build-arg BUILD_GO_LDFLAGS_EXTRA=$($(image)_build_go_ldflags_extra) \
	  $(EXTRA_BUILD_ARGS) \
	  -- $(image)

# The container targets: staging/$OS/$ARCH/.$IMAGE.cid
# - Those depend on staging/$OS/$ARCH/.$IMAGE.iid.
$(foreach i,$(all_image_dirs),$(eval $(dir $(i)).$(notdir $(i)).cid: $(dir $(i)).$(notdir $(i)).iid))
$(foreach i,$(all_image_dirs),$(dir $(i)).$(notdir $(i)).cid):
	rm -f -- '$@'
	$(DOCKER) create --cidfile='$@' --entrypoint=/dev/null "$$(cat -- '$<')"
