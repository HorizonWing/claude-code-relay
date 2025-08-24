# 前端构建阶段
FROM node:18.18 AS frontend-builder

WORKDIR /app/web

# 安装pnpm
RUN npm install -g pnpm

# 复制前端项目文件
COPY web/package.json ./

# 安装依赖（跳过prepare脚本避免husky问题）
RUN pnpm install --ignore-scripts

# 复制前端源码
COPY web/ ./

# 构建前端项目
RUN pnpm run build

# 后端构建阶段
FROM golang:1.21 AS backend-builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags '-w -s' -o claude-code-relay main.go

# 最终运行阶段
FROM ubuntu:22.04

# 更新系统并安装必要的包
RUN apt-get update && \
    apt-get install -y \
    ca-certificates \
    tzdata \
    curl \
    wget \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# 设置时区
ENV TZ=Asia/Shanghai
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

WORKDIR /app

# 创建非root用户
RUN useradd -r -s /bin/false appuser

# 创建必要的目录并设置权限
RUN mkdir -p /app/logs && \
    chown -R appuser:appuser /app && \
    chmod -R 755 /app/logs

# 复制后端可执行文件
COPY --from=backend-builder --chown=appuser:appuser /app/claude-code-relay .

# 复制前端构建产物
COPY --from=frontend-builder --chown=appuser:appuser /app/web/dist ./web/dist

COPY --chown=appuser:appuser .env.example .env.example

# 设置可执行权限
RUN chmod +x ./claude-code-relay

# 切换到非root用户
USER appuser

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD curl --fail http://localhost:8080/health || exit 1

CMD ["./claude-code-relay"]