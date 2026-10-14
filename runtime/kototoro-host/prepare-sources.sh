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

# Parser plugins may declare several mirrored domains but only the first is
# used by their config. If that mirror is unreachable from the container (TLS
# reset, DNS failure, timeout), rotate through the declared mirrors, persist
# the first working one, and retry the operation before reporting failure.
(
cd "$OUTPUT_ROOT"
# `--git-dir`/`--work-tree` force git into its plain-file mode. Without them,
# running this from inside another Git work tree makes git apply print
# "Skipped patch" and leave the file untouched, which is how an earlier build
# silently shipped a sidecar without this fix.
git --git-dir=/nonexistent --work-tree="$OUTPUT_ROOT" apply --recount - <<'PATCH'
diff --git a/parser-host/src/main/kotlin/org/skepsun/kototoro/parserhost/ParserSourceRuntime.kt b/parser-host/src/main/kotlin/org/skepsun/kototoro/parserhost/ParserSourceRuntime.kt
--- a/parser-host/src/main/kotlin/org/skepsun/kototoro/parserhost/ParserSourceRuntime.kt
+++ b/parser-host/src/main/kotlin/org/skepsun/kototoro/parserhost/ParserSourceRuntime.kt
@@ -35,8 +35,14 @@
 import org.skepsun.kototoro.parsers.model.ContentSource
 import org.skepsun.kototoro.parsers.model.SortOrder
 import org.skepsun.kototoro.source.host.SourceImageStore
+import java.io.EOFException
 import java.io.IOException
+import java.net.ConnectException
+import java.net.SocketTimeoutException
+import java.net.UnknownHostException
 import java.util.Base64
 import java.util.concurrent.ConcurrentHashMap
+import javax.net.ssl.SSLException

 /**
@@ -174,4 +174,60 @@
     private suspend fun <T> execute(sourceName: String, block: suspend (ParserHandle) -> T): T {
         if (sourceName.isEmpty()) throw SourceInvalidArgumentException()
-        return withContext(Dispatchers.IO) { registry.withParser(sourceName) { handle -> block(handle) } }
+        return withContext(Dispatchers.IO) {
+            registry.withParser(sourceName) { handle ->
+                runWithDomainFallback(sourceName, handle, block)
+            }
+        }
+    }
+
+    /**
+     * Mirrors declared by parsers are not all reachable from every host. Retry
+     * transport-level failures with the next declared domain and persist the
+     * first working value so later calls use it without probing again.
+     */
+    private suspend fun <T> runWithDomainFallback(
+        sourceName: String,
+        handle: ParserHandle,
+        block: suspend (ParserHandle) -> T,
+    ): T {
+        return try {
+            block(handle)
+        } catch (error: Throwable) {
+            if (!isTransportFailure(error)) throw error
+            val key = handle.parser.configKeyDomain
+            val candidates = key.presetValues.toList().distinct()
+            if (candidates.size <= 1) throw error
+            val config = ParserSourceConfig(platform.preferences.open(ParserLoaderContext.namespace(sourceName)))
+            var current = config[key]
+            var last = error
+            for (candidate in candidates) {
+                if (candidate == current) continue
+                if (!config.put(key, SourcePreferenceValue.Text(candidate))) throw last
+                current = candidate
+                try {
+                    return block(handle)
+                } catch (retry: Throwable) {
+                    if (!isTransportFailure(retry)) throw retry
+                    last = retry
+                }
+            }
+            throw last
+        }
+    }
+
+    private fun isTransportFailure(error: Throwable): Boolean {
+        var current: Throwable? = error
+        var depth = 0
+        while (current != null && depth++ < 16) {
+            when (current) {
+                is SSLException,
+                is SocketTimeoutException,
+                is ConnectException,
+                is UnknownHostException,
+                is EOFException
+                -> return true
+            }
+            current = current.cause?.takeIf { it !== current }
+        }
+        return false
     }
PATCH
)

# A silently skipped patch is worse than a failed build, so assert the result.
if ! grep -q 'runWithDomainFallback' "$PARSER_RUNTIME"; then
    echo "prepare-sources: domain fallback patch did not apply to $PARSER_RUNTIME" >&2
    exit 1
fi

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
