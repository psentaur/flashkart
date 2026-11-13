FROM alpine:3.22
RUN apk add --no-cache socat
USER 65532:65532
ENTRYPOINT ["socat"]
