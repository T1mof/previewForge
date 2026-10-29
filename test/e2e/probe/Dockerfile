FROM golang:1.26.9 AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY test/e2e/probe/ ./test/e2e/probe/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /netprobe ./test/e2e/probe
FROM scratch
COPY --from=builder /netprobe /netprobe
USER 65532:65532
ENTRYPOINT ["/netprobe"]
