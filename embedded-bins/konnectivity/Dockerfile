ARG BUILDIMAGE
FROM --platform=$BUILDPLATFORM $BUILDIMAGE AS build-base
ENV GOTOOLCHAIN=local GOTELEMETRY=off
WORKDIR /go/src/konnectivity

RUN if [ ! -z "$(which apt)" ]; then \
       apt update && apt install -y build-essential make protobuf-compiler git; \
    elif [ ! -z "$(which apk)" ]; then \
       apk add git make protoc; \
    else \
       echo "unsupported package manager"; \
       exit 1; \
    fi

FROM build-base AS build
ARG VERSION
RUN --mount=type=tmpfs,target=/tmp \
  git -C /go/src -c advice.detachedHead=false clone -b v$VERSION --depth=1 https://github.com/kubernetes-sigs/apiserver-network-proxy.git konnectivity \
  && go mod download -x

# for mockgen and protoc-gen-* versions, see:
# https://github.com/kubernetes-sigs/apiserver-network-proxy/blob/v0.34.0/README.md#build
RUN --mount=type=tmpfs,target=/tmp \
  --mount=type=tmpfs,target=/go/bin \
  --mount=type=cache,id=konnectivity-go-cache-$BUILDPLATFORM,target=/root/.cache/go-build \
  go install go.uber.org/mock/mockgen@v0.6.0 && \
  go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.27.1 && \
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2 && \
  make gen DESIRED_MOCKGEN=v0.6.0

ARG TARGETARCH \
  BUILD_GO_CGO_ENABLED \
  BUILD_GO_FLAGS \
  BUILD_GO_TAGS \
  BUILD_GO_LDFLAGS
RUN --mount=type=tmpfs,target=/tmp \
  --mount=type=cache,id=konnectivity-go-cache-$TARGETARCH,target=/root/.cache/go-build \
  --network=none \
  CGO_ENABLED=$BUILD_GO_CGO_ENABLED \
  GOARCH=$TARGETARCH \
  go build \
    $BUILD_GO_FLAGS \
    -tags="$BUILD_GO_TAGS" \
    -ldflags="$BUILD_GO_LDFLAGS" \
    ./cmd/server

FROM scratch
COPY --from=build /go/src/konnectivity/server /bin/konnectivity-server
