FROM golang:1.24.6 AS builder

ARG TARGETOS

ARG TARGETARCH

WORKDIR /workspace

RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=bind,source=go.mod,target=go.mod \
    --mount=type=bind,source=go.sum,target=go.sum \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager cmd/main.go

FROM registry-cn-hangzhou.ack.aliyuncs.com/dev/debian:12-slim-update

WORKDIR /

COPY --from=builder /workspace/manager .

USER 65534:65534

ENTRYPOINT ["/manager"]
