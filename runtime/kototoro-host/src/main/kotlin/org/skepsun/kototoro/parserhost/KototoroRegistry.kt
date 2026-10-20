package org.skepsun.kototoro.parserhost

import kotlinx.coroutines.ThreadContextElement
import kotlinx.coroutines.withContext
import org.skepsun.kototoro.core.source.SourceRef
import org.skepsun.kototoro.core.source.SourceUnavailableException
import org.skepsun.kototoro.parsers.ContentLoaderContext
import org.skepsun.kototoro.parsers.ContentParser
import org.skepsun.kototoro.parsers.model.ContentSource
import java.io.Closeable
import java.lang.reflect.InvocationTargetException
import java.lang.reflect.Method
import java.nio.file.Path
import kotlin.coroutines.AbstractCoroutineContextElement
import kotlin.coroutines.CoroutineContext

/** A source declared by a loaded Kototoro parser plugin. */
data class ParserSourceInfo(
    val source: SourceRef,
    val title: String,
    val pluginId: String,
    val architecture: ParserPluginArchitecture,
    val homepage: String = "",
)

data class LoadedParserPlugin(
    val id: String,
    val metadata: ParserPluginMetadata,
    val sources: List<ParserSourceInfo>,
)

class ParserHandle internal constructor(val parser: ContentParser, val info: ParserSourceInfo)

/**
 * Minimal JVM registry for the Kototoro parser ABI.
 *
 * The upstream registry also supports Kotatsu and Tsuki. This sidecar is
 * deliberately narrow: its purpose is to run Kototoro Parsers on a plain JVM
 * without pulling an Android compatibility runtime into the Manco image.
 */
class ParserPluginRegistry(
    private val platform: ParserPlatform,
    private val inspector: ParserPluginInspector = ParserPluginInspector(),
    private val hostLoader: ClassLoader = ParserPluginRegistry::class.java.classLoader,
) : Closeable {

    private class Plugin(
        val info: LoadedParserPlugin,
        val loader: ParserPluginClassLoader,
        val factory: Method,
        val constants: Map<String, Any>,
        val parsers: HashMap<String, ContentParser>,
    )

    private val plugins = linkedMapOf<String, Plugin>()
    private var resolved: Map<String, Plugin> = emptyMap()
    private var resolvedInfos: List<ParserSourceInfo> = emptyList()
    private val context = ParserLoaderContext(platform, ::siblingParser)
    private var closed = false

    @Synchronized
    fun load(path: Path, id: String, expectedSha256: String? = null): LoadedParserPlugin {
        requireOpen()
        val metadata = inspector.inspect(path)
        if (metadata.architecture != ParserPluginArchitecture.KOTOTORO) {
            throw ParserPluginException(ParserPluginFailure.UNSUPPORTED_ARCHITECTURE)
        }
        expectedSha256?.let { inspector.verify(metadata, it) }
        val loader = ParserPluginClassLoader(path.toAbsolutePath().toUri().toURL(), hostLoader)
        val plugin = try {
            withContextLoader(loader) { build(id, metadata, loader) }
        } catch (error: Throwable) {
            loader.close()
            val cause = if (error is InvocationTargetException) error.targetException else error
            if (cause is ParserPluginException) throw cause
            throw ParserPluginException(ParserPluginFailure.CONSTRUCTION_FAILED, cause)
        }
        plugins[id] = plugin
        recompute()
        return plugin.info
    }

    @Synchronized
    fun installed(): List<LoadedParserPlugin> {
        requireOpen()
        return plugins.values.map { it.info }
    }

    @Synchronized
    fun sources(): List<ParserSourceInfo> {
        requireOpen()
        return resolvedInfos
    }

    @Synchronized
    fun owns(sourceName: String): Boolean = !closed && sourceName in resolved

    suspend fun <T> withParser(sourceName: String, block: suspend (ParserHandle) -> T): T {
        val plugin = acquire(sourceName)
        return withContext(ContextLoaderElement(plugin.loader)) {
            block(ParserHandle(parserOf(plugin, sourceName), plugin.info.sources.first { it.source.name == sourceName }))
        }
    }

    private fun build(id: String, metadata: ParserPluginMetadata, loader: ParserPluginClassLoader): Plugin {
        val factoryClass = loader.loadClass(metadata.architecture.factoryClass)
        val contextClass = loader.loadClass(metadata.architecture.contextClass)
        val factory = factoryClass.declaredMethods.firstOrNull { method ->
            method.name.startsWith("newParser") &&
                method.parameterTypes.size == 2 &&
                method.parameterTypes[1] == contextClass
        } ?: throw ParserPluginException(ParserPluginFailure.UNSUPPORTED_ARCHITECTURE)
        factory.isAccessible = true
        val enumClass = factory.parameterTypes[0]
        if (!enumClass.isEnum) throw ParserPluginException(ParserPluginFailure.NO_SOURCES)
        val constants = enumClass.enumConstants.orEmpty().associateBy { (it as Enum<*>).name }
        if (constants.isEmpty()) throw ParserPluginException(ParserPluginFailure.NO_SOURCES)
        val parserInstances = HashMap<String, ContentParser>()
        val sources = constants.values.map { constant ->
            val source = constant as? ContentSource ?: throw ParserPluginException(ParserPluginFailure.NO_SOURCES)
            // A repository can declare thousands of sources. Enumeration
            // must not construct their parsers or retain their site caches.
            ParserSourceInfo(source.toSourceRef(), titleOf(constant), id, metadata.architecture)
        }
        return Plugin(LoadedParserPlugin(id, metadata, sources), loader, factory, constants, parserInstances)
    }

    private fun siblingParser(name: String): ContentParser {
        val plugin = synchronized(this) { resolved[name] ?: throw SourceUnavailableException(name) }
        return parserOf(plugin, name)
    }

    private fun parserOf(plugin: Plugin, name: String): ContentParser = synchronized(plugin.parsers) {
        plugin.parsers.getOrPut(name) {
            val constant = plugin.constants[name] ?: throw SourceUnavailableException(name)
            withContextLoader(plugin.loader) {
                try {
                    plugin.factory.invoke(null, constant, context) as ContentParser
                } catch (error: InvocationTargetException) {
                    throw error.targetException
                }
            }
        }
    }

    @Synchronized
    private fun acquire(sourceName: String): Plugin {
        requireOpen()
        return resolved[sourceName] ?: throw SourceUnavailableException(sourceName)
    }

    private fun recompute() {
        val map = linkedMapOf<String, Plugin>()
        val infos = mutableListOf<ParserSourceInfo>()
        for (plugin in plugins.values) {
            for (info in plugin.info.sources) {
                if (map.putIfAbsent(info.source.name, plugin) == null) infos += info
            }
        }
        resolved = map
        resolvedInfos = infos
    }

    @Synchronized
    override fun close() {
        if (closed) return
        closed = true
        plugins.values.forEach { it.loader.close() }
        plugins.clear()
        resolved = emptyMap()
        resolvedInfos = emptyList()
    }

    private fun requireOpen() {
        if (closed) throw ParserPluginException(ParserPluginFailure.CLOSED)
    }

    private inline fun <T> withContextLoader(loader: ClassLoader, block: () -> T): T {
        val thread = Thread.currentThread()
        val previous = thread.contextClassLoader
        thread.contextClassLoader = loader
        return try {
            block()
        } finally {
            thread.contextClassLoader = previous
        }
    }

    private class ContextLoaderElement(private val loader: ClassLoader) :
        ThreadContextElement<ClassLoader?>, AbstractCoroutineContextElement(Key) {
        companion object Key : CoroutineContext.Key<ContextLoaderElement>

        override fun updateThreadContext(context: CoroutineContext): ClassLoader? {
            val thread = Thread.currentThread()
            return thread.contextClassLoader.also { thread.contextClassLoader = loader }
        }

        override fun restoreThreadContext(context: CoroutineContext, oldState: ClassLoader?) {
            Thread.currentThread().contextClassLoader = oldState
        }
    }
}

private fun titleOf(constant: Any): String = try {
    constant.javaClass.getMethod("getTitle").invoke(constant) as? String
} catch (_: ReflectiveOperationException) {
    null
} ?: (constant as? Enum<*>)?.name?.lowercase()?.replaceFirstChar { it.uppercase() } ?: "Source"
