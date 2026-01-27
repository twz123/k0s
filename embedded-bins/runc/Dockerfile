ARG BUILDIMAGE
FROM $BUILDIMAGE AS build-base
ENV GOTOOLCHAIN=local
RUN dir="${XDG_CONFIG_HOME-$HOME/.config}"/go/telemetry && mkdir -p -- "$dir" && echo off >"$dir/mode"

RUN --mount=type=tmpfs,target=/var/lib/apt/lists \
  if [ ! -z "$(which apt)" ]; then \
    export DEBIAN_FRONTEND=noninteractive \
    && apt-get update \
    && apt-get install --no-install-recommends -y build-essential git \
      curl ca-certificates \
      pkg-config \
      gperf bash; \
  elif [ ! -z "$(which apk)" ]; then \
    apk add --no-cache build-base git curl \
      pkgconf \
      linux-headers gperf bash; \
  else \
    echo "unsupported package manager"; \
    exit 1; \
  fi


FROM build-base AS libseccomp-build
WORKDIR /libseccomp
ARG LIBSECCOMP_VERSION=2.6.0
RUN --mount=type=tmpfs,target=/tmp \
  curl --proto '=https' --tlsv1.2 --retry 5 --retry-all-errors -sSLfo /tmp/src.tar.gz https://github.com/seccomp/libseccomp/releases/download/v$LIBSECCOMP_VERSION/libseccomp-$LIBSECCOMP_VERSION.tar.gz \
  && tar xf /tmp/src.tar.gz --strip-components=1 --no-same-owner

RUN --network=none \
  ./configure --sysconfdir=/etc --enable-static --disable-shared \
  && make -j$(nproc) \
  && make -j$(nproc) check


FROM build-base AS build
WORKDIR /go/src/runc
ARG VERSION
RUN git -C .. -c advice.detachedHead=false clone -b v$VERSION --depth=1 https://github.com/opencontainers/runc.git
RUN go mod download

RUN --mount=from=libseccomp-build,source=/libseccomp,target=/libseccomp,ro \
  --network=none \
  make -C /libseccomp install

ARG BUILD_GO_TAGS \
  BUILD_GO_CGO_ENABLED \
  BUILD_GO_FLAGS \
  BUILD_GO_LDFLAGS
RUN --network=none \
  make \
    CGO_ENABLED=$BUILD_GO_CGO_ENABLED \
    BUILDTAGS="$BUILD_GO_TAGS" \
    EXTRA_FLAGS="$BUILD_GO_FLAGS" \
    EXTRA_LDFLAGS="$BUILD_GO_LDFLAGS"


FROM scratch
COPY --from=build /go/src/runc/runc /bin/runc
