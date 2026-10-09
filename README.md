# Manco

Manco 是一个自托管的漫画订阅下载器：从漫画源搜索、浏览、订阅作品，发现新章节后自动下载，并且**每个章节单独打包为一个 `.cbz` 文件**（不是一张张散图）。后端 Go，前端 Vue 3，全部打包进单个 Docker 镜像，容器端口 `15600`。

- 不接入 Komga，只负责订阅与下载到本地目录。
- 默认部署目录：`/vol4/1000/Backups/Develop/Manco`（NAS 上的 x86_64 Docker 环境）。

## 功能

- 首次启动进入创建账号页；登录后可在「设置 → 账号设置」中修改用户名和密码。
- 漫画源：**哔咔漫画（picacg）**、**禁漫天堂（jmcomic / 18comic）**、**包子漫画（baozimh）**。
- 「系统日志」支持原始文本与结构化列表切换，并记住上次选择；同一份日志也会输出到容器标准输出，可用 `docker logs -f Manco` 查看。
- 支持搜索、浏览、作品详情、章节列表、勾选章节批量下载。
- 发现页的三个漫画源标签与搜索框位于同一行，各源筛选条件会按浏览器持久化。
- 漫画源可单独从发现页隐藏，隐藏状态保存在服务端，不影响继续维护账号和 Cookie。
- 网络代理可在「设置」中配置，用于访问被地域或网络策略拦截的漫画源。
- 订阅追更：首次检查只记录当前最新章节作为基线，之后按订阅卡片中的 Cron 表达式周期性检查；新订阅默认使用创建时的星期与整点，每周执行一次。
- 下载队列：成功与失败任务都会持久保存；已成功下载的章节再次下载时直接跳过，失败任务可手动重试，也可删除记录。
- 下载时会同时保存作品封面与 `ComicInfo.xml` 元数据（标题、作者、简介、标签、作品 ID、来源、状态等），并把同一份 `ComicInfo.xml` 作为条目写入每个章节 `.cbz`，遵循 comicinfo 规范。
- 每个章节的图片先下载到临时目录，写出 `001.jpg`、`002.jpg`…… 后压缩为 `章节目录.cbz.part`，完成后重命名为最终 `.cbz`，不会留下半成品文件。
- 禁漫天堂的图片按 18comic 的算法自动解扰（MD5 分段还原）。
- 内置图片代理，带域名白名单 + 内网地址拦截，避免浏览器直连图床时的 Referer 限制和 SSRF 风险。
- 「系统设置」分为账号与安全（用户名 / 密码 / 会话存活期）、代理（地址与可选凭据）、下载设置（三源独立线程数、批量下载数量与间隔、繁体转简体、未更新关闭天数）。
- 下载失败会按 10 分钟、30 分钟自动重试两次，仍失败则保留失败记录，可在下载页多选（支持 Shift 多选）后手动重试。
- 订阅长期未更新（超过所设天数）会先关闭，关闭 15 天后归档；已完结的订阅 15 天后归档且不再轮询；手动重新启用会重新计算检查周期。
- 前端 UI 参考 BangumiKomga 的浅色后台风格：左侧导航 + 卡片式列表。

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
    container_name: manco
    platform: linux/amd64
    restart: unless-stopped
    ports:
      - "15600:15600"
    environment:
      TZ: "Asia/Shanghai"
    volumes:
      - ./data:/app/data
      - ./downloads:/app/downloads
```

```bash
cd /vol4/1000/Backups/Develop/Manco
docker compose pull
docker compose up -d
docker compose logs -f
```

启动后访问 `http://<NAS-IP>:15600`，首次进入会要求创建登录账号；之后可在「设置 → 修改密码」中更新密码。

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
| `/app/data` | `./data` | SQLite 数据库、会话、加密密钥 `.secret` |
| `/app/downloads` | `./downloads` | 下载的 CBZ 文件 |

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MANCO_ADDR` | `:15600` | 监听地址 |
| `MANCO_SECRET` | 自动生成 `data/.secret` | 凭据加密密钥，建议显式设置并备份 |
| `MANCO_DATA_DIR` | `data` | 数据目录 |
| `MANCO_DOWNLOAD_DIR` | `downloads` | 下载目录 |
| `MANCO_SOURCE_REPO` | Kototoro 拓展仓库 | 源清单参考地址 |
| `MANCO_SCAN_INTERVAL` | `30m` | 无 Cron 数据的旧订阅兼容扫描间隔 |
| `MANCO_MAX_CHAPTER_CONCURRENCY` | `2` | 同时下载的章节数 |
| `MANCO_MAX_PAGE_CONCURRENCY` | `4` | 单章节内同时下载的图片数 |
| `MANCO_COOKIE_SECURE` | `false` | 反向代理启用 HTTPS 后设为 `true` |

## 漫画源配置

登录后在「漫画源」页面配置：

如果所在网络无法直连这些站点，先在「设置 → 网络代理」填入可用的 HTTP/HTTPS 代理，然后保存。

**哔咔漫画（picacg）** — 需要账号密码。填写账号与密码后点击「登录并保存」，后端调用 `POST /auth/sign-in` 获取 Token 并加密保存；之后的搜索、章节、图片请求都会自动带签名头。

**禁漫天堂（jmcomic）** — 免登录即可浏览，但部分线路需要 Cookie 才能看到完整章节。可在「站点域名」里填写可用域名（如 `https://18comic.vip`），并把浏览器中的 Cookie 粘贴到 Cookie 输入框。

**包子漫画（baozimh）** — 免费站点，遇到 Cloudflare 校验时把浏览器 Cookie 粘贴进来即可；同样支持自定义域名（镜像站）。

禁漫天堂与包子漫画都已内置多个备用镜像站（镜像列表参考 Kototoro 拓展仓库）。请求某个站点失败时会自动按顺序切换下一个镜像；在「站点域名」里填写的自定义域名会作为最高优先级。

三个源都参考 [Kototoro 拓展仓库](https://raw.githubusercontent.com/skepsun/kototoro-parsers/repo/index.min.json) 中同名解析器的访问方式，但 Manco 使用 Go 原生实现，不加载 Android 插件包。

## 下载与文件命名

- 输出结构：`downloads/<作品名>/<章节名>.cbz`，同目录额外保存 `cover.<图片后缀>` 与 `comic.nfo`。
- 每个 `.cbz` 里是按顺序命名的图片：`001.jpg`、`002.png`……
- 输出结构：`downloads/<作品名>/<章节名>.cbz`，同目录额外保存 `cover.<图片后缀>` 与 `ComicInfo.xml`。
- 每个 `.cbz` 里包含按顺序命名的图片 `001.jpg`、`002.png`……，以及一个 `ComicInfo.xml` 条目。
- `ComicInfo.xml` 保存作品标题、作者、简介、标签、来源、作品 ID、状态等，遵循 ComicInfo 规范，供漫画阅读器直接读取（本项目本身不接入 Komga）。
- 名称会过滤 `\ / : * ? " < > |` 等字符，并限制长度，避免 NAS 上的文件系统报错。
- 图片扩展名按文件头判断，WebP/PNG 原样写入，JPEG 保持原格式；解扰后的禁漫图片统一转为 JPEG/PNG。

## 订阅逻辑

1. 在作品详情页点击「订阅追更」，当前最新章节会被记录为基线。
2. 新订阅默认生成每周 Cron：星期和整点取自创建订阅的时间；可在订阅卡片中修改。
3. 发现比基线更新的章节时，自动创建下载任务，逐话打包 CBZ。
4. 在「订阅」页可以开关「启用」（是否检查）与「自动下载」（检查但不自动下载）。

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
| `GET` | `/api/sources/{id}/search?q=&page=` | 搜索 |
| `GET` | `/api/sources/{id}/browse?kind=&page=` | 浏览 |
| `GET` | `/api/sources/{id}/comics/{comicId}` | 作品详情 + 章节 |
| `GET/POST/PATCH/DELETE` | `/api/subscriptions` … | 订阅管理 |
| `GET/POST/DELETE` | `/api/downloads` … | 下载任务 |
| `GET` | `/api/library` | 本地 CBZ 资料库 |
| `GET/PUT` | `/api/settings` | 设置 |
| `GET` | `/api/stats` | 统计 |
| `GET` | `/api/logs?limit=` | 最近运行日志 |
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
git tag v0.0.7 && git push origin v0.0.7     # 构建并发布 :v0.0.7
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
