# syntax=docker/dockerfile:1

# --- Stage 1: build the Vue frontend -----------------------------------------
FROM node:22-alpine AS web
WORKDIR /src/web
COPY cmd/manco/web/package.json cmd/manco/web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY cmd/manco/web/ ./
RUN npm run build

# --- Stage 2: build the Go backend (x86_64) ----------------------------------
FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=web /src/web/dist ./cmd/manco/web/dist
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w" -o /out/manco ./cmd/manco

# --- Stage 3: minimal runtime ------------------------------------------------
FROM alpine:3.22
# x86_64 target: docker-compose.yml pins platform: linux/amd64.
RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Shanghai \
    MANCO_ADDR=:15600 \
    MANCO_DATA_DIR=/app/data \
    MANCO_DOWNLOAD_DIR=/app/downloads
WORKDIR /app
COPY --from=backend /out/manco /usr/local/bin/manco
RUN mkdir -p /app/data /app/downloads
EXPOSE 15600
VOLUME ["/app/data", "/app/downloads"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:15600/ || exit 1
ENTRYPOINT ["/usr/local/bin/manco"]
