ARG BUILDIMAGE
FROM $BUILDIMAGE AS build
ENV GOTOOLCHAIN=local
RUN dir="${XDG_CONFIG_HOME-$HOME/.config}"/go/telemetry && mkdir -p -- "$dir" && echo off >"$dir/mode"

ARG VERSION

RUN --mount=type=tmpfs,target=/var/lib/apt/lists \
  if [ ! -z "$(which apt)" ]; then \
    export DEBIAN_FRONTEND=noninteractive \
    && apt-get update \
    && apt-get install --no-install-recommends -y build-essential git \
      bash; \
  elif [ ! -z "$(which apk)" ]; then \
    apk add --no-cache build-base git \
      bash; \
  else \
    echo "unsupported package manager"; \
    exit 1; \
  fi

RUN mkdir -p $GOPATH/src/github.com/kubernetes/kubernetes
RUN git -c advice.detachedHead=false clone -b v$VERSION --depth=1 https://github.com/kubernetes/kubernetes.git $GOPATH/src/github.com/kubernetes/kubernetes
WORKDIR /go/src/github.com/kubernetes/kubernetes

COPY riscv64.patch .

ARG KUBERNETES_BINS \
  SOURCE_DATE_EPOCH \
  TARGET_OS \
  BUILD_GO_TAGS \
  BUILD_GO_CGO_ENABLED \
  BUILD_GO_CGO_CFLAGS \
  BUILD_GO_FLAGS \
  BUILD_GO_LDFLAGS
RUN \
  set -ex \
  && git apply < riscv64.patch \
  && export GOPATH=/go \
  && if [ "$TARGET_OS" = windows ]; then \
    commands="$KUBERNETES_BINS" \
    && binarySuffix=.exe \
    && export KUBE_BUILD_PLATFORMS=windows/amd64; \
  else \
    commands="$KUBERNETES_BINS" \
    && binarySuffix=''; \
  fi \
  # Ensure that all of the binaries are built with CGO \
  && if [ $BUILD_GO_CGO_ENABLED:-0 -eq 1 ]; then \
    export KUBE_CGO_OVERRIDES=$commands; \
  else \
    export KUBE_STATIC_OVERRIDES=$commands; \
  fi \
  && mkdir /out \
  && export SOURCE_DATE_EPOCH=$SOURCE_DATE_EPOCH \
  && export FORCE_HOST_GO=y \
  && export KUBE_VERBOSE=9 \
  && export KUBE_GIT_TREE_STATE=clean \
  && export KUBE_GIT_VERSION="v$VERSION+k0s" \
  && for cmd in $commands; do \
    make GOFLAGS="$BUILD_GO_FLAGS -tags=$BUILD_GO_TAGS" GOLDFLAGS="$BUILD_GO_LDFLAGS" WHAT=cmd/$cmd \
    && mv /go/src/github.com/kubernetes/kubernetes/_output/local/bin/*/*/$cmd$binarySuffix /out/; \
  done

FROM scratch
COPY --from=build out/* /bin/
