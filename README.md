# Manco

自托管的漫画 / 书籍订阅下载器。Go 后端 + Vue 3 前端，单镜像部署，容器端口 `15600`。

- 从内容源搜索、浏览、订阅作品，发现新章节后自动下载
- **漫画与插件书籍一话一个 `.cbz`**，并写入封面与 `ComicInfo.xml`；内置电子书源保留原始 EPUB / PDF / MOBI / AZW3
- 支持多内容源、拓展仓库、代理、本地库、运行日志与全局繁转简
- 配置与数据分离：`/app/config` 存模块化配置，`/app/data` 存数据库、缓存与密钥

## 部署

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
      # 按需映射下载目录，例如：
      # - /vol3/1000/家庭影音/:/家庭影音:shared
    environment:
      - TZ=Asia/Shanghai
    restart: unless-stopped
```

```bash
docker compose pull
docker compose up -d
docker compose logs -f
```

访问 `http://<主机IP>:15600`，首次进入创建账号。下载位置在作品下载 / 订阅设置里选择容器内已映射目录。

GHCR 镜像为 `linux/amd64`。若拉取提示 `denied`，把 GitHub 包 `manco` 的可见性改为 Public，或先 `docker login ghcr.io`。

## 目录与环境变量

| 容器路径 | 用途 |
| --- | --- |
| `/app/config` | 设置、账号、来源、拓展仓库、订阅等模块化配置；登录密码 bcrypt 存储，来源凭据加密保存 |
| `/app/data` | SQLite、会话、探索发现缓存、图标索引、下载临时缓存与密钥 |

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MANCO_ADDR` | `:15600` | 监听地址 |
| `MANCO_SECRET` | 自动生成 `config/secret.key` | 凭据加密密钥，建议显式设置并备份 |
| `MANCO_DATA_DIR` | `/app/data` | 运行数据目录 |
| `MANCO_CONFIG_DIR` | `/app/config` | 用户配置目录 |
| `MANCO_COOKIE_SECURE` | `false` | 反向代理启用 HTTPS 后设为 `true` |

下载线程数、每下载 N 章暂停 M 分钟属于内容源设置，在「资源仓库」的单个来源卡片里配置；站点域名、Cookie、代理与内容源图标也在同一编辑窗口维护。

## 内容源

内置来源：哔咔漫画、禁漫天堂、包子漫画、Z-Library。「资源仓库」分为漫画源 / 书籍源 / 其他源 / 拓展仓库。

- 哔咔需要账号密码登录
- 禁漫天堂、包子漫画免登录可浏览，遇到校验时填写站点域名与 Cookie
- Z-Library 使用邮箱密码登录，下载原始电子书文件；插件书籍按 Kototoro 结构生成 CBZ
- 拓展仓库支持 Kototoro / Mihon / JAR / Aniyomi 等兼容仓库；Kototoro Parsers 的 JAR/DEX 插件由内置 JVM 运行时执行，APK 类型仅登记清单
- 网络受限时在「系统设置 → 代理」配置 HTTP/HTTPS 代理

## 本地开发

```bash
# 后端，默认监听 :15600
go run ./cmd/manco

# 前端开发服务器，/api 自动代理到 15600
cd cmd/manco/web
npm install
npm run dev
```

构建与检查：

```bash
cd cmd/manco/web && npm run build   # 先构建前端，产物在 cmd/manco/web/dist
go build ./...
go vet ./...
go test ./...
```

前端改动后必须重新 `npm run build`，否则 `go build` 内嵌的仍是旧 dist。

## 发布

`.github/workflows/docker.yml` 在 push `main` 或推送 `v*` 标签时执行测试并构建 `linux/amd64` 镜像，推送到 `ghcr.io/hyaeve/manco`（`:latest` 或对应版本标签）。

```bash
git tag v0.5.1 && git push origin v0.5.1   # 构建并发布 :v0.5.1
docker compose pull && docker compose up -d  # 升级到新镜像
```

## 说明

本项目只做本地缓存式下载，请自行确认使用地区与相关站点的规定，仅用于个人备份。内容源接口可能随时变化，失效时可在来源编辑窗口调整域名 / Cookie，或在 `internal/source/` 下更新解析逻辑。
