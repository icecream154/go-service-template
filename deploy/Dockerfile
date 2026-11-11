FROM golang:1.26-alpine3.24 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/go-service ./cmd/web

FROM alpine:3.24

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=build /out/go-service /usr/local/bin/go-service
EXPOSE 8080

# Go Logger 根据挂载的 logger YAML 写文件，程序直接作为容器进程启动。
ENTRYPOINT ["/usr/local/bin/go-service"]
CMD ["-config-dir", "/app/config", "-addr", ":8080"]
