# airipress 存储与部署

## 目录约定

```text
data/
  .meta/     权威数据库与文件对象，必须备份
    airipress.db  SQLite 默认数据库
    objects/sha256/{前两位}/{摘要}/content
    objects/sha256/{前两位}/{摘要}/manifest.json
  .build/    导出和发布中间产物，可删除重建
```

`.meta` 保存 SQLite 数据库和不可变原始对象，数据库保存工作区引用、配置和任务的可查询事实；`.build` 永远不是恢复数据的来源，可以随时删除后重建。备份至少包含 `data/.meta/airipress.db` 和 `data/.meta/objects`，对象目录按 SHA-256 校验后备份。

## 数据库

单机默认 SQLite，容器内路径为 `file:/data/.meta/airipress.db?cache=shared`（项目目录对应 `data/.meta/airipress.db`）。多实例或高并发使用 PostgreSQL profile，并设置 `AIRIPRESS_DATABASE_URL`；迁移必须先备份 `.meta` 和数据库。数据库只保存索引、任务和配置元数据，不能替代对象存储。

旧版数据库的自动迁移边界仅限路径迁移：当新路径不存在而同级旧路径 `data/airipress.db`（容器内 `/data/airipress.db`）存在时，启动会将其重命名为 `data/.meta/airipress.db`；若新旧文件同时存在则启动失败，需要人工备份并处理。不会自动合并两个数据库，也不会迁移 PostgreSQL 数据库或对象目录。

## 对象存储

本地部署把对象写入 `/data`。生产可设置 `AIRIPRESS_S3_ENDPOINT`、`AIRIPRESS_S3_BUCKET`、`AIRIPRESS_S3_REGION`、`AIRIPRESS_S3_ACCESS_KEY`、`AIRIPRESS_S3_SECRET_KEY`，兼容 AWS S3、Cloudflare R2 和 MinIO。R2 使用账户 endpoint、region `auto`，并限制 bucket 凭据为最小读写权限。

初版把 S3-compatible 作为最大公约数：除 R2/MinIO 外，可连接提供 S3 API 的 Backblaze B2、Ceph、SeaweedFS 等服务；是否有免费额度由服务商账户与当期政策决定，airipress 不依赖某个免费套餐。无法提供 S3 API 的网盘应作为独立 BlobStore adapter 接入，不能把供应商 SDK 泄漏到 Resource 领域层。

## 安全与一致性

`config/secrets/airipress_secret` 中的根 SECRET 用于会话签名及加密模型凭据，必须持久保存且不得提交仓库；更换它会使现有会话失效，并使历史模型凭据无法解密。写入对象采用临时文件、校验 SHA-256、原子 rename，再写 `.meta` 引用；GC 只能删除已确认无引用且超过保留期的对象。发布任务通过 jobs 查询状态，失败保留错误信息，不删除上一版本产物。

全库归档是实例级 `.airipress` ZIP：包含 8 张业务表的逻辑 records 与全部登记文件对象，不包含 `config`、根 `SECRET` 和 `.build`。导入覆盖当前数据库与对象索引；对象内容离线修改后，导入会重算 SHA-256、size 和 `object_key`，相同内容继续去重。跨实例恢复模型配置必须使用同一根 `SECRET`，否则密文凭据不可用。
