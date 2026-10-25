# Used by GoReleaser: the binary is built outside and copied in.
FROM gcr.io/distroless/static-debian12:nonroot
COPY skimdit /usr/bin/skimdit
ENTRYPOINT ["/usr/bin/skimdit"]
