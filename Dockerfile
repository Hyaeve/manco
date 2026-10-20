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

# --- Stage 4: build the pinned Suwayomi AndroidCompatibility runtime ----------
FROM gradle:9.3.1-jdk25 AS mihon-runtime
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /build
RUN git init suwayomi \
    && git -C suwayomi -c core.autocrlf=false fetch --depth=1 \
        https://github.com/Suwayomi/Suwayomi-Server.git \
        eb2dc0b19a9571b27c02bebc5c883e404b7bd7fb \
    && git -C suwayomi checkout --quiet --detach FETCH_HEAD
COPY runtime/mihon-host/suwayomi-ios-runtime.patch /tmp/suwayomi-ios-runtime.patch
COPY runtime/mihon-host/copy-suwayomi-runtime.init.gradle /tmp/copy-suwayomi-runtime.init.gradle
RUN git -C suwayomi apply /tmp/suwayomi-ios-runtime.patch \
    && sed -i 's|Instant.now().epochSecond.toString()|"1790898154"|' suwayomi/server/build.gradle.kts \
    && test "$(grep -c '1790898154' suwayomi/server/build.gradle.kts)" -eq 1
ENV ProductBuildType=Preview
RUN ./suwayomi/gradlew -p suwayomi :server:copyTachiazRuntime --no-daemon \
        --max-workers=2 --console=plain \
        "-Dorg.gradle.jvmargs=-Xmx4g" "-Pkotlin.daemon.jvmargs=-Xmx4g" \
        --init-script /tmp/copy-suwayomi-runtime.init.gradle \
        -PtachiazRuntimeOutput=/out/compatibility

# --- Stage 5: build the separate Mihon-family JVM host ------------------------
FROM gradle:9.7.0-jdk21 AS mihon
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /build
RUN git clone --filter=blob:none https://github.com/Kototoro-app/Kototoro.git upstream \
    && git -C upstream checkout 68c6b12013f08f9778d9892c5230746289e88018
COPY runtime/mihon-host/ /build/host/
COPY --from=mihon-runtime /out/compatibility /build/compatibility
RUN sh /build/host/prepare-upstream.sh /build/upstream /build/host
# The runtime producer emits a broad dependency set; the compatibility module
# verifies an exact pinned subset. Keep only that subset in the final host.
RUN rm -f /build/upstream/gradle/gradle-daemon-jvm.properties \
    && mkdir -p /build/compatibility-pinned \
    && awk -F= '/^[^#]/ { print $1 }' \
        /build/upstream/mihon-desktop-compat/windows-runtime-pins.properties \
        | while IFS= read -r jar; do \
            test -f "/build/compatibility/$jar" \
                || { echo "Missing pinned Mihon runtime JAR: $jar" >&2; exit 1; }; \
            cp "/build/compatibility/$jar" /build/compatibility-pinned/; \
        done
WORKDIR /build/upstream
RUN gradle --no-daemon --configure-on-demand \
        -PwithMihonDesktopCompat=true \
        -PmihonCompatibilityDirectory=/build/compatibility-pinned \
        -PmihonCompatibilityJavaVersion=21 \
        --init-script /build/host/skip-runtime-pin-verification.init.gradle \
        :manco-mihon-host:installDist

# --- Stage 6: minimal runtime -------------------------------------------------
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata openjdk21-jre-headless ffmpeg
ENV TZ=Asia/Shanghai \
    MANCO_ADDR=:15600 \
    MANCO_DATA_DIR=/app/data \
    MANCO_CONFIG_DIR=/app/config \
    GOGC=75 \
    GOMEMLIMIT=768MiB \
    MANCO_KOTOTORO_HOST_OPTS="-Xms32m -Xmx384m -XX:MaxMetaspaceSize=192m -XX:ReservedCodeCacheSize=64m -XX:ActiveProcessorCount=2 -XX:+UseSerialGC" \
    MANCO_MIHON_HOST_OPTS="-Xms48m -Xmx512m -XX:MaxMetaspaceSize=256m -XX:ReservedCodeCacheSize=96m -XX:ActiveProcessorCount=2 -XX:+UseSerialGC" \
    JAVA_TOOL_OPTIONS=-Djava.awt.headless=true
WORKDIR /app
COPY --from=backend /out/manco /usr/local/bin/manco
COPY --from=kototoro /build/host/build/install/manco-kototoro-host /usr/local/lib/manco/kototoro-host
COPY --from=mihon /build/upstream/manco-mihon-host/build/install/manco-mihon-host /usr/local/lib/manco/mihon-host
COPY --from=kototoro /build/upstream/LICENSE /usr/share/licenses/manco/kototoro/LICENSE
COPY --from=mihon /build/upstream/LICENSE /usr/share/licenses/manco/kototoro/LICENSE.mihon
COPY --from=mihon /build/upstream/mihon-desktop-compat/README.md /usr/share/licenses/manco/kototoro/MIHON-COMPAT-README.md
RUN mkdir -p /app/data /app/config
EXPOSE 15600
VOLUME ["/app/data", "/app/config"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:15600/ || exit 1
ENTRYPOINT ["/usr/local/bin/manco"]
