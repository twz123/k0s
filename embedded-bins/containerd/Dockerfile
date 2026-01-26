ARG BUILDIMAGE
FROM --platform=$BUILDPLATFORM $BUILDIMAGE AS build-base
ENV GOTOOLCHAIN=local GOTELEMETRY=off
WORKDIR /go/src/containerd

RUN if [ ! -z "$(which apt)" ]; then \
       apt update && apt install -y \
        build-essential git \
        btrfs-progs protobuf-compiler \
        bash; \
    elif [ ! -z "$(which apk)" ]; then \
       apk upgrade -U -a && apk add \
        build-base git \
        btrfs-progs-dev btrfs-progs-static \
        protoc bash; \
    else \
       echo "unsupported package manager"; \
       exit 1; \
    fi

FROM build-base AS build
ARG VERSION
RUN --mount=type=tmpfs,target=/tmp \
  git -C /go/src -c advice.detachedHead=false clone -b v$VERSION --depth=1 https://github.com/containerd/containerd.git \
  && wget -O /tmp/01.patch https://github.com/containerd/containerd/commit/0eaa09e35a3eca10094173a0b0210c90810a3674.patch \
  && git apply /tmp/01.patch \
  && go mod download -x

ARG CONTAINERD_BINS \
  TARGETARCH \
  TARGET_OS \
  BUILD_MAKEFLAGS \
  BUILD_GO_CGO_ENABLED \
  BUILD_SHIM_GO_CGO_ENABLED \
  BUILD_GO_TAGS \
  BUILD_GO_FLAGS \
  BUILD_GO_LDFLAGS

RUN echo yo

RUN MAKEFLAGS="$BUILD_MAKEFLAGS" make \
  REVISION="$(git rev-parse HEAD)" \
  COMMANDS="$CONTAINERD_BINS" \
  GOARCH=$TARGETARCH \
  GOOS=$TARGET_OS \
  CGO_ENABLED=$BUILD_GO_CGO_ENABLED \
  SHIM_CGO_ENABLED=$BUILD_SHIM_GO_CGO_ENABLED \
  BUILDTAGS+="$BUILD_GO_TAGS" \
  GO_BUILD_FLAGS="-v $BUILD_GO_FLAGS" \
  EXTRA_LDFLAGS="$BUILD_GO_LDFLAGS"

FROM scratch
COPY --from=build /go/src/containerd/bin/* /bin/
