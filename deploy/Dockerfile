# server 形态镜像（docs/16 §11）。构建上下文必须是仓库根：
#
#   docker build -f deploy/Dockerfile -t zrcin-server .
#
# BUILD_TAGS 与 cmd/zrcin-gate/main.go:160-177 的判定一致：
#   - 上下文里有 .build/harness-src 或 cyber-harness 检出 => 加 inprochost
#   - 上下文里有 rem/ 检出                              => 加 remtunnel
# 默认（无 harness/rem）不带 tags，镜像内进程外 Hub（--harness 指向镜像内
# 路径或宿主路径）也能跑。要打 inprochost/remtunnel 镜像，把对应检出的源码
# 拷进上下文（go.mod 的 replace 指向 ../cyber-harness 与 ../rem，注意目录名）。
FROM golang:1.26-alpine AS build
ARG BUILD_TAGS=""
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -tags "$BUILD_TAGS" -o /out/zrcin ./cmd/zrcin

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/zrcin /usr/local/bin/zrcin
# 数据目录（projects、jwt.key、host 库等）与 PG 数据在 compose 里是两个独立卷。
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/zrcin"]
CMD ["--data", "/data", "--addr", "0.0.0.0:8080"]
