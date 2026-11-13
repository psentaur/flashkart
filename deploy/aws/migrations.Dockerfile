FROM alpine:3.22
RUN apk add --no-cache aws-cli jq postgresql17-client
