ARG BUILDIMAGE
FROM $BUILDIMAGE AS build-base
ENV GOTOOLCHAIN=local
RUN dir="${XDG_CONFIG_HOME-$HOME/.config}"/go/telemetry && mkdir -p -- "$dir" && echo off >"$dir/mode"


FROM build-base AS go-tools
# for mockgen and protoc-gen-* versions, see:
# https://github.com/kubernetes-sigs/apiserver-network-proxy/blob/v0.34.0/README.md#build
RUN --mount=type=cache,id=konnectivity-gomodcache,target=/go/pkg/mod \
  --mount=type=cache,id=konnectivity-gocache,target=/root/.cache/go-build \
  export GOBIN=/run/go-tools/bin \
  && go install go.uber.org/mock/mockgen@v0.6.0 \
  && go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.27.1 \
  && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2


FROM build-base AS build
RUN --mount=type=tmpfs,target=/var/lib/apt/lists \
  if [ ! -z "$(which apt)" ]; then \
    export DEBIAN_FRONTEND=noninteractive \
    && apt-get update \
    && apt-get install --no-install-recommends -y build-essential git \
      protobuf-compiler; \
  elif [ ! -z "$(which apk)" ]; then \
    apk add --no-cache build-base git \
      protoc; \
  else \
    echo "unsupported package manager"; \
    exit 1; \
  fi

WORKDIR /go/src/konnectivity
ARG VERSION
RUN git -C .. -c advice.detachedHead=false clone -b v$VERSION --depth=1 https://github.com/kubernetes-sigs/apiserver-network-proxy.git konnectivity
RUN --mount=type=cache,id=konnectivity-gomodcache,target=/go/pkg/mod \
  go mod download
RUN --mount=type=cache,id=konnectivity-gomodcache,target=/go/pkg/mod,ro \
  --mount=from=go-tools,source=/run/go-tools/bin,target=/run/go-tools/bin,ro \
  --network=none \
  export PATH="$PATH:/run/go-tools/bin" \
  && make gen-proto mock_gen DESIRED_MOCKGEN=v0.6.0

ARG BUILD_GO_TAGS \
  BUILD_GO_CGO_ENABLED \
  BUILD_GO_FLAGS \
  BUILD_GO_LDFLAGS
RUN --mount=type=cache,id=konnectivity-gomodcache,target=/go/pkg/mod \
  --mount=type=cache,id=konnectivity-gocache,target=/root/.cache/go-build \
  --network=none \
  CGO_ENABLED=$BUILD_GO_CGO_ENABLED GOOS=linux go build -C cmd/server \
    $BUILD_GO_FLAGS \
    -tags="$BUILD_GO_TAGS" \
    -ldflags="$BUILD_GO_LDFLAGS"


FROM scratch
COPY --from=build /go/src/konnectivity/cmd/server/server /bin/konnectivity-server
