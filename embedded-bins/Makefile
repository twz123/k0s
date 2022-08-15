include Makefile.variables

.PHONY: all
ifeq ($(TARGET_OS),windows)
all: $(addprefix staging/windows/bin/,$(embedded_windows_bins))
else
all: $(addprefix staging/linux/bin/,$(embedded_linux_bins))
endif

define build_bin =
staging/$(1)/bin/$(2): .container.$(patsubst %/,%,$(dir $(2))).$(1)
	mkdir -p $$(@D)
	docker export $$(shell cat -- $$<) | tar -C $$(@D) -xv --strip-components 1 bin/$$(@F)
	touch -c -- $$@
endef
$(foreach bin,$(embedded_linux_bins),$(eval $(call build_bin,linux,$(bin))))
$(foreach bin,$(embedded_windows_bins),$(eval $(call build_bin,windows,$(bin))))

arg_if_nonempty = $(if $($(1)_build_$(2)),--build-arg BUILD_$(3)=$($(1)_build_$(2)),)
define build_container =
.docker-image.$(2).$(1).stamp: $(2)/Dockerfile
	docker build -t k0sbuild.docker-image.$(2).$(1) \
	  --build-arg TARGET_OS=$(1) \
	  --build-arg VERSION=$$($(2)_version) \
	  --build-arg BUILDIMAGE=$$(or $$($(2)_buildimage),golang:$$(go_version)-alpine$$(alpine_image_version)) \
	  $$(call arg_if_nonempty,$(2),go_tags,GO_TAGS) \
	  $$(call arg_if_nonempty,$(2),go_cgo_enabled,GO_CGO_ENABLED) \
	  $$(call arg_if_nonempty,$(2),go_cgo_cflags,GO_CGO_CFLAGS) \
	  $$(call arg_if_nonempty,$(2),shim_go_cgo_enabled,SHIM_GO_CGO_ENABLED) \
	  $$(call arg_if_nonempty,$(2),go_flags,GO_FLAGS) \
	  $$(call arg_if_nonempty,$(2),go_ldflags,GO_LDFLAGS) \
	  $$(call arg_if_nonempty,$(2),go_ldflags_extra,GO_LDFLAGS_EXTRA) \
	  - <$(2)/Dockerfile
	trap 'echo Interrupted' INT && \
	  docker images -q k0sbuild.docker-image.$(2).$(1):latest > $$@.tmp || { \
	    code=$$$$?; rm -f $$@.tmp; exit $$$$code; \
	  }
	mv $$@.tmp $$@

.container.$(2).$(1): .docker-image.$(2).$(1).stamp
	trap 'echo Interrupted' INT && \
	  docker create --entrypoint=/dev/null $$(shell cat -- $$<) > $$@.tmp || { \
	    code=$$$$?; rm -f $$@.tmp; exit $$$$code; \
	  }
	mv $$@.tmp $$@
endef
images_for = $(sort $(foreach bin,$(1),$(patsubst %/,%,$(dir $(bin)))))
$(foreach image,$(call images_for,$(embedded_linux_bins)),$(eval $(call build_container,linux,$(image))))
$(foreach image,$(call images_for,$(embedded_windows_bins)),$(eval $(call build_container,windows,$(image))))

.PHONY: clean
clean:
	for i in .container.*; do \
		if [ -f $$i ]; then \
			docker rm $$(cat $$i) && rm $$i; \
		fi; \
	done
	for i in .docker-image.*; do \
		if [ -f $$i ]; then \
			docker rmi -f k0sbuild$$(basename $$i .stamp) && rm $$i;\
		fi; \
	done
	rm -rf staging .tmp/* *.tmp
