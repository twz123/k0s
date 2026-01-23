ARG BUILDIMAGE
FROM $BUILDIMAGE AS build-base
ENV GOTOOLCHAIN=local GOTELEMETRY=off
WORKDIR /go/src/runc

RUN if [ ! -z "$(which apt)" ]; then \
       apt update && apt install -y build-essential git \
        curl gperf bash pkg-config; \
    elif [ ! -z "$(which apk)" ]; then \
       apk add build-base git \
       	curl linux-headers gperf bash pkgconf; \
    else \
       echo "unsupported package manager"; \
       exit 1; \
    fi


FROM build-base AS libseccomp-sources
WORKDIR /libseccomp
ARG LIBSECCOMP_VERSION=2.6.0
RUN --mount=type=tmpfs,target=/tmp \
  curl --proto '=https' --tlsv1.2 --retry 5 --retry-all-errors -sSLfo /tmp/src.tar.gz https://github.com/seccomp/libseccomp/releases/download/v$LIBSECCOMP_VERSION/libseccomp-$LIBSECCOMP_VERSION.tar.gz \
  && tar xf /tmp/src.tar.gz --strip-components=1


FROM libseccomp-sources AS libseccomp-build
RUN --mount=type=tmpfs,target=/tmp \
  --network=none \
  ./configure --sysconfdir=/etc --enable-static --disable-shared && \
  make -j$(nproc) && \
  make -j$(nproc) check


FROM build-base AS sources
ARG VERSION
RUN git -C /go/src -c advice.detachedHead=false clone -b v$VERSION --depth=1 https://github.com/opencontainers/runc.git \
  && go mod download -x


FROM sources AS build
RUN --mount=from=libseccomp-build,source=/libseccomp,target=/libseccomp,ro \
  --mount=type=tmpfs,target=/tmp \
  --network=none \
  make -C /libseccomp install

ARG BUILD_GO_CGO_ENABLED \
  BUILD_GO_TAGS \
  BUILD_GO_FLAGS \
  BUILD_GO_LDFLAGS
RUN make \
	CGO_ENABLED="$BUILD_GO_CGO_ENABLED" \
	BUILDTAGS="$BUILD_GO_TAGS" \
	EXTRA_FLAGS="$BUILD_GO_FLAGS" \
	EXTRA_LDFLAGS="$BUILD_GO_LDFLAGS"


FROM scratch
COPY --from=build /go/src/runc/runc /bin/runc
