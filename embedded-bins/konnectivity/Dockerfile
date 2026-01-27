ARG BUILDIMAGE
FROM $BUILDIMAGE AS build

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

ARG VERSION
RUN git -c advice.detachedHead=false clone -b v$VERSION --depth=1 https://github.com/kubernetes-sigs/apiserver-network-proxy.git /apiserver-network-proxy
WORKDIR /apiserver-network-proxy

ARG BUILD_GO_TAGS \
  BUILD_GO_CGO_ENABLED \
  BUILD_GO_FLAGS \
  BUILD_GO_LDFLAGS
# for mockgen and protoc-gen-* versions, see:
# https://github.com/kubernetes-sigs/apiserver-network-proxy/blob/v0.34.0/README.md#build
RUN go install go.uber.org/mock/mockgen@v0.6.0 \
  && go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.27.1 \
  && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2 \
  && make gen DESIRED_MOCKGEN=v0.6.0 \
  && CGO_ENABLED=$BUILD_GO_CGO_ENABLED GOOS=linux go build \
    $BUILD_GO_FLAGS \
    -tags="$BUILD_GO_TAGS" \
    -ldflags="$BUILD_GO_LDFLAGS" \
    -o bin/proxy-server cmd/server/main.go

FROM scratch
COPY --from=build /apiserver-network-proxy/bin/proxy-server /bin/konnectivity-server
