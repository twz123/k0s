include Makefile.variables

DOCKER ?= docker

# https://reproducible-builds.org/docs/source-date-epoch/#makefile
# https://reproducible-builds.org/docs/source-date-epoch/#git
SOURCE_DATE_EPOCH ?= $(shell git log -1 --pretty=%ct || date -u +%s)

PHONY: .no_default
.no_default:
	$(error no default target)

# Some helpers

linux_images   := containerd etcd iptables keepalived kine konnectivity kubernetes runc
windows_images := containerd                                            kubernetes

linux_arches   := amd64 arm64 arm riscv64
windows_arches := amd64 arm64

exe_path             = staging/$(1)/$(2)/$(3)$(if $(1:windows=),,.exe)
cid_dependency       = $(eval $(call exe_path,$(1),$(2),$(3)): staging/$(1)/$(2)/.$(4).cid)
container_dependency = $(eval $(call exe_path,$(1),$(2),$(4)): staging/$(1)/$(2)/.$(3).cid)
extra_build_arg      = $(eval staging/$(1)/$(2)/.$(3).iid: EXTRA_BUILD_ARGS += --build-arg $(4)='$(5)')
foreach_asset        = $(foreach os,$(2),$(foreach arch,$(3),$(foreach asset,$(4),$(call $(1),$(os),$(arch),$(asset),$(5),$(6)))))
foreach_linux        = $(call foreach_asset,$(1),linux,$(linux_arches),$(2),$(3),$(4))

# Declare assets: staging/$OS/$ARCH/containerd...
containerd_linux_assets   := containerd containerd-shim containerd-shim-runc-v2
containerd_windows_assets := containerd containerd-shim-runhcs-v1
all_targets               += \
	$(call foreach_asset,exe_path,linux,$(linux_arches),$(containerd_linux_assets)) \
	$(call foreach_asset,exe_path,windows,$(windows_arches),$(containerd_windows_assets))
$(call foreach_asset,cid_dependency,linux,$(linux_arches),$(containerd_linux_assets),containerd)
$(call foreach_asset,cid_dependency,windows,$(windows_arches),$(containerd_windows_assets),containerd)
$(call foreach_asset,extra_build_arg,linux,$(linux_arches),containerd,COMMANDS,$(containerd_linux_assets))
$(call foreach_asset,extra_build_arg,windows,$(windows_arches),containerd,COMMANDS,$(containerd_windows_assets))

# Declare assets: staging/linux/$ARCH/etcd
all_targets += $(call foreach_linux,exe_path,etcd)
$(call foreach_linux,cid_dependency,etcd,etcd)

# Declare assets: staging/linux/$ARCH/xtables-...
iptables_assets = xtables-legacy-multi xtables-nft-multi
all_targets += $(call foreach_linux,exe_path,$(iptables_assets))
$(call foreach_linux,cid_dependency,$(iptables_assets),iptables)

# Declare assets: staging/linux/$ARCH/keepalived
all_targets += $(call foreach_linux,exe_path,keepalived)
$(call foreach_linux,cid_dependency,keepalived,keepalived)

# Declare assets: staging/linux/$ARCH/kine
all_targets += $(call foreach_linux,exe_path,kine)
$(call foreach_linux,cid_dependency,keepalived,keepalived)

# Declare assets: staging/linux/$ARCH/konnectivity
all_targets += $(call foreach_linux,exe_path,konnectivity-server)
$(call foreach_linux,container_dependency,konnectivity,konnectivity-server)

# Declare assets: staging/$OS/$ARCH/kubernetes
kubernetes_linux_assets   := kubelet kube-apiserver kube-scheduler kube-controller-manager
kubernetes_windows_assets := kubelet
all_targets               += \
	$(call foreach_asset,exe_path,linux,$(linux_arches),$(kubernetes_linux_assets)) \
	$(call foreach_asset,exe_path,windows,$(windows_arches),$(kubernetes_windows_assets))
$(call foreach_asset,cid_dependency,linux,$(linux_arches),$(kubernetes_linux_assets),kubernetes)
$(call foreach_asset,cid_dependency,windows,$(windows_arches),$(kubernetes_windows_assets),kubernetes)
$(call foreach_asset,extra_build_arg,linux,$(linux_arches),kubernetes,COMMANDS,$(kubernetes_linux_assets))
$(call foreach_asset,extra_build_arg,windows,$(windows_arches),kubernetes,COMMANDS,$(kubernetes_windows_assets))

# Declare assets: staging/linux/$ARCH/runc
all_targets += $(call foreach_linux,exe_path,runc)
$(call foreach_linux,container_dependency,runc,runc)

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
