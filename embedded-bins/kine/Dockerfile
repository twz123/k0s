ARG BUILDIMAGE
FROM $BUILDIMAGE AS build
ENV GOTOOLCHAIN=local
RUN dir="${XDG_CONFIG_HOME-$HOME/.config}"/go/telemetry && mkdir -p -- "$dir" && echo off >"$dir/mode"

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

WORKDIR /go/src/kine
ARG VERSION
RUN git -C .. -c advice.detachedHead=false clone -b v$VERSION --depth=1 https://github.com/rancher/kine.git
RUN go mod download

ARG BUILD_GO_TAGS \
  BUILD_GO_CGO_ENABLED \
  BUILD_GO_CGO_CFLAGS \
  BUILD_GO_FLAGS \
  BUILD_GO_LDFLAGS
RUN --network=none \
  CGO_ENABLED=$BUILD_GO_CGO_ENABLED \
    CGO_CFLAGS="$BUILD_GO_CGO_CFLAGS" go build \
    $BUILD_GO_FLAGS \
    -tags="$BUILD_GO_TAGS" \
    -ldflags="$BUILD_GO_LDFLAGS -X github.com/k3s-io/kine/pkg/version.Version=v$VERSION -X github.com/k3s-io/kine/pkg/version.GitCommit=$(git rev-parse --short HEAD)"


FROM scratch
COPY --from=build /go/src/kine/kine /bin/kine
