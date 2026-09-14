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
RUN apk add --no-cache ca-certificates wget tzdata su-exec \
    && addgroup -S -g 10001 weread \
    && adduser -S -D -H -u 10001 -G weread weread \
    && mkdir -p /data \
    && chown weread:weread /data
COPY --from=build /weread-kit /usr/local/bin/weread-kit
COPY --chmod=755 docker/entrypoint.sh /usr/local/bin/docker-entrypoint.sh
ENV WEREAD_HOST=0.0.0.0 WEREAD_DB=/data/weread.db TZ=Asia/Shanghai
EXPOSE 8080
VOLUME ["/data"]
STOPSIGNAL SIGINT
# 初始化挂载目录后立即降权，应用本身始终以非 root 用户运行。
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/local/bin/weread-kit"]
