# Manco

Manco 是一个自托管的漫画订阅下载器：从漫画源搜索、浏览、订阅作品，发现新章节后自动下载，并且**每个章节单独打包为一个 `.cbz` 文件**（不是一张张散图）。后端 Go，前端 Vue 3，全部打包进单个 Docker 镜像，容器端口 `15600`。

- 不接入 Komga，只负责订阅与下载到本地目录。
- 默认部署目录：`/vol4/1000/Backups/Develop/Manco`（NAS 上的 x86_64 Docker 环境）。

## 功能

- 首次启动进入创建账号页；登录后可在「设置 → 账号设置」中修改用户名和密码。
- 默认漫画源：**哔咔漫画（picacg）**、**禁漫天堂（jmcomic / 18comic）**、**包子漫画（baozimh）**；默认书籍源：**Z-Library（zlibrary）**。
- 书籍源：**Z-Library**，支持邮箱密码登录、检索并下载原始 EPUB / PDF / MOBI / AZW3 文件；「资源仓库」页分为「漫画源 / 书籍源 / 其他源 / 拓展仓库」四个栏目。
- 拓展仓库不内置第三方仓库；可添加多个 Kototoro、Mihon、JAR、Aniyomi、iReader、CloudStream 或 Tsundoku 兼容仓库，支持添加、编辑、同步、展开、删除、来源搜索和导入。除 JSON 清单外，还支持 gzip protobuf（Mihon / Keiyoushi `index.pb`）。内置的 Kototoro JVM 解析器运行时可以加载 Kototoro Parsers 的 DEX/JAR 插件，并执行内容源自己的搜索、浏览、章节、图片和封面接口；Mihon/Aniyomi 的 Android APK 仍只登记清单。
- 「资源仓库」按期类分为「漫画源 / 书籍源 / 其他源 / 拓展仓库」四个栏目，来源可按类型拖拽排序，顺序持久化到服务端并同步到探索发现。
- 探索发现浏览结果缓存写入 `/app/data/discover-cache.json`，默认 30 分钟内复用；访问过的作品详情与封面按内容源类型落盘到 `/app/data/discover/<kind>/<中文来源名>/<中文作品名>/`，30 天未访问自动清理。
- 「本地库」页面浏览已经配置或选择过的下载目录，可查看已下载的漫画 / 书籍、展开文件列表并下载或预览。
- 本地库支持右键海报进入多选，底部胶囊工具栏可全选、清空、繁转简、合并或删除；合并以第一个所选为母本，删除会二次确认后移除所选作品目录及其文件，执行前都会弹出项目内确认窗口。
- 「运行日志」支持原始文本与结构化列表切换，并记住上次选择；同一份日志也会输出到容器标准输出，可用 `docker logs -f Manco` 查看。
- 运行日志可按系统、账号、资源源、拓展仓库、订阅、下载、本地库、系统设置等模块筛选，并支持信息 / 警告 / 错误 / 调试级别筛选；模块与级别过滤器不持久化，只有原始 / 结构化视图模式会保留。
- 支持搜索、浏览、作品详情、章节列表、勾选章节批量下载。
- 顶部搜索栏常驻（Ctrl/⌘+K 聚焦），可跨所有已启用来源检索并保留最近三次搜索；发现页分为「漫画源 / 书籍源 / 其他源」三个标签，源标签单行横滑，各源筛选条件与视图模式（网格/列表）按浏览器持久化。
- 网格视图按七行铺满并支持虚拟加载，列表视图每页 30 行；支持输入页码快速跳转，进入作品详情再返回会恢复到原来的源、页码与滚动位置。
- 漫画源可单独从发现页隐藏，隐藏状态保存在服务端，不影响继续维护账号和 Cookie。
- 网络代理可在「设置」中配置，用于访问被地域或网络策略拦截的漫画源。
- 订阅追更：首次检查只记录当前最新章节作为基线，之后按订阅卡片中的 Cron 表达式周期性检查；新订阅默认使用创建时的星期与整点，每周执行一次。
- 下载队列：成功与失败任务都会持久保存；已成功下载的章节再次下载时直接跳过，失败任务可手动重试，也可删除记录。
- 下载时会同时保存作品封面与 `ComicInfo.xml` 元数据（标题、作者、简介、标签、作品 ID、来源、状态等），并把同一份 `ComicInfo.xml` 作为条目写入每个章节 `.cbz`，遵循 comicinfo 规范。
- 每个章节的图片先下载到临时目录，写出 `001.jpg`、`002.jpg`…… 后压缩为 `章节目录.cbz.part`，完成后重命名为最终 `.cbz`，不会留下半成品文件。
- 禁漫天堂的图片按 18comic 的算法自动解扰（MD5 分段还原）。
- 内置图片代理，带域名白名单 + 内网地址拦截，避免浏览器直连图床时的 Referer 限制和 SSRF 风险。
- 「系统设置」分为账号与安全（账号 / 密码 / 会话存活期）、代理（地址、用户名、密码）与订阅归档（默认 30 天，输入即自动保存）三个窗口，底部为「关于 Manco」；各来源的下载线程、每 N 话暂停与暂停分钟改到资源仓库的来源编辑窗口里单独设置。
- 包子漫画封面统一通过图片 CDN `s1.bzcdn.net` 访问（原 `static-tw.baozimh.com` 常被 Cloudflare 拦截），章节目录从站点结构化容器解析并按章节槽位排序。
- 下载失败会按 10 分钟、30 分钟自动重试两次，仍失败则保留失败记录，可在下载页多选（支持 Shift 多选）后手动重试。
- 订阅长期未更新（超过所设天数）会先关闭，关闭 15 天后归档；已完结的订阅 15 天后归档且不再轮询；手动重新启用会重新计算检查周期。
- 前端 UI 参考 Aether / MoviePilot / kmreader 的后台与阅读器风格：浅黄绿色侧栏、细灰色滚动条、来源原生圆角图标、卡片式订阅和响应式发现网格。
- 订阅卡片支持章节标符开关（文件名前缀 `#1`）、封面模糊、检查更新与全量下载；已启用章节标符后可回填已有下载文件名。

## 目录结构

```
Manco/
├── cmd/manco/            # Go 入口 + 内嵌前端资源
│   └── web/              # Vue 3 + Vite 前端
├── internal/
│   ├── api/              # HTTP API、登录会话、图片代理、SPA 托管
│   ├── config/           # 环境变量配置
│   ├── downloader/       # 下载引擎、CBZ 打包、解扰
│   ├── scheduler/        # 订阅轮询
│   ├── secret/           # 凭据加密
│   ├── source/           # 三个漫画源的适配实现
│   └── store/            # SQLite 持久化
├── Dockerfile
├── .github/workflows/docker.yml   # CI：测试 + 构建并推送 GHCR 镜像
└── docker-compose.yml
```

## 快速开始（Docker）

镜像是 `ghcr.io/hyaeve/manco:latest`（由 GitHub Actions 构建并推送，`linux/amd64`）。

### 方式一：拉取 GHCR 镜像（NAS 推荐）

在部署目录建一个 `docker-compose.yml`：

```yaml
services:
  manco:
    image: ghcr.io/hyaeve/manco:latest
    container_name: Manco
    network_mode: bridge
    ports:
      - 15600:15600
    volumes:
      - ./config:/app/config
      - ./data:/app/data
      # 自定义下载目录示例；不需要时删除，下载位置在订阅任务里选择容器内目录。
      - /vol3/1000/家庭影音/:/家庭影音:shared
    environment:
      - TZ=Asia/Shanghai
    restart: unless-stopped
```

```bash
cd /vol4/1000/Backups/Develop/Manco
docker compose pull
docker compose up -d
docker compose logs -f
```

启动后访问 `http://<NAS-IP>:15600`，首次进入会要求创建登录账号；之后可在「系统设置 → 账号与安全」中更新账号、密码与会话存活期。

> 首次拉取如果提示 `denied` 或未授权，说明 GHCR 包还是私有可见性：把 GitHub 仓库的
> **Packages → manco → Package settings → Change visibility** 改成 Public，或在 NAS 上先
> `echo <PAT> | docker login ghcr.io -u <GitHub用户名> --password-stdin`（PAT 需要 `read:packages`）。

### 方式二：从源码本地构建

仓库里的 `docker-compose.yml` 使用 GHCR 镜像；如需本地构建，可临时把 `image:` 换成以下 build 段：

```bash
cd /vol4/1000/Backups/Develop/Manco

docker build -t ghcr.io/hyaeve/manco:latest .
docker compose up -d
```

镜像固定使用 `platform: linux/amd64`，在 x86_64 NAS 上原生运行；在 ARM 设备上会通过模拟层运行。

数据卷：

| 容器路径 | 宿主机路径 | 说明 |
| --- | --- | --- |
| `/app/data` | `./data` | SQLite 数据库、会话、探索发现缓存、图标索引与加密密钥 |
| `/app/config` | `./config` | 系统设置、来源与账号、拓展仓库、订阅等模块化 JSON 配置 |

| 自定义下载目录 | 按需映射 | 在作品详情页的下载/订阅任务设置中选择实际挂载目录，如 `/家庭影音/Manga` |

`/app/config` 按模块分别保存为 `settings.json`、`accounts.json`、`sources.json`、`repositories.json`、`subscriptions.json`。系统登录密码以 bcrypt 哈希保存，来源账号的密码、Token 与 Cookie 在写入前使用 `MANCO_SECRET` 派生密钥加密。

> **从旧版升级**：旧版本把配置写在数据目录里（如 `<data>/config` 或 `/data/config`，密钥为 `data/.secret`）。首次启动新版本时，Manco 会自动把这些文件补齐到 `MANCO_CONFIG_DIR`（默认 `/app/config`），并使用 `secret.key` 作为密钥文件名；已存在的文件不会被覆盖。建议升级前先整体备份旧的 `data` 目录。

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MANCO_ADDR` | `:15600` | 监听地址 |
| `MANCO_SECRET` | 自动生成 `config/secret.key` | 凭据加密密钥，建议显式设置并备份 |
| `MANCO_DATA_DIR` | `/app/data`（镜像默认，compose 中为 `/app/data`） | 运行数据目录，包含 SQLite、探索缓存和图标索引 |
| `MANCO_CONFIG_DIR` | `/app/config`（镜像默认，compose 中为 `/app/config`） | 用户配置目录，保存设置、来源、仓库与订阅配置 |
| `MANCO_DOWNLOAD_DIR` | `downloads` | 仅作旧任务兼容回退，不参与下载位置目录选择 |
| `MANCO_SOURCE_REPO` | 空 | 可选的源清单地址；默认不内置第三方仓库 |
| `MANCO_SCAN_INTERVAL` | `30m` | 无 Cron 数据的旧订阅兼容扫描间隔 |
| `MANCO_MAX_CHAPTER_CONCURRENCY` | `2` | 同时下载的章节数 |
| `MANCO_MAX_PAGE_CONCURRENCY` | `4` | 单章节内同时下载的图片数 |
| `MANCO_COOKIE_SECURE` | `false` | 反向代理启用 HTTPS 后设为 `true` |

## 漫画源配置

登录后在「资源仓库」页面配置，页面分为「漫画源」「书籍源」「其他源」「拓展仓库」四个栏目。包子、哔咔、禁漫、Z-Library 等默认源不可删除或改名；拓展仓库不再默认内置，用户可自行添加多个 Kototoro / Mihon / JAR / Aniyomi 等兼容仓库，同步后导入可执行的配置来源。Kototoro Parsers 的 `plugin.jar` 会安装到 `/app/config/plugins`，由内置 JVM sidecar 加载；插件返回的图片字节由插件自身完成解扰、签名和请求头处理，Manco 不再对插件图片做二次解扰：

如果所在网络无法直连这些站点，先在「设置 → 网络代理」填入可用的 HTTP/HTTPS 代理，然后保存。

**哔咔漫画（picacg）** — 需要账号密码。填写账号与密码后点击「登录并保存」，后端调用 `POST /auth/sign-in` 获取 Token 并加密保存；之后的搜索、章节、图片请求都会自动带签名头。

**禁漫天堂（jmcomic）** — 免登录即可浏览，但部分线路需要 Cookie 才能看到完整章节。可在「站点域名」里填写可用域名（如 `https://18comic.vip`），并把浏览器中的 Cookie 粘贴到 Cookie 输入框。

**包子漫画（baozimh）** — 免费站点，遇到 Cloudflare 校验时把浏览器 Cookie 粘贴进来即可；同样支持自定义域名（镜像站）。

禁漫天堂与包子漫画都已内置多个备用镜像站（镜像列表参考 Kototoro 拓展仓库）。请求某个站点失败时会自动按顺序切换下一个镜像；在「站点域名」里填写的自定义域名会作为最高优先级。


**书籍源** — **Z-Library（zlibrary）** 使用邮箱密码登录，登录后可搜索热门、最近和指定语言/格式的电子书；章节代表整本电子书，下载时保存服务返回的原始 EPUB、PDF、MOBI 或 AZW3 文件，不再转成纯文本。书籍与漫画分开显示在「资源仓库 → 书籍源」栏目下。
默认源使用 Go 原生实现；同时，Manco 也能加载 [Kototoro 拓展仓库](https://raw.githubusercontent.com/skepsun/kototoro-parsers/repo/index.min.json) 中的 Kototoro Parsers 插件，把返回的页面和封面字节直接写入本地缓存 / CBZ。

Kototoro Parsers 插件的使用方式：

1. 在「资源仓库 → 拓展仓库」添加 `https://raw.githubusercontent.com/skepsun/kototoro-parsers/repo/index.min.json`，类型可选 `json`，保存后同步。
2. 在「漫画源 / 书籍源 / 其他源」栏目点击「添加源」，选择该仓库中的 `Kototoro Parsers` 并安装 `plugin.jar`。Manco 会把插件安装到 `/app/config/plugins`，按插件声明的 `contentType` 自动归类：漫画、成人漫画、韩漫、美漫等进入漫画源；小说、成人小说等进入书籍源；其余进入其他源。
3. 来源卡片上的「浏览器验证」会调用插件返回站点验证地址。需要 Cloudflare、Gatekeeper 等验证时，先在浏览器完成验证，再把该站点 Cookie 逐行粘贴回窗口导入；Cookie 会保存在 `/app/data/kototoro-host/preferences/cookies.json`，解析器后续请求会复用该会话。
4. 插件内容源的浏览、搜索、详情、章节和图片均由内置 JVM sidecar 执行。封面和章节图片不会走普通 HTTP 代理下载，而是由插件自身完成签名、请求头与解扰后再写入发现缓存或 CBZ。

## 下载与文件命名

- 输出结构：`downloads/<作品名>/<章节名>.cbz`，同目录额外保存 `cover.<图片后缀>` 与 `ComicInfo.xml`。
- 每个 `.cbz` 里包含按顺序命名的图片 `001.jpg`、`002.png`……，以及一个 `ComicInfo.xml` 条目。
- `ComicInfo.xml` 保存作品标题、作者、简介、标签、来源、作品 ID、状态等，遵循 ComicInfo 规范，供漫画阅读器直接读取（本项目本身不接入 Komga）。
- 名称会过滤 `\ / : * ? " < > |` 等字符，并限制长度，避免 NAS 上的文件系统报错。
- 图片扩展名按文件头判断，WebP/PNG 原样写入，JPEG 保持原格式；解扰后的禁漫图片统一转为 JPEG/PNG。
- 书籍输出：`downloads/<书名>/<文件名>.<epub|pdf|mobi|azw3>`，保留 Z-Library 返回的原始文件和扩展名，并在作品目录写入 `book.json` 与封面。

## 订阅逻辑

1. 在作品详情页点击「订阅追更」，先在订阅任务设置窗口确认 Cron、下载位置与繁转简选项；当前最新章节会被记录为基线。
2. 新订阅默认生成每周 Cron：星期和整点取自创建订阅的时间；可在订阅卡片三点菜单的「编辑」里修改。
3. 发现比基线更新的章节时，自动创建下载任务，逐话打包 CBZ。
4. 在「订阅清单」页可以通过卡片右下角三点菜单编辑、启用/禁用、归档或删除订阅。

## API 概览

所有 `/api/*` 接口除登录外都需要登录会话。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/api/auth/login` | 登录 |
| `GET` | `/api/auth/setup` | 查询是否需要首次创建账号 |
| `POST` | `/api/auth/register` | 首次创建账号（仅空库可用） |
| `POST` | `/api/auth/logout` | 退出 |
| `GET` | `/api/auth/me` | 当前用户 |
| `GET` | `/api/sources` | 漫画源列表与账号状态 |
| `PATCH` | `/api/sources/{id}` | 设置漫画源是否在发现页隐藏 |
| `PUT` | `/api/sources/{id}/account` | 保存账号 / Cookie / 域名 |
| `DELETE` | `/api/sources/{id}/account` | 清除凭据 |
| `GET` | `/api/sources/{id}/browser-session` | 获取 Kototoro 插件的浏览器验证地址与 User-Agent |
| `POST` | `/api/sources/{id}/cookies` | 导入 Kototoro 插件运行时 Cookie |
| `GET` | `/api/sources/{id}/search?q=&page=` | 搜索 |
| `GET` | `/api/sources/{id}/filters` | 读取来源自身声明的筛选条件 |
| `GET` | `/api/sources/{id}/browse?kind=&page=` | 浏览 |
| `GET` | `/api/sources/{id}/comics/{comicId}` | 作品详情 + 章节 |
| `GET` | `/api/source-repo` | 读取 Kototoro 拓展仓库清单 |
| `GET/POST/PUT/DELETE` | `/api/custom-sources` … | 自定义源管理与配置 |
| `GET/POST/PATCH/DELETE` | `/api/subscriptions` … | 订阅管理 |
| `GET/POST/DELETE` | `/api/downloads` … | 下载任务 |
| `GET` | `/api/library` | 本地 CBZ 资料库 |
| `GET` | `/api/local` | 本地库（扫描已配置的下载目录） |
| `GET` | `/api/local/file?path=` | 读取 / 下载本地库中的单个文件 |
| `POST` | `/api/local/merge` | 以第一个所选作品为母本，合并后续作品文件 |
| `POST` | `/api/local/simplify` | 将所选作品目录与文件名繁转简 |
| `POST` | `/api/local/delete` | 二次确认后删除所选本地作品目录及文件 |
| `GET/PUT` | `/api/settings` | 设置 |
| `GET` | `/api/stats` | 统计 |
| `GET` | `/api/logs?limit=` | 最近运行日志 |
| `GET` | `/api/activity` | 顶部活动通知 |
| `GET` | `/api/download-directories` | 可选的容器内下载目录 |
| `GET` | `/api/proxy/image?url=&sourceId=` | 图片代理 |

## CI 与镜像发布

工作流文件：`.github/workflows/docker.yml`。触发条件与行为：

| 事件 | 行为 |
| --- | --- |
| push 到 `main` / `master` | 先 `go vet` + `go test`，再构建 `linux/amd64` 镜像并推送 `:latest` 与 `:sha-<短哈希>` |
| 推送 `v*` 标签（如 `v1.2.0`） | 同样测试并推送 `:v1.2.0` 与 `:sha-<短哈希>` |
| 向默认分支提 PR | 只构建验证，不推送镜像 |
| 手动 `workflow_dispatch` | 与默认分支 push 一致 |

镜像地址固定为 `ghcr.io/hyaeve/manco`，构建使用 Buildx + GitHub Actions 层缓存，并生成 OCI 标签、provenance 与 SBOM。
首次推送成功后，到仓库 **Packages** 面板把 `manco` 的可见性改为 Public（或在 NAS 上登录 GHCR）才能匿名拉取。

发新版本：

```bash
git tag v0.1.21 && git push origin v0.1.21   # 构建并发布 :v0.1.21
docker compose pull && docker compose up -d  # NAS 上升级到最新镜像
```

## 本地开发

```bash
# 后端（默认 :15600）
go run ./cmd/manco

# 前端（Vite dev server，自动代理 /api 到 15600）
cd cmd/manco/web
npm install
npm run dev
```

构建生产版本：

```bash
cd cmd/manco/web && npm run build      # 产物写入 cmd/manco/web/dist
go build ./...                          # dist 通过 go:embed 打进二进制
go test ./...
```

> 修改前端后必须重新执行 `npm run build`，否则 `go build` 嵌入的仍是旧的 `dist`。

> 工作区里的 `AGENTS.md`（AI 变更日志：时间 / 代码位置 / 功能）已被 `.gitignore` 排除，只保留在本地，不会随仓库上传。

## 说明与免责声明

- 本项目只做本地缓存式下载，请自行确认所在地区对相关站点的访问与使用规定，仅用于个人备份。
- 漫画源接口随时可能变化；若某个源失效，可在「漫画源」页更新域名或 Cookie，或在 `internal/source/<源>` 下调整解析逻辑。
- 包子漫画使用 HTML 解析 + Cookie，遇到更强的 Cloudflare 校验时需要在浏览器中重新获取 Cookie。
