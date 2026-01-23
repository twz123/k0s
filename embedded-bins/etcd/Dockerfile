ARG BUILDIMAGE
FROM --platform=$BUILDPLATFORM $BUILDIMAGE AS build-base
ENV GOTOOLCHAIN=local GOTELEMETRY=off
WORKDIR /go/src/etcd

RUN if [ ! -z "$(which apt)" ]; then \
       apt update && apt install -y build-essential git; \
    elif [ ! -z "$(which apk)" ]; then \
       apk add --no-cache build-base git; \
    else \
       echo "unsupported package manager"; \
       exit 1; \
    fi

FROM build-base AS build
ARG VERSION
RUN --mount=type=tmpfs,target=/tmp \
  git -C /go/src -c advice.detachedHead=false clone -b v$VERSION --depth=1 https://github.com/etcd-io/etcd.git \
  && go mod download -x

ARG TARGETARCH \
  BUILD_GO_TAGS \
  BUILD_GO_CGO_ENABLED \
  BUILD_GO_FLAGS \
  BUILD_GO_LDFLAGS
ENV GOARCH=$TARGETARCH CGO_ENABLED=$BUILD_GO_CGO_ENABLED
RUN --mount=type=tmpfs,target=/tmp \
  --network=none \
  gitRev=$(git rev-parse --short HEAD) && \
  echo Git revision: $gitRev && \
  go build -C server -tags="$BUILD_GO_TAGS" $BUILD_GO_FLAGS \
    -ldflags="$BUILD_GO_LDFLAGS -X=go.etcd.io/etcd/api/v3/version.GitSHA=$gitRev" \
    -v -mod=readonly

FROM scratch
COPY --from=build /go/src/etcd/server/server /bin/etcd
