FROM golang:1.26.9 AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /manager ./cmd/manager
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /admission ./cmd/admission

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /manager /manager
COPY --from=builder /admission /admission
USER 65532:65532
ENTRYPOINT ["/manager"]
