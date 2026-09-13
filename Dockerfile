FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /wxread ./cmd/wxread

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && mkdir -p /data
COPY --from=build /wxread /usr/local/bin/wxread
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/wxread"]
