package org.skepsun.kototoro.mihonhost

import kotlinx.serialization.Serializable
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.json.Json
import org.skepsun.kototoro.core.source.MihonJarIdentity
import org.skepsun.kototoro.desktop.compat.MihonDesktopPlatform
import org.skepsun.kototoro.dex.ApkExtensionConverter
import org.skepsun.kototoro.dex.DexJarConverter
import org.skepsun.kototoro.source.host.MihonJarInspector
import org.skepsun.kototoro.source.host.SourceHostConfig
import org.skepsun.kototoro.source.host.SourceHostJar
import org.skepsun.kototoro.source.host.SourceHostJsonSession
import java.io.PrintStream
import java.net.Authenticator
import java.net.PasswordAuthentication
import java.net.URI
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.security.MessageDigest
import java.util.Locale
import java.util.zip.ZipFile

@Serializable
private data class MancoHostConfig(
    val plugins: List<MancoPlugin> = emptyList(),
    val preferenceDirectory: String = "preferences",
    val imageDirectory: String = "images",
    val proxyUrl: String = "",
    val proxyUsername: String = "",
    val proxyPassword: String = "",
)

@Serializable
private data class MancoPlugin(
    val id: String,
    val path: String,
    val version: String = "",
    val type: String = "",
)

private val hostJson = Json { ignoreUnknownKeys = true }

fun main(args: Array<String>) {
    require(args.isNotEmpty()) { "usage: serve <config.json>" }
    require(args.size == 2 && args[0] == "serve") { "usage: serve <config.json>" }
    serve(Path.of(args[1]))
}

private fun serve(configPath: Path) {
    val absoluteConfig = configPath.toAbsolutePath().normalize()
    val config = hostJson.decodeFromString<MancoHostConfig>(Files.readString(absoluteConfig))
    val normalized = config.copy(
        proxyUrl = System.getenv("MANCO_MIHON_PROXY_URL") ?: config.proxyUrl,
        proxyUsername = System.getenv("MANCO_MIHON_PROXY_USERNAME") ?: config.proxyUsername,
        proxyPassword = System.getenv("MANCO_MIHON_PROXY_PASSWORD") ?: config.proxyPassword,
    )
    configureProxy(normalized)

    val base = absoluteConfig.parent ?: Path.of(".").toAbsolutePath()
    val protocolOutput = System.out
    System.setOut(PrintStream(System.err, true, Charsets.UTF_8))

    try {
        val inspector = MihonJarInspector()
        val preferenceDirectory = resolve(base, normalized.preferenceDirectory)
        // Converted APK/DEX artifacts are derived cache, not module configuration.
        val conversionDirectory = Files.createDirectories(
            preferenceDirectory.resolveSibling("converted"),
        )
        val jars = normalized.plugins
            .filter { isMihonRuntime(it.type) }
            .map { plugin ->
                val original = resolve(base, plugin.path)
                val path = prepareExtension(original, conversionDirectory)
                val metadata = inspector.inspect(path)
                SourceHostJar(
                    path = path.toString(),
                    identity = MihonJarIdentity(
                        packageName = metadata.packageName,
                        versionCode = metadata.versionCode,
                        sha256 = metadata.sha256,
                    ),
                )
            }
        val hostConfig = SourceHostConfig(
            platformClass = MihonDesktopPlatform::class.java.name,
            jars = jars,
            imageDirectory = resolve(base, normalized.imageDirectory).toString(),
            preferenceDirectory = preferenceDirectory.toString(),
            catalogDirectory = preferenceDirectory.resolveSibling("catalog").toString(),
        )
        val platform = MihonDesktopPlatform(
            dataDirectory = preferenceDirectory.resolveSibling("compat"),
            bridgeExecutable = null,
            enableBrowserChallenges = false,
        )
        kotlinx.coroutines.runBlocking {
            SourceHostJsonSession.run(
                hostConfig,
                base,
                System.`in`.reader(Charsets.UTF_8),
                protocolOutput.writer(Charsets.UTF_8),
                platform,
            )
        }
    } finally {
        System.setOut(protocolOutput)
    }
}

/**
 * Mihon-family repositories publish Android packages or DEX-only JARs. Convert
 * both shapes to ordinary JVM class archives before the upstream inspector and
 * URLClassLoader see them; cache by source hash so container restarts do not
 * spend time converting stable extension artifacts.
 */
private fun prepareExtension(source: Path, directory: Path): Path {
    if (!ApkExtensionConverter.isApk(source) && !hasDex(source)) return source
    val hash = hash(source)
    val target = directory.resolve("$hash-v${DexJarConverter.VERSION}.jar")
    val marker = directory.resolve("$hash-v${DexJarConverter.VERSION}.ready")
    if (Files.isRegularFile(target) && Files.isRegularFile(marker)) return target

    val staging = Files.createTempFile(directory, ".convert-", ".tmp")
    try {
        val report = if (ApkExtensionConverter.isApk(source)) {
            ApkExtensionConverter.convert(source, staging).report
        } else {
            DexJarConverter.convert(source, staging)
        }
        check(report.brokenMethods.isEmpty()) {
            "Extension conversion produced broken methods: ${report.brokenMethods.joinToString(", ")}"
        }
        Files.move(staging, target, StandardCopyOption.REPLACE_EXISTING)
        Files.writeString(
            marker,
            "source=$hash\nconverter=${DexJarConverter.VERSION}\nclasses=${report.classes}\n",
            Charsets.UTF_8,
        )
        return target
    } finally {
        Files.deleteIfExists(staging)
    }
}

private fun hasDex(path: Path): Boolean = try {
    ZipFile(path.toFile()).use { zip ->
        zip.entries().asSequence().any { entry ->
            !entry.isDirectory && entry.name.matches(Regex("classes[0-9]*\\.dex"))
        }
    }
} catch (_: Exception) {
    false
}

private fun hash(path: Path): String {
    val digest = MessageDigest.getInstance("SHA-256")
    Files.newInputStream(path).use { input ->
        val buffer = ByteArray(64 * 1024)
        while (true) {
            val count = input.read(buffer)
            if (count < 0) break
            digest.update(buffer, 0, count)
        }
    }
    return digest.digest().joinToString("") { "%02x".format(it.toInt() and 0xff) }
}

private fun resolve(base: Path, value: String): Path {
    val path = Path.of(value)
    return if (path.isAbsolute) path.normalize() else base.resolve(path).normalize()
}

private fun isMihonRuntime(type: String): Boolean {
    return when (type.trim().lowercase(Locale.ROOT)) {
        "mihon", "aniyomi", "tsundoku", "kanade", "ireader", "cloudstream" -> true
        else -> false
    }
}

private fun configureProxy(config: MancoHostConfig) {
    clearProxyProperties()
    val address = config.proxyUrl.trim()
    if (address.isEmpty()) {
        Authenticator.setDefault(null)
        return
    }
    val uri = URI(address)
    val host = uri.host ?: error("Invalid proxy host")
    val scheme = uri.scheme?.lowercase(Locale.ROOT) ?: error("Invalid proxy scheme")
    val port = when {
        uri.port >= 0 -> uri.port
        scheme == "https" -> 443
        scheme.startsWith("socks") -> 1080
        else -> 80
    }
    if (scheme.startsWith("socks")) {
        System.setProperty("socksProxyHost", host)
        System.setProperty("socksProxyPort", port.toString())
    } else {
        System.setProperty("http.proxyHost", host)
        System.setProperty("http.proxyPort", port.toString())
        System.setProperty("https.proxyHost", host)
        System.setProperty("https.proxyPort", port.toString())
    }
    if (config.proxyUsername.isNotBlank()) {
        Authenticator.setDefault(object : Authenticator() {
            override fun getPasswordAuthentication(): PasswordAuthentication {
                return PasswordAuthentication(config.proxyUsername, config.proxyPassword.toCharArray())
            }
        })
    } else {
        Authenticator.setDefault(null)
    }
}

private fun clearProxyProperties() {
    listOf(
        "http.proxyHost",
        "http.proxyPort",
        "https.proxyHost",
        "https.proxyPort",
        "socksProxyHost",
        "socksProxyPort",
    ).forEach(System::clearProperty)
}
