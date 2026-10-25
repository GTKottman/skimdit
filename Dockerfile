# The Skimdit server image. GoReleaser builds the binary and copies it in;
# deploy/Dockerfile.source builds the same image from source.
FROM alpine:3.22
ARG TARGETARCH
ARG LITESTREAM_VERSION=0.5.17
RUN apk add --no-cache git ca-certificates tzdata openssh-client \
 && case "$TARGETARCH" in \
      amd64) arch=x86_64; sum=cfb371176d164437ae869f8351cfde49bd1804ae71c61923f75c9cba9c9c006d ;; \
      arm64) arch=arm64;  sum=f8ca4a050095c1efbda2c4365172e61bf9d955ea0d9ac42f448b52e51819baa5 ;; \
      *) echo "unsupported arch $TARGETARCH" >&2; exit 1 ;; \
    esac \
 && wget -q -O /tmp/ls.tgz "https://github.com/benbjohnson/litestream/releases/download/v${LITESTREAM_VERSION}/litestream-${LITESTREAM_VERSION}-linux-${arch}.tar.gz" \
 && echo "$sum  /tmp/ls.tgz" | sha256sum -c - \
 && tar -xzf /tmp/ls.tgz -C /usr/local/bin litestream \
 && rm /tmp/ls.tgz \
 && adduser -D -u 10001 skimdit \
 && mkdir -p /data /etc/skimdit && chown skimdit /data
COPY deploy/known_hosts /etc/skimdit/known_hosts
COPY deploy/entrypoint.sh /usr/local/bin/skimdit-entrypoint
COPY skimdit /usr/local/bin/skimdit
USER skimdit
ENV HOME=/home/skimdit
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/skimdit-entrypoint"]
