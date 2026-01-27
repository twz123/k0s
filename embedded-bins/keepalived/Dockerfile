ARG BUILDIMAGE
FROM $BUILDIMAGE AS build

RUN --mount=type=tmpfs,target=/var/lib/apt/lists \
  if [ ! -z "$(which apt)" ]; then \
    export DEBIAN_FRONTEND=noninteractive \
    && apt-get update \
    && apt-get install --no-install-recommends -y build-essential curl ca-certificates \
      libssl-dev \
      libnl-3-dev libnl-genl-3-dev; \
  elif [ ! -z "$(which apk)" ]; then \
    apk add --no-cache build-base curl \
      linux-headers \
      openssl-dev openssl-libs-static \
      libnl3-dev libnl3-static; \
  else \
    echo "unsupported package manager"; \
    exit 1; \
  fi

WORKDIR /keepalived
ARG VERSION
RUN --mount=type=tmpfs,target=/tmp \
  curl --proto '=https' --tlsv1.2 --retry 5 --retry-all-errors -sSLfo /tmp/src.tar.gz https://www.keepalived.org/software/keepalived-$VERSION.tar.gz \
  && tar xf /tmp/src.tar.gz --strip-components=1 --no-same-owner

RUN --network=none \
  CFLAGS='-static -s' LDFLAGS=-static ./configure --disable-dynamic-linking \
  && make -j$(nproc)


FROM scratch
COPY --from=build /keepalived/bin/keepalived /bin/keepalived
