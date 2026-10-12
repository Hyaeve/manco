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

# --- Stage 3: build the Kototoro JVM parser host -----------------------------
FROM gradle:9.3.1-jdk21 AS kototoro
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /build
RUN git clone --filter=blob:none https://github.com/skepsun/kototoro.git upstream \
    && git -C upstream checkout 31f98eb8182fb39790d2cf89e6d121cd4e798d6d
COPY runtime/kototoro-host/ /build/host/
RUN sh /build/host/prepare-sources.sh /build/upstream /build/host/upstream
WORKDIR /build/host
RUN gradle --no-daemon installDist

# --- Stage 4: minimal runtime ------------------------------------------------
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata openjdk21-jre-headless
ENV TZ=Asia/Shanghai \
    MANCO_ADDR=:15600 \
    MANCO_DATA_DIR=/app/data \
    MANCO_CONFIG_DIR=/app/config \
    MANCO_DOWNLOAD_DIR=/app/downloads \
    JAVA_TOOL_OPTIONS=-Djava.awt.headless=true
WORKDIR /app
COPY --from=backend /out/manco /usr/local/bin/manco
COPY --from=kototoro /build/host/build/install/manco-kototoro-host /usr/local/lib/manco/kototoro-host
COPY --from=kototoro /build/upstream/LICENSE /usr/share/licenses/manco/kototoro/LICENSE
RUN mkdir -p /app/data /app/config /app/downloads
EXPOSE 15600
VOLUME ["/app/data", "/app/config", "/app/downloads"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:15600/ || exit 1
ENTRYPOINT ["/usr/local/bin/manco"]
