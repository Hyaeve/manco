package org.skepsun.kototoro.parserhost

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import okhttp3.Authenticator
import okhttp3.Cookie
import okhttp3.CookieJar
import okhttp3.Credentials
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import java.io.File
import kotlinx.serialization.encodeToString
import org.mozilla.javascript.Context
import org.mozilla.javascript.ScriptableObject
import org.skepsun.kototoro.core.source.SourceEndpoint
import org.skepsun.kototoro.core.source.SourcePreferenceStore
import org.skepsun.kototoro.parsers.ContentParserCredentialsAuthProvider
import org.skepsun.kototoro.dex.DexJarConverter
import org.skepsun.kototoro.parsers.bitmap.Bitmap
import org.skepsun.kototoro.parsers.bitmap.Rect
import org.skepsun.kototoro.source.host.FileSourceImageStore
import org.skepsun.kototoro.source.host.FileSourcePreferenceStore
import java.awt.image.BufferedImage
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.PrintStream
import java.nio.file.Files
import java.nio.file.Path
import java.net.InetSocketAddress
import java.net.Proxy
import java.net.URI
import java.util.Locale
import javax.imageio.ImageIO

@Serializable
private data class HostConfig(
    val plugins: List<HostPlugin> = emptyList(),
    val preferenceDirectory: String = "preferences",
    val imageDirectory: String = "images",
    val proxyUrl: String = "",
    val proxyUsername: String = "",
    val proxyPassword: String = "",
)

@Serializable
private data class HostPlugin(
    val id: String,
    val path: String,
    val version: String = "",
    // "kototoro-parser" (default) or "mihon" for Tachiyomi-family APKs.
    val type: String = "",
)

private val hostJson = Json { ignoreUnknownKeys = true }

@Serializable
private data class HostCookie(
	val name: String,
	val value: String,
	val domain: String,
	val path: String = "/",
	val expiresAt: Long = 0L,
	val secure: Boolean = false,
	val httpOnly: Boolean = false,
	val hostOnly: Boolean = false,
)

private class CookieStore(private val file: File) {
	fun load(jar: MemoryCookieJar) {
		if (!file.isFile) return
		runCatching {
			val records = hostJson.decodeFromString<List<HostCookie>>(file.readText(Charsets.UTF_8))
			jar.restore(records)
		}.onFailure { System.err.println("kototoro-host: read cookie store: ${it.message}") }
	}

	fun save(jar: MemoryCookieJar) {
		runCatching {
			file.parentFile?.mkdirs()
			file.writeText(hostJson.encodeToString(jar.snapshot()), Charsets.UTF_8)
		}.onFailure { System.err.println("kototoro-host: write cookie store: ${it.message}") }
	}
}

fun main(args: Array<String>) {
    require(args.isNotEmpty()) { "usage: inspect <jar> | serve <config.json>" }
    when (args[0]) {
        "inspect" -> inspect(args)
        "serve" -> serve(args)
        else -> error("unknown command: ${args[0]}")
    }
}

private fun inspect(args: Array<String>) {
    require(args.size == 2)
    val metadata = ParserPluginInspector().inspect(Path.of(args[1]))
    val payload = buildJsonObject {
        put("architecture", metadata.architecture.name)
        put("sha256", metadata.sha256)
        put("classCount", metadata.classCount)
        put("maximumClassVersion", metadata.maximumClassVersion)
    }
    println(hostJson.encodeToString(JsonObject.serializer(), payload))
}

private fun serve(args: Array<String>) {
    require(args.size == 2)
    val configPath = Path.of(args[1]).toAbsolutePath().normalize()
    val config = hostJson.decodeFromString(HostConfig.serializer(), Files.readString(configPath))
	val effectiveConfig = config.copy(
		proxyUrl = System.getenv("MANCO_KOTOTORO_PROXY_URL") ?: config.proxyUrl,
		proxyUsername = System.getenv("MANCO_KOTOTORO_PROXY_USERNAME") ?: config.proxyUsername,
		proxyPassword = System.getenv("MANCO_KOTOTORO_PROXY_PASSWORD") ?: config.proxyPassword,
	)
    val base = configPath.parent ?: Path.of(".").toAbsolutePath()
    val protocolOutput = System.out
    System.setOut(PrintStream(System.err, true, Charsets.UTF_8))

    val preferenceStore = FileSourcePreferenceStore(resolve(base, config.preferenceDirectory))
    try {
		val cookieStore = CookieStore(resolve(base, config.preferenceDirectory).resolve("cookies.json").toFile())
		val cookieJar = MemoryCookieJar()
		cookieStore.load(cookieJar)
		val platform = MancoParserPlatform(preferenceStore, cookieJar, effectiveConfig)
        val registry = ParserPluginRegistry(platform)
        try {
            for (plugin in config.plugins) {
                val raw = resolve(base, plugin.path)
                if (isMihonRuntime(plugin.type)) {
                    // The APK is downloaded and tracked. Browsing it requires the pinned
                    // Mihon desktop-compatibility runtime; until that is bundled the host
                    // keeps the other plugins alive and reports the limitation per source.
                    System.err.println("kototoro-host: skipped Mihon extension ${plugin.id}: compatibility runtime unavailable")
                    continue
                }
                val jvmJar = prepareJvmJar(raw)
                registry.load(jvmJar, plugin.id)
            }
            val images = FileSourceImageStore(resolve(base, config.imageDirectory))
            val endpoint = SourceEndpoint(
                ParserSourceRuntime(registry, platform, images),
                { request, error ->
                    System.err.println(
                        "kototoro-host: failed ${request.call::class.simpleName}: ${causeChain(error)}",
                    )
                    error.printStackTrace(System.err)
                },
            )
            val reader = System.`in`.bufferedReader(Charsets.UTF_8)
            val writer = protocolOutput.bufferedWriter(Charsets.UTF_8)
            while (true) {
                val line = reader.readLine() ?: break
                if (line.isBlank()) continue
                val operation = operationOf(line)
                platform.useProxy = useProxyOf(line) ?: true
                val rawResponse = if (operation == "metadata") {
                    metadataResponse(line, registry)
                } else {
                    when (operation) {
                        "authCapabilities" -> authCapabilitiesResponse(line, registry)
                        "loginCredentials" -> loginCredentialsResponse(line, registry)
                        else -> runBlocking { endpoint.exchange(line) }
                    }
                }
                val response = if (operation == "list") trimListChapters(rawResponse) else rawResponse
                cookieStore.save(cookieJar)
                writer.write(response)
                writer.newLine()
                writer.flush()
            }
            registry.close()
        } finally {
            preferenceStore.close()
        }
    } finally {
        System.setOut(protocolOutput)
    }
}

private fun resolve(base: Path, value: String): Path {
    val path = Path.of(value)
    return if (path.isAbsolute) path.normalize() else base.resolve(path).normalize()
}

/** Renders the full cause chain so runtime failures are diagnosable from one log line. */
private fun causeChain(error: Throwable): String {
    val parts = ArrayList<String>(4)
    var current: Throwable? = error
    var depth = 0
    while (current != null && depth++ < 16) {
        val message = current.message?.trim().orEmpty()
        parts += if (message.isEmpty()) current::class.java.simpleName else "${current::class.java.simpleName}: $message"
        current = current.cause?.takeIf { it !== current }
    }
    return parts.joinToString(" <- ")
}

private fun prepareJvmJar(source: Path): Path {
    val inspector = ParserPluginInspector()
    return try {
        val metadata = inspector.inspect(source)
        if (metadata.architecture != ParserPluginArchitecture.KOTOTORO) {
            throw ParserPluginException(ParserPluginFailure.UNSUPPORTED_ARCHITECTURE)
        }
        source
    } catch (error: ParserPluginException) {
        if (error.failure != ParserPluginFailure.DEX_ONLY) throw error
        val target = source.resolveSibling(source.fileName.toString() + ".jvm.jar")
		Files.deleteIfExists(target)
		DexJarConverter.convert(source, target)
        val metadata = inspector.inspect(target)
        if (metadata.architecture != ParserPluginArchitecture.KOTOTORO) {
            throw ParserPluginException(ParserPluginFailure.UNSUPPORTED_ARCHITECTURE)
        }
        target
    }
}

private fun isMihonRuntime(type: String): Boolean {
    val value = type.trim().lowercase(Locale.ROOT)
    return value == "mihon" || value == "aniyomi" || value == "tsundoku" || value == "kanade"
}

private fun operationOf(line: String): String? = try {
    val envelope = hostJson.parseToJsonElement(line).jsonObject
    (envelope["call"] as? JsonObject)?.get("operation")?.jsonPrimitive?.content
} catch (_: Exception) {
    null
}

/**
 * List and search only need the card metadata. Some parsers include their full
 * chapter list in each Content object; dropping it here avoids serializing and
 * parsing those chapters on every discovery page request.
 */
private fun trimListChapters(response: String): String = try {
    val envelope = hostJson.parseToJsonElement(response).jsonObject
    val result = envelope["result"] as? JsonObject
    val content = result?.get("content") as? JsonArray
    if (result == null || content == null) {
        response
    } else {
        val trimmedContent = buildJsonArray {
            content.forEach { element ->
                val item = element as? JsonObject
                if (item == null) {
                    add(element)
                } else {
                    val trimmed = item.toMutableMap()
                    if (trimmed["latestChapterTitle"] !is JsonPrimitive) {
                        latestChapterTitle(item)?.let { title ->
                            trimmed["latestChapterTitle"] = JsonPrimitive(title)
                        }
                    }
                    trimmed.remove("chapters")
                    add(JsonObject(trimmed))
                }
            }
        }
        val nextResult = JsonObject(result.toMutableMap().apply { put("content", trimmedContent) })
        val nextEnvelope = JsonObject(envelope.toMutableMap().apply { put("result", nextResult) })
        hostJson.encodeToString(JsonObject.serializer(), nextEnvelope)
    }
} catch (_: Exception) {
    response
}

private fun latestChapterTitle(item: JsonObject): String? {
    val chapters = item["chapters"] as? JsonArray ?: return null
    var selectedTitle: String? = null
    var selectedNumber = 0.0
    var hasNumber = false
    chapters.forEach { value ->
        val chapter = value as? JsonObject ?: return@forEach
        val title = (chapter["title"] as? JsonPrimitive)
            ?.content
            ?.trim()
            ?.takeIf { it.isNotEmpty() }
            ?: return@forEach
        val number = (chapter["number"] as? JsonPrimitive)
            ?.content
            ?.toDoubleOrNull()
        if (!hasNumber || (number != null && number >= selectedNumber)) {
            selectedTitle = title
            if (number != null && number > 0) {
                selectedNumber = number
                hasNumber = true
            }
        }
    }
    return selectedTitle
}

/**
 * Reads the per-call proxy preference the Manco backend attaches to every
 * content-source operation. Returns null when absent so the host keeps using
 * the proxy (the historical default).
 */
private fun useProxyOf(line: String): Boolean? = try {
    val envelope = hostJson.parseToJsonElement(line).jsonObject
    val call = envelope["call"] as? JsonObject
    val value = call?.get("useProxy")?.jsonPrimitive
    when {
        value == null || value.isString -> null
        else -> value.booleanOrNull
    }
} catch (_: Exception) {
    null
}

private fun authCapabilitiesResponse(line: String, registry: ParserPluginRegistry): String {
    val envelope = hostJson.parseToJsonElement(line).jsonObject
    val requestId = (envelope["requestId"] as? JsonPrimitive)?.content ?: ""
    val call = envelope["call"]?.jsonObject
    val sourceName = call?.get("sourceName")?.jsonPrimitive?.content?.trim().orEmpty()
    if (sourceName.isEmpty()) return customError(requestId, "INVALID_ARGUMENT", "sourceName is required")
    return runCatching {
        runBlocking {
            registry.withParser(sourceName) { handle ->
                val provider = handle.parser.authorizationProvider
                val credentials = handle.parser is ContentParserCredentialsAuthProvider
                val authorized = runCatching { provider?.isAuthorized() == true }.getOrDefault(false)
                val username = if (authorized && provider != null) {
                    runCatching { provider.getUsername() }.getOrDefault("")
                } else ""
                val authUrl = runCatching { provider?.authUrl.orEmpty() }.getOrDefault("")
                    .ifBlank { runCatching { "https://${handle.parser.domain}" }.getOrDefault("") }
                buildJsonObject {
                    put("credentialsSupported", credentials)
                    put("browserSupported", authUrl.isNotBlank())
                    put("authorized", authorized)
                    put("username", username)
                    put("authUrl", authUrl)
                }
            }
        }
    }.fold(
        onSuccess = { customResponse(requestId, it) },
        onFailure = { customError(requestId, "RUNTIME_FAILURE", it.message ?: "auth capabilities failed") },
    )
}

private fun loginCredentialsResponse(line: String, registry: ParserPluginRegistry): String {
    val envelope = hostJson.parseToJsonElement(line).jsonObject
    val requestId = (envelope["requestId"] as? JsonPrimitive)?.content ?: ""
    val call = envelope["call"]?.jsonObject
    val sourceName = call?.get("sourceName")?.jsonPrimitive?.content?.trim().orEmpty()
    val username = call?.get("username")?.jsonPrimitive?.content?.trim().orEmpty()
    val password = call?.get("password")?.jsonPrimitive?.content.orEmpty()
    if (sourceName.isEmpty() || username.isEmpty() || password.isEmpty()) {
        return customError(requestId, "INVALID_ARGUMENT", "sourceName, username and password are required")
    }
    return runCatching {
        runBlocking {
            registry.withParser(sourceName) { handle ->
                val credentials = handle.parser as? ContentParserCredentialsAuthProvider
                    ?: throw IllegalStateException("该内容源不支持账号密码登录")
                val accepted = credentials.login(username, password)
                val provider = handle.parser.authorizationProvider
                val authorized = accepted && runCatching { provider?.isAuthorized() == true }.getOrDefault(accepted)
                val currentUser = if (authorized && provider != null) {
                    runCatching { provider.getUsername() }.getOrDefault(username)
                } else ""
                buildJsonObject {
                    put("accepted", accepted)
                    put("authorized", authorized)
                    put("username", currentUser)
                }
            }
        }
    }.fold(
        onSuccess = { customResponse(requestId, it) },
        onFailure = { customError(requestId, "RUNTIME_FAILURE", it.message ?: "login failed") },
    )
}

private fun customResponse(requestId: String, result: JsonObject): String = buildJsonObject {
    put("version", 1)
    put("requestId", requestId)
    put("result", result)
}.toString()

private fun customError(requestId: String, code: String, message: String): String = buildJsonObject {
    put("version", 1)
    put("requestId", requestId)
    put("error", buildJsonObject {
        put("code", code)
        put("message", message)
    })
}.toString()

private fun metadataResponse(line: String, registry: ParserPluginRegistry): String {
    val envelope = hostJson.parseToJsonElement(line).jsonObject
    val requestId = (envelope["requestId"] as? JsonPrimitive)?.content ?: ""
    val sources = buildJsonArray {
        registry.sources().forEach { source ->
            add(buildJsonObject {
                put("name", source.source.name)
                put("title", source.title)
                put("locale", source.source.locale)
                put("contentType", source.source.contentType)
                put("homepage", source.homepage)
            })
        }
    }
    return buildJsonObject {
        put("version", 1)
        put("requestId", requestId)
        put("result", buildJsonObject {
            put("operation", "metadata")
            put("sources", sources)
        })
    }.toString()
}

private class MancoParserPlatform(
    override val preferences: SourcePreferenceStore,
    private val memoryCookieJar: MemoryCookieJar,
    config: HostConfig,
) : ParserPlatform {
    private val client: OkHttpClient = OkHttpClient.Builder()
        .cookieJar(memoryCookieJar)
        .followRedirects(true)
        .followSslRedirects(true)
        .apply {
            proxyFor(config.proxyUrl)?.let(::proxy)
            if (config.proxyUsername.isNotBlank()) {
                proxyAuthenticator(Authenticator { _, response ->
                    if (response.request.header("Proxy-Authorization") != null) return@Authenticator null
                    response.request.newBuilder()
                        .header("Proxy-Authorization", Credentials.basic(config.proxyUsername, config.proxyPassword))
                        .build()
                })
            }
        }
        .build()

    // Some sources need to bypass the system proxy (for example a site that is
    // only reachable from the local network). The backend sends a per-call
    // flag; the host is single-threaded over stdin so a volatile switch is
    // enough to route that exchange and its images through the direct client.
    private val directClient: OkHttpClient = OkHttpClient.Builder()
        .cookieJar(memoryCookieJar)
        .followRedirects(true)
        .followSslRedirects(true)
        .build()

    @Volatile
    var useProxy: Boolean = true

    override val httpClient: OkHttpClient get() = if (useProxy) client else directClient
    override val cookieJar: CookieJar get() = client.cookieJar
    override val preferredLocales: List<Locale> get() = listOf(Locale.SIMPLIFIED_CHINESE, Locale.US)

    override fun createBitmap(width: Int, height: Int): Bitmap =
        AwtBitmap(BufferedImage(width, height, BufferedImage.TYPE_INT_ARGB))

    override fun redrawImageResponse(response: Response, redraw: (Bitmap) -> Bitmap): Response =
        AwtBitmap.redraw(response, redraw)

    override suspend fun evaluateJs(baseUrl: String, script: String): String? = withContext(Dispatchers.IO) {
        val context = Context.enter()
        try {
            context.optimizationLevel = -1
            val scope = context.initStandardObjects()
            ScriptableObject.putProperty(scope, "__mancoBaseUrl", baseUrl)
            Context.toString(context.evaluateString(scope, script, "source-script", 1, null))
        } finally {
            Context.exit()
        }
    }
}

private fun proxyFor(value: String): Proxy? {
    val address = value.trim()
    if (address.isEmpty()) return null
    val uri = URI(address)
    val host = uri.host ?: error("Invalid proxy host")
    val scheme = uri.scheme?.lowercase(Locale.ROOT) ?: error("Invalid proxy scheme")
    val port = when {
        uri.port >= 0 -> uri.port
        scheme == "https" -> 443
        scheme.startsWith("socks") -> 1080
        else -> 80
    }
    val type = if (scheme.startsWith("socks")) Proxy.Type.SOCKS else Proxy.Type.HTTP
    return Proxy(type, InetSocketAddress(host, port))
}

private class MemoryCookieJar : CookieJar {
    private val values = linkedMapOf<String, MutableList<Cookie>>()

    @Synchronized
    override fun saveFromResponse(url: HttpUrl, cookies: List<Cookie>) {
        for (cookie in cookies) {
            val key = cookie.domain.ifBlank { url.host }
            val current = values.getOrPut(key) { mutableListOf() }
            current.removeAll { it.name == cookie.name && it.path == cookie.path }
            current += cookie
        }
    }

    @Synchronized
    override fun loadForRequest(url: HttpUrl): List<Cookie> =
        values.values.flatten().filter { cookie -> cookie.matches(url) }

    @Synchronized
    fun snapshot(): List<HostCookie> = values.flatMap { (_, cookies) ->
        cookies.filter { it.expiresAt > System.currentTimeMillis() }.map {
            HostCookie(
                name = it.name,
                value = it.value,
                domain = it.domain,
                path = it.path,
                expiresAt = it.expiresAt,
                secure = it.secure,
                httpOnly = it.httpOnly,
                hostOnly = it.hostOnly,
            )
        }
    }

    @Synchronized
    fun restore(records: List<HostCookie>) {
        values.clear()
        val now = System.currentTimeMillis()
        for (record in records) {
            if (record.expiresAt in 1..now) continue
            val key = record.domain.ifBlank { continue }
            val cookie = Cookie.Builder()
                .name(record.name)
                .value(record.value)
                .path(record.path.ifBlank { "/" })
                .expiresAt(record.expiresAt)
                .apply {
                    if (record.hostOnly) hostOnlyDomain(key) else domain(key)
                    if (record.secure) secure()
                    if (record.httpOnly) httpOnly()
                }
                .build()
            values.getOrPut(key) { mutableListOf() } += cookie
        }
    }
}

private class AwtBitmap(private val image: BufferedImage) : Bitmap {
    override val width: Int get() = image.width
    override val height: Int get() = image.height

    override fun drawBitmap(sourceBitmap: Bitmap, src: Rect, dst: Rect) {
        val source = requireNotNull((sourceBitmap as? AwtBitmap)?.image) { "Bitmap was not created by this host" }
        val graphics = image.createGraphics()
        try {
            graphics.drawImage(source, dst.left, dst.top, dst.right, dst.bottom, src.left, src.top, src.right, src.bottom, null)
        } finally {
            graphics.dispose()
        }
    }

    companion object {
        private const val MAXIMUM_BYTES = 64L * 1024 * 1024

        fun redraw(response: Response, redraw: (Bitmap) -> Bitmap): Response {
            val bytes = response.use { source ->
                val body = source.body
                if (body.contentLength() > MAXIMUM_BYTES) throw IOException("Image exceeds configured limit")
                body.bytes().also { if (it.size > MAXIMUM_BYTES) throw IOException("Image exceeds configured limit") }
            }
            val decoded = ImageIO.read(ByteArrayInputStream(bytes)) ?: throw IOException("Unsupported image format")
            val mutable = BufferedImage(decoded.width, decoded.height, BufferedImage.TYPE_INT_ARGB).also { copy ->
                copy.createGraphics().apply { drawImage(decoded, 0, 0, null); dispose() }
            }
            val result = requireNotNull((redraw(AwtBitmap(mutable)) as? AwtBitmap)?.image) {
                "Image redraw must return a bitmap created by this host"
            }
            val output = ByteArrayOutputStream()
            check(ImageIO.write(result, "png", output)) { "PNG encoder unavailable" }
            return response.newBuilder()
                .removeHeader("Content-Length")
                .header("Content-Type", "image/png")
                .body(output.toByteArray().toResponseBody("image/png".toMediaType()))
                .build()
        }
    }
}
