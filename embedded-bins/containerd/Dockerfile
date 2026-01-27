ARG BUILDIMAGE
FROM $BUILDIMAGE AS build
ENV GOTOOLCHAIN=local
RUN dir="${XDG_CONFIG_HOME-$HOME/.config}"/go/telemetry && mkdir -p -- "$dir" && echo off >"$dir/mode"

RUN --mount=type=tmpfs,target=/var/lib/apt/lists \
  if [ ! -z "$(which apt)" ]; then \
    export DEBIAN_FRONTEND=noninteractive \
    && apt-get update \
    && apt-get install --no-install-recommends -y build-essential git \
      bash \
      btrfs-progs; \
  elif [ ! -z "$(which apk)" ]; then \
    apk add --no-cache build-base git \
      bash \
      btrfs-progs-dev btrfs-progs-static; \
  else \
    echo "unsupported package manager"; \
    exit 1; \
  fi

WORKDIR /go/src/containerd
ARG VERSION
RUN git -C .. -c advice.detachedHead=false clone -b v$VERSION --depth=1 https://github.com/containerd/containerd.git
RUN --mount=type=tmpfs,target=/tmp \
  wget -O /tmp/01.patch https://github.com/containerd/containerd/commit/0eaa09e35a3eca10094173a0b0210c90810a3674.patch \
  && git apply /tmp/01.patch
RUN go mod download

ARG CONTAINERD_BINS \
  TARGET_OS \
  BUILD_GO_TAGS \
  BUILD_GO_CGO_ENABLED \
  BUILD_SHIM_GO_CGO_ENABLED \
  BUILD_GO_FLAGS \
  BUILD_GO_LDFLAGS
RUN make \
    COMMANDS="$CONTAINERD_BINS" \
    GOOS="$TARGET_OS" \
    CGO_ENABLED=$BUILD_GO_CGO_ENABLED \
    SHIM_CGO_ENABLED=$BUILD_SHIM_GO_CGO_ENABLED \
    GO_TAGS="-tags=$BUILD_GO_TAGS" \
    GO_BUILD_FLAGS="$BUILD_GO_FLAGS" \
    EXTRA_LDFLAGS="$BUILD_GO_LDFLAGS"


FROM scratch
COPY --from=build /go/src/containerd/bin/* /bin/
