# airipress 架构约定

## 模块边界

服务端 Go 负责 HTTP API、鉴权边界、工作区元数据、任务编排和持久化接口；Python `tools/` 负责内容导出、缩略图、思维导图和站点发布等内容处理。Go 调用 Python 时使用受控 CLI/进程协议，输入输出为 JSON，Python 不直接操作数据库。Web 只调用 `/api/v1`，不读取服务器文件系统。

工作区由三类数据组成：`.meta` 是原始文件对象与固有元数据的权威存储；数据库保存资产索引、引用关系、配置和任务状态；`.build` 是可重建、可删除的构建缓存。工作区引用源文件时只保存内容 ID、相对路径和关系，不复制内容。实例级“全库同步”通过 `.airipress` ZIP 导出/覆盖导入完成。

```text
Web -> Go API -> Store (SQLite/Postgres)
                  |       |
                  |       +-- .meta / object metadata
                  +---------- Python tools (export/mindmap/publish)
                             |
                         .build (ephemeral)
```

内容寻址使用 SHA-256。相同内容只保存一份；重命名只更新引用，不移动内容对象。删除引用不会立即删除对象，清理由后台 GC 根据 `.meta` 的引用计数或 mark-and-sweep 完成。

## 模块与可替换零件

| 模块 | 稳定领域接口 | 初版零件 | 可扩展零件 |
| --- | --- | --- | --- |
| Resource | 内容寻址文件对象、固有元数据 | 本地 BlobStore、S3-compatible BlobStore | WebDAV、网盘专用 adapter |
| Workspace | 工作区、来源引用、逻辑路径 | SQLite/PostgreSQL repository | 多租户权限 repository |
| Archive | 实例级全库导出与覆盖导入 | `.airipress` ZIP | 远程备份存储、定时归档 |
| AI | ChatProvider | OpenAI、DeepSeek、Gemini | 任意 OpenAI-compatible provider |
| Conversation | 消息历史与来源上下文 | 每工作区默认会话 | 多会话、检索引用、SSE |
| Studio | Mindmap、Site source/build | 确定性思维导图、Astro 默认主题 | AI 导图、更多受控主题 |
| Deployment | 静态产物发布 | 已有仓库 GitHub Pages | Cloudflare Pages、对象存储静态站点 |
| Build | 可删除派生产物 | Python CLI、Astro CLI | 隔离 worker/任务队列 |

Go 领域层只依赖这些稳定接口；数据库 DSN、S3 endpoint、AI base URL 与构建工具路径均由配置选择具体零件。

## 角色图与插画差分数据模型

图片本身仍是普通 Resource；角色、服装设定和衍生差分属于独立元数据，不写入全局文件名。建议实体如下：

- `characters(id, franchise, canonical_name, aliases_json)`：例如“碧蓝航线 / 百眼巨人”。
- `character_designs(id, character_id, name, canonical, source_resource_id)`：默认立绘、学园之眠不觉晓等服装/设定基线。
- `illustrations(id, resource_id, design_id, parent_illustration_id, provenance, prompt_snapshot, revision)`：官方图或衍生图；`parent_illustration_id` 表示同流程衍生关系。
- `illustration_tags(illustration_id, dimension, value, vocabulary_version, confidence)`：维度固定为 `scene`、`action`、`camera`、`expression`、`costume`、`pose`、`lighting` 等，值可扩展。
- `illustration_differences(id, from_illustration_id, to_illustration_id, changed_dimensions_json)`：记录从基线到差分图改变了哪些维度。

选择插画时先按角色与 design 限定，再按小说段落的场景/动作/镜头/表情标签评分；相同图片跨工作区只新增引用。官方来源、AI 衍生来源和人工二创必须在 `provenance` 中区分，生成时保存模型、提示词与父图快照，确保可追溯。

## 全库归档与发布

全库导出是实例级操作：归档写入 8 张业务表的逻辑 records（`workspaces`、`models`、`files`、`sources`、`chats`、`messages`、`mindmaps`、`deploy_jobs`）以及所有登记文件对象，不写入 `config`、根 `SECRET` 或 `.build`。导入会覆盖当前实例；对象离线修改后，导入时重新计算内容 SHA-256、大小和 `object_key`，相同内容仍共享一个内容对象。模型凭据是根 `SECRET` 加密的密文，恢复到另一实例必须提供相同 `SECRET`，否则凭据不可解密使用。

发布假设目标是已有 GitHub 仓库（owner/repo 或 remote URL），发布器生成静态产物后推送约定分支，凭据只通过 secret 输入。
