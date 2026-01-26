ARG BUILDIMAGE
FROM --platform=$BUILDPLATFORM $BUILDIMAGE AS build-base
ENV GOTOOLCHAIN=local GOTELEMETRY=off
WORKDIR /go/src/kubernetes

RUN if [ ! -z "$(which apt)" ]; then \
       apt update && apt install -y build-essential git bash; \
    elif [ ! -z "$(which apk)" ]; then \
       apk add --no-cache git make bash; \
    else \
       echo "unsupported package manager"; \
       exit 1; \
    fi


FROM build-base AS build
ARG VERSION
RUN --mount=source=riscv64.patch,target=/run/patches/riscv64.patch,ro \
  git -C /go/src -c advice.detachedHead=false clone -b v$VERSION --depth=1 https://github.com/kubernetes/kubernetes.git && \
  git apply </run/patches/riscv64.patch
RUN go mod download -x

ARG TARGET_OS \
  TARGETARCH \
  SOURCE_DATE_EPOCH \
  BUILD_GO_TAGS \
  BUILD_GO_CGO_ENABLED \
  BUILD_GO_FLAGS \
  BUILD_GO_LDFLAGS \
  KUBERNETES_BINS
RUN --mount=type=tmpfs,target=/tmp \
  --mount=type=cache,id=kubernetes-go-cache-$TARGETARCH,target=/root/.cache/go-build \
  --network=none \
  set -e && \
  export KUBE_BUILD_PLATFORMS=$TARGET_OS/$TARGETARCH && \
  # Ensure that all of the binaries are built with CGO \
  if [ "$BUILD_GO_CGO_ENABLED" == 1 ]; then \
    export KUBE_CGO_OVERRIDES="$KUBERNETES_BINS"; \
  else \
    export KUBE_STATIC_OVERRIDES="$KUBERNETES_BINS"; \
  fi && \
  mkdir /out && \
  export SOURCE_DATE_EPOCH=$SOURCE_DATE_EPOCH && \
  export FORCE_HOST_GO=y && \
  export KUBE_VERBOSE=9 && \
  export KUBE_GIT_TREE_STATE=clean && \
  export KUBE_GIT_VERSION=v$VERSION+k0s && \
  for cmd in $KUBERNETES_BINS; do \
    make GOFLAGS="$BUILD_GO_FLAGS -tags=$BUILD_GO_TAGS" GOLDFLAGS="$BUILD_GO_LDFLAGS" WHAT=cmd/$cmd && \
    mv _output/local/bin/*/*/$cmd$(GOOS=$TARGET_OS go env GOEXE) /out/; \
  done

FROM --platform=$BUILDPLATFORM scratch
COPY --from=build out/* /bin/
