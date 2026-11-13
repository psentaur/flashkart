FROM golang:1.27.2 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
COPY gen gen
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /out/ ./cmd/...

FROM debian:bookworm-slim
COPY --from=build /out/ /usr/local/bin/
USER 65532:65532
