# airipress

airipress 是一个面向个人与小团队的知识工作台：把工作区文件、可配置的模型、对话、来源引用、思维导图和可发布站点放在同一个项目中。服务端提供 `/api/v1` HTTP API，Web 客户端通过同一 API 完成编辑与发布。

## 产品模块

- **工作区**：工作区是文件、来源、对话和思维导图的隔离边界。
- **模型配置**：保存 provider、模型名、API Base URL 与加密后的凭据；请求只引用配置 ID，不在客户端反复传密钥。
- **资产与来源**：上传资产并按 SHA-256 去重；把资产挂到工作区的相对路径后即可作为对话上下文和站点内容来源。
- **对话与消息**：创建工作区对话、追加用户/助手/系统消息，并保留消息时间线和来源 ID。
- **Studio / Mindmap**：Studio 从来源文件名与 Markdown 标题生成思维导图，并保存最新结果。
- **Site**：从工作区来源生成 Astro 默认主题，构建后推送到已有 GitHub 仓库的 Pages 分支。
- **Deploy**：部署任务异步执行，客户端通过任务详情查询状态，不需要长连接等待构建完成。
- **全库同步**：在实例级导出 `.airipress` ZIP 归档，或以归档覆盖导入当前实例。

全库同步归档包含 8 张业务表的逻辑 records（工作区、模型、文件、来源、对话、消息、思维导图、部署任务）及全部登记的文件对象；不包含 `config`、根 `SECRET` 或 `.build`。对象在离线修改后导入时会重新计算内容 SHA-256、大小和 `object_key`，同一内容仍按内容寻址去重。模型凭据保存为根 `SECRET` 加密的密文；跨实例恢复必须使用同一根 `SECRET`，否则凭据不可用。导入会覆盖当前实例的数据，无法撤销。

完整的请求、响应和错误契约见 [api/openapi.yaml](api/openapi.yaml)。

## 快速开始

需要 Docker 24+ 与 Compose v2。Linux 云服务器可直接运行：

```bash
curl -fsSL https://raw.githubusercontent.com/mo2iairi/airipress/master/deploy/install.sh | bash -s -- ./airipress
```

该一行安装方式要求 GitHub 仓库及 `master` 分支可公开读取；私有仓库会被 GitHub 的 raw 地址以 404 隐藏。私有部署请先通过 SSH 或 GitHub CLI 克隆仓库，再从检出目录运行 `./deploy/install.sh`。

安装器只在缺失时下载根目录 `compose.yaml` 和 `config/config.example.yaml`。首次安装会询问公开 Web 端口、`SECRET` 和管理员账密；留空会安全生成随机值，并自动执行 `docker compose up -d`。已有 `config/config.yaml` 时不会覆盖它，但必须同时保留 `config/secrets/airipress_secret`。生成的管理员密码只显示一次，请立即保存。`--dry-run` 只显示计划，不下载、不创建文件，也不启动容器。

开发环境也可直接运行：

```bash
make test
make build
```

## 配置与部署

认证、OAuth 与允许来源统一位于 `config/config.yaml`，密码只保存为 bcrypt cost 12 哈希；根 SECRET 位于 `config/secrets/airipress_secret`，用于会话签名及模型凭据加密。Compose 默认使用本地 SQLite、`127.0.0.1:3000` 和项目下的数据目录。根目录 `.env` 只保存 Docker 在启动前必须读取的非敏感部署参数（端口、绑定地址、镜像归属、数据目录）；安装器会在首次安装时生成它，绝不生成 `config/.env`。可参考 [.env.example](.env.example)。

GitHub Pages 发布支持 OAuth。将 OAuth App 的 `client_id`、`client_secret` 和回调地址写入 `config/config.yaml` 的顶层 `github` 节点；Studio 中点击“连接 GitHub”后授权，发布时不再需要粘贴 Token。回调地址必须与 GitHub OAuth App 完全一致，例如 `http://127.0.0.1:3000/api/v1/github/callback`。

### 静态网站主题

Studio 内置 `astro-default` 与 `hugo-default`。可在 Studio 的主题库中直接粘贴 GitHub HTTPS Git 链接（例如 `https://github.com/owner/repository.git`），并填写可选分支或标签导入主题；连接 GitHub 后可导入有权限的私有仓库。安装会缓存当前 commit，之后可预览、选择并发布。主题仓库必须包含 `airipress.theme.json`，内容至少为 `{"engine":"astro"}` 或 `{"engine":"hugo"}`。Astro 主题需要提供 `src/layouts/Base.astro`；Hugo 主题采用标准 Hugo theme 目录结构。

```json
{"engine":"hugo"}
```

默认使用服务端本地 SQLite 与文件目录，适合单机。生产环境建议：

1. 使用 `--profile postgres` 启用 PostgreSQL，并通过受限权限的部署环境变量设置 PostgreSQL 密码及 `AIRIPRESS_DATABASE_URL=postgres://airipress:<密码>@postgres:5432/airipress?sslmode=disable`。
2. 使用 `--profile minio` 启用兼容 S3 的 MinIO，并通过受限权限的部署环境变量设置 endpoint `http://minio:9000`、bucket 与凭据；也可填入已有 S3/R2 的 endpoint、bucket、region 与凭据。未设置 endpoint 时使用本地 `.meta`。
3. 只暴露 Web 端口；API 由 Web 反向代理或内网访问。默认绑定 `127.0.0.1`，本地配置同时允许 `localhost` 与 `127.0.0.1`。若要公网访问，请改为 `0.0.0.0`、配置 TLS，并将 `cookie_secure: true` 与实际前端域名写入 `allowed_origins`。

示例：

```bash
docker compose -f compose.yaml --profile postgres --profile minio up -d
```

架构与存储约定见 [docs/architecture.md](docs/architecture.md) 和 [docs/storage.md](docs/storage.md)；非敏感部署变量见 [.env.example](.env.example) 与 `compose.yaml`。

## 安全边界

服务端密钥、数据库和资产目录属于敏感数据，不要提交配置和 secret 文件。登录使用 HttpOnly、SameSite Strict 会话 Cookie，写请求还要求同源校验；生产部署仍应在网关启用 TLS。发布器只执行固定的 Astro 与 GitHub Pages 流程，不接受任意命令。

## 镜像发布

`.github/workflows/container-images.yml` 在提交到 `master` 时构建并推送两个 GHCR 镜像的 `master`、提交 SHA 和 `latest`（默认分支）标签；推送 `v*` 标签时同时发布语义化版本标签和构建来源证明。Pull Request 只构建验证，不推送镜像。镜像名默认是 `ghcr.io/mo2iairi/*`；fork 发布时在目标实例的根 `.env` 设置 `AIRIPRESS_IMAGE_OWNER=<你的 GitHub 用户或组织>`。公开安装要求容器包随公开仓库保持公开可拉取。
