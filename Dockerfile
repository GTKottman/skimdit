# Used by GoReleaser: the binary is built outside and copied in.
# git is needed by `skimdit serve` to commit published posts.
FROM alpine:3.22
RUN apk add --no-cache git ca-certificates tzdata openssh-client \
 && adduser -D -u 10001 skimdit
COPY skimdit /usr/bin/skimdit
USER skimdit
WORKDIR /site
EXPOSE 8080
ENTRYPOINT ["/usr/bin/skimdit"]
CMD ["serve", "--site", "/site"]
