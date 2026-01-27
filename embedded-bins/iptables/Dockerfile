ARG BUILDIMAGE
FROM $BUILDIMAGE AS build

RUN --mount=type=tmpfs,target=/var/lib/apt/lists \
  if [ ! -z "$(which apt)" ]; then \
    export DEBIAN_FRONTEND=noninteractive \
    && apt-get update \
    && apt-get install --no-install-recommends -y build-essential curl ca-certificates \
      pkg-config \
      libmnl-dev libnftnl-dev \
      file pax-utils; \
  elif [ ! -z "$(which apk)" ]; then \
    apk add --no-cache build-base curl \
      pkgconf \
      libmnl-dev libmnl-static \
      libnftnl-dev; \
  else \
    echo "unsupported package manager"; \
    exit 1; \
  fi

WORKDIR /iptables
ARG VERSION
RUN --mount=type=tmpfs,target=/tmp \
  curl --proto '=https' --tlsv1.2 --retry 5 --retry-all-errors -sSLfo /tmp/src.tar.xz https://www.netfilter.org/projects/iptables/files/iptables-$VERSION.tar.xz \
  && tar xf /tmp/src.tar.xz --strip-components=1 --no-same-owner

ARG TARGET_OS
# -D__UAPI_DEF_ETHHDR is required to build on musl.
# If this CFLAG isn't defined, itpables will define it in include/xtables.h
# and will have a conflict with musl, which defines it in
# /usr/include/netinet/if_ether.h
RUN --network=none \
  if [ ! -z "$(which apk)" ]; then \
      CFLAGS="-Os -D__UAPI_DEF_ETHHDR=0" ./configure --sysconfdir=/etc --enable-static --disable-shared --without-kernel --disable-devel; \
    else \
      CFLAGS="-Os" ./configure --sysconfdir=/etc --enable-static --disable-shared --without-kernel --disable-devel; \
    fi

RUN make -j$(nproc) LDFLAGS=-all-static
RUN make -j$(nproc) install

RUN strip /usr/local/sbin/xtables-legacy-multi
RUN strip /usr/local/sbin/xtables-nft-multi
RUN scanelf -Rn /usr/local && file /usr/local/sbin/*


FROM scratch
COPY --from=build /usr/local/sbin/xtables-*-multi /bin/
