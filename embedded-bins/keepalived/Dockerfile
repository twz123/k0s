ARG BUILDIMAGE
FROM $BUILDIMAGE AS build-base
WORKDIR /keepalived

RUN if [ ! -z "$(which apt)" ]; then \
       apt update && apt install -y build-essential curl \
        openssl libssl-dev \
        libnl-3-dev libnl-3-200 libnl-genl-3-dev; \
    elif [ ! -z "$(which apk)" ]; then \
       apk add build-base curl \
        linux-headers \
        openssl-dev openssl-libs-static \
        libnl3-dev libnl3-static; \
    else \
       echo "unsupported package manager"; \
       exit 1; \
    fi


FROM build-base AS sources
ARG VERSION
RUN --mount=type=tmpfs,target=/tmp \
  curl --proto '=https' --tlsv1.2 --retry 5 --retry-all-errors -sSLfo /tmp/src.tar.gz https://www.keepalived.org/software/keepalived-$VERSION.tar.gz \
  && tar xf /tmp/src.tar.gz --strip-components=1


FROM sources AS build
ARG BUILD_GO_CGO_CFLAGS BUILD_GO_LDFLAGS
RUN --mount=type=tmpfs,target=/tmp \
  --network=none \
  CFLAGS="$BUILD_GO_CGO_CFLAGS" LDFLAGS="$BUILD_GO_LDFLAGS" ./configure --disable-dynamic-linking && \
  make -j$(nproc)


FROM scratch
COPY --from=build /keepalived/bin/keepalived /bin/keepalived
