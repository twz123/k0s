ARG BUILDIMAGE
FROM $BUILDIMAGE AS build-base
WORKDIR /iptables

RUN if [ ! -z "$(which apt)" ]; then \
       apt update && apt install -y build-essential curl \
        pkg-config \
        libmnl-dev libnftnl-dev; \
    elif [ ! -z "$(which apk)" ]; then \
       apk add build-base curl pkgconf \
         linux-headers \
         libmnl-dev libmnl-static \
         libnftnl-dev; \
    else \
       echo "unsupported package manager"; \
       exit 1; \
    fi


FROM build-base AS sources
ARG VERSION
RUN --mount=type=tmpfs,target=/tmp \
  curl --proto '=https' --tlsv1.2 --retry 5 --retry-all-errors -sSLfo /tmp/src.tar.gz https://www.netfilter.org/projects/iptables/files/iptables-$VERSION.tar.xz \
  && tar xf /tmp/src.tar.gz --strip-components=1


FROM sources AS build
ARG BUILD_GO_CGO_CFLAGS BUILD_GO_LDFLAGS
ENV CFLAGS="$BUILD_GO_CGO_CFLAGS"
RUN --mount=type=tmpfs,target=/tmp \
  --network=none \
  ./configure --sysconfdir=/etc --enable-static --disable-shared --without-kernel --disable-devel && \
  make -j$(nproc) LDFLAGS="$BUILD_GO_LDFLAGS" && \
  make install

RUN strip /usr/local/sbin/xtables-legacy-multi
RUN strip /usr/local/sbin/xtables-nft-multi
RUN scanelf -Rn /usr/local && file /usr/local/sbin/*


FROM scratch
COPY --from=build /usr/local/sbin/xtables-legacy-multi /usr/local/sbin/xtables-nft-multi /bin/
