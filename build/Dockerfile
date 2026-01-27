ARG BUILDIMAGE
FROM $BUILDIMAGE
ENV GOTOOLCHAIN=local
RUN dir="${XDG_CONFIG_HOME-$HOME/.config}"/go/telemetry && mkdir -p -- "$dir" && echo off >"$dir/mode"

ARG TARGETARCH
RUN --mount=type=tmpfs,target=/var/lib/apt/lists \
  set -ex; \
  if [ ! -z "$(which apt)" ]; then \
    export DEBIAN_FRONTEND=noninteractive \
    && apt-get update \
    && apt-get install --no-install-recommends -y make gcc; \
  elif [ ! -z "$(which apk)" ]; then \
# Need to use the gold linker on ARM, Go really wants to have it.
# https://github.com/golang/go/blob/go1.25.6/src/cmd/link/internal/ld/lib.go#L1678-L1697
     case "$TARGETARCH" in \
     arm*) binutils=binutils-gold ;; \
        *) binutils=binutils ;; \
     esac \
     && apk add --no-cache make gcc musl-dev "$binutils"; \
  else \
     echo "unsupported package manager"; \
     exit 1; \
  fi

ENV \
  HOME="/run/k0s-build" \
  PATH="/run/k0s-build/go/bin:$PATH" \
  GOBIN="/run/k0s-build/go/bin" \
  GOCACHE="/run/k0s-build/go/build" \
  GOMODCACHE="/run/k0s-build/go/mod"
