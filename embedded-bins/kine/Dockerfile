ARG BUILDIMAGE
FROM $BUILDIMAGE AS build

RUN --mount=type=tmpfs,target=/var/lib/apt/lists \
  if [ ! -z "$(which apt)" ]; then \
    export DEBIAN_FRONTEND=noninteractive \
    && apt-get update \
    && apt-get install --no-install-recommends -y build-essential git; \
  elif [ ! -z "$(which apk)" ]; then \
    apk add --no-cache build-base git; \
  else \
    echo "unsupported package manager"; \
    exit 1; \
  fi

ARG VERSION
RUN cd / && git -c advice.detachedHead=false clone -b v$VERSION --depth=1 https://github.com/rancher/kine.git
WORKDIR /kine

ARG BUILD_GO_TAGS \
  BUILD_GO_CGO_ENABLED \
  BUILD_GO_CGO_CFLAGS \
  BUILD_GO_FLAGS \
  BUILD_GO_LDFLAGS
RUN CGO_ENABLED=$BUILD_GO_CGO_ENABLED \
    CGO_CFLAGS="$BUILD_GO_CGO_CFLAGS" go build \
    $BUILD_GO_FLAGS \
    -tags="$BUILD_GO_TAGS" \
    -ldflags="$BUILD_GO_LDFLAGS -X github.com/k3s-io/kine/pkg/version.Version=v$VERSION -X github.com/k3s-io/kine/pkg/version.GitCommit=$(git rev-parse --short HEAD)" \
    -o kine

FROM scratch
COPY --from=build /kine/kine /bin/kine
