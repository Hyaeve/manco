#!/usr/bin/env sh
set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: prepare-sources.sh <kototoro-source> <output-dir>" >&2
    exit 2
fi

SOURCE_ROOT=$(CDPATH= cd -- "$1" && pwd)
OUTPUT_ROOT=$2

rm -rf "$OUTPUT_ROOT"
mkdir -p "$OUTPUT_ROOT/parser-api" \
    "$OUTPUT_ROOT/core-source" \
    "$OUTPUT_ROOT/dex-convert" \
    "$OUTPUT_ROOT/parser-host" \
    "$OUTPUT_ROOT/source-host/src"

cp -R "$SOURCE_ROOT/parser-api/src" "$OUTPUT_ROOT/parser-api/src"
cp -R "$SOURCE_ROOT/core-source/src" "$OUTPUT_ROOT/core-source/src"
cp -R "$SOURCE_ROOT/dex-convert/src" "$OUTPUT_ROOT/dex-convert/src"
cp -R "$SOURCE_ROOT/parser-host/src" "$OUTPUT_ROOT/parser-host/src"

# Surface the HTTP status and image host when a parser image request is
# rejected, instead of the bare "Image response failed" that the protocol
# collapses into RUNTIME_FAILURE. Both the page-image and cover request in
# ParserSourceRuntime use the same literal, so one replacement covers both.
PARSER_RUNTIME="$OUTPUT_ROOT/parser-host/src/main/kotlin/org/skepsun/kototoro/parserhost/ParserSourceRuntime.kt"
sed -i 's|throw IOException("Image response failed")|throw IOException("Image response failed: http=${response.code} host=${request.url.host}")|g' "$PARSER_RUNTIME"

# Keep only the Kototoro parser host. The Kotatsu/Tsuki bridges require
# additional Android-compatible runtimes and are not part of this sidecar.
rm -rf \
    "$OUTPUT_ROOT/parser-host/src/main/kotlin/org/skepsun/kototoro/parserhost/kotatsu" \
    "$OUTPUT_ROOT/parser-host/src/main/kotlin/org/skepsun/kototoro/parserhost/tsuki" \
    "$OUTPUT_ROOT/parser-host/src/main/kotlin/org/skepsun/kototoro/parserhost/ParserPluginRegistry.kt"

mkdir -p "$OUTPUT_ROOT/source-host/src/main/kotlin/org/skepsun/kototoro/source/host"
cp "$SOURCE_ROOT/source-host/src/main/kotlin/org/skepsun/kototoro/source/host/SourceImageStore.kt" \
    "$OUTPUT_ROOT/source-host/src/main/kotlin/org/skepsun/kototoro/source/host/SourceImageStore.kt"
cp "$SOURCE_ROOT/source-host/src/main/kotlin/org/skepsun/kototoro/source/host/FileSourcePreferenceStore.kt" \
    "$OUTPUT_ROOT/source-host/src/main/kotlin/org/skepsun/kototoro/source/host/FileSourcePreferenceStore.kt"
cp "$SOURCE_ROOT/source-host/src/main/kotlin/org/skepsun/kototoro/source/host/FileSourceImageStore.kt" \
    "$OUTPUT_ROOT/source-host/src/main/kotlin/org/skepsun/kototoro/source/host/FileSourceImageStore.kt"

# The upstream source set is KMP; our minimal JVM project wants only the
# Kotlin sources it can compile directly.
find "$OUTPUT_ROOT" -name '*.java' -delete
