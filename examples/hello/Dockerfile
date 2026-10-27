FROM golang:1.26.9 AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
COPY examples/hello/ examples/hello/
ARG REVISION=development
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.revision=${REVISION}" -o /server ./examples/hello

FROM scratch
COPY --from=builder /server /server
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/server"]
