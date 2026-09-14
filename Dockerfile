# builder 固定跑在构建机原生平台($BUILDPLATFORM),用 Go 交叉编译产出
# TARGETOS/TARGETARCH 产物,避免 CI 上 QEMU 模拟 arm64 整场编译。
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /weread-kit ./cmd/weread-kit

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && mkdir -p /data
COPY --from=build /weread-kit /usr/local/bin/weread-kit
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/weread-kit"]
