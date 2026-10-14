# Membox Remote — 第一阶段开发计划

**制定日期：2026-10-13**  
**目标：** 在现有 Go Membox 代码库中增加一个可独立启动的多用户 Remote Server。本地完成开发和测试，再把相同的服务部署到 VPS。用户通过认证后可以上传 PDF/Markdown，内容保存在 Cloudflare R2，元数据及版本历史保存在 PostgreSQL。不同用户的数据必须隔离。

## 0. 边界与验收定义

**本阶段交付：** Go Server、邮箱白名单 + OTP 登录、会话鉴权、PostgreSQL Migration、R2 BlobStore、Document API、基本不可变版本历史、VPS HTTPS 部署、自动化测试及备份。

**暂不交付：** `mm sync` / 本地 SQLite 与远端数据同步、WXT Clipper 和 Miru 远端模式、云端 FTS/PDF 转换、Agent 执行、公开分享、多设备冲突合并、FastCDC/LayerFS、R2 预签名直传。浏览器一键保存 PDF 是下一阶段的集成任务；本阶段先通过 HTTP API 验证同一条上传链路。

**通过标准（DoD）：** 用户 A 与 B 能分别登录；A 上传真实 PDF，R2 存在对应对象，PG 存在文档和 v1 版本；下载后 SHA-256 一致；B 对 A 的列表、读取、修改、删除都无法越权；修改内容能产生 v2，陈旧版本提交失败；软删除后普通 API 不可访问；上述流程在本地和 VPS HTTPS 环境都能重复执行；重启服务器后数据仍在。

## 1. 固定架构决策（实施中不要擅自改变）

- 复用现有 `domain/`、`application/` 及 UUIDv7 身份语义；单独新增 Remote Server 进程。**不要复制本地 SQLite 文件，不要重写本地扫描器、Miru、FTS5 或 mmd。**
- 本地 Membox 保持 `SQLite + 本地文件系统`；远端采用 `PostgreSQL + R2`。两端未来通过文档级 Sync API 交换状态，不进行数据库复制。
- `document_id` 为稳定 UUIDv7；SHA-256 只标识内容，不标识文档。远端数据库对已发布的文档元数据和版本负责；R2 保存不可变 Blob。
- R2 Bucket 私有。按用户使用 `users/{user_id}/blobs/sha256/{sha256}` 作为 Object Key；Server 从认证会话推导 `user_id`，不得接受客户端提供 `owner_id` 或任意 `blob_key`。授权必须在 SQL 查询和业务层执行，不能只靠 Key 前缀。
- 用户通过**邮箱白名单 + 一次性验证码 OTP** 登录。首次验证成功且在白名单中的邮箱自动创建用户；禁止无白名单自助注册。生产环境不打印验证码。
- 先由 Go Server 代理上传/下载，限制单个文件 **50 MiB（可配置）**。后续再考虑浏览器直传 R2。
- API 删除是**软删除**，暂不物理删除历史 Blob；R2 的 `DeleteObject` 仅用于隔离的 Storage Smoke Test。暂不实现自动垃圾回收。
- 每次内容或元数据更新产生新的文档 revision，版本记录保留完整文档状态。客户端修改或删除时携带 `If-Match: "rev-N"`；缺失返回 428，版本不匹配返回 412。

## 2. 预计目录结构（以仓库现有命名习惯为准）

```text
cmd/membox-server/main.go
internal/domain/                          # 复用已有 Document / UUID
internal/application/                    # 增加 remote document 服务和 ports
internal/infrastructure/postgres/        # Repository + migrations/
internal/infrastructure/r2/              # BlobStore 实现
internal/interfaces/httpserver/          # API handlers / auth middleware
internal/remoteauth/                     # OTP / session / allowlist
scripts/remote-smoke.sh
deploy/membox-remote/                      # compose.dev.yml / systemd / nginx 示例
docs/remote-stage1.md                     # 架构和操作手册
```

不要求照搬这些包名。开始编码前先检查当前代码的依赖方向，遵循已存在的 ports / adapters 边界，不为了远端复用而使本地模块依赖远端数据库。

## 3. 任务清单（按顺序执行）

### M0 — 工程入口与边界（0.5 天）

- [x] **T01 仓库审计：** 查找现有 Document、UUIDv7、BlobStore、HTTP Handler、配置加载及 Migration 约定；记录可复用代码与必须新增的接口。不要未经审查移动现有代码。输出 `docs/remote-stage1.md` 的首版 ADR。
- [x] **T02 Remote 启动入口：** 新增 `cmd/membox-server`、配置加载和 `GET /healthz`（进程存活）及 `GET /readyz`（PG / R2 依赖可用性，避免每次探测过重）。监听地址由配置决定，开发默认 `127.0.0.1:8081`。优雅关闭 HTTP 与数据库连接。
- [x] **T03 配置模板：** 提供 `.env.example`，包括 `APP_ENV`、`HTTP_ADDR`、`DATABASE_URL`、`R2_ACCOUNT_ID`、`R2_BUCKET`、`R2_ACCESS_KEY_ID`、`R2_SECRET_ACCESS_KEY`、`SMTP_*`、`OTP_HMAC_SECRET`、`PUBLIC_BASE_URL` 和 `MAX_UPLOAD_BYTES`。真实凭据不得提交 Git。

**M0 验收：** `go run ./cmd/membox-server` 启动；`curl http://127.0.0.1:8081/healthz` 返回 200；不影响现有 `mm`、`mmd`、Companion。

### M1 — R2 最小存储实验（0.5–1 天）

- [x] **T04 建立隔离资源：** 在 Cloudflare 创建私有 `membox-dev` Bucket 和仅限该 Bucket 的 Object Read & Write 凭据；另行规划 `membox-prod`，不要共用测试 Key。配置 S3 Endpoint `https://<ACCOUNT_ID>.r2.cloudflarestorage.com`，Region `auto`。
- [x] **T05 R2 Adapter：** 实现最小 `Put(ctx,key,reader,size)`、`Get(ctx,key)`、`Head(ctx,key)`、`Delete(ctx,key)`；可另有仅供测试的 `List(prefix)`。使用 `aws-sdk-go-v2`；生产文档列表**从 PostgreSQL 查询**，不是 ListObjects。
- [x] **T06 Smoke Test：** 上传一个真实 PDF 到 `smoke/<random-id>`，读回字节，分别计算本地 SHA-256 并比较；检查 Head/大小；列出测试前缀；删除测试对象并确认不存在。只清理 `smoke/` 前缀，绝不批量清空 Bucket。

**M1 验收：** R2 Put/Get/Head/List/Delete 全部可运行；无 R2 凭据时服务清晰报错；测试不污染正式文档。

### M2 — PostgreSQL 与 Migration（1 天）

- [x] **T07 开发数据库：** 用 Docker Compose 启动本地 PostgreSQL，只绑定 loopback，并配置持久化 volume、健康检查。数据库连接用 `pgx`。
- [x] **T08 Migration：** 使用 `golang-migrate`，至少准备 `000001_init.up.sql` / `down.sql`；新增 Makefile 入口，如 `make remote-db-up`、`make remote-migrate`。启动 Server 时检查 Schema 版本，不自动执行破坏性迁移。
- [x] **T09 初始 Schema：** 建立 `auth_allowlist`、`users`、`otp_challenges`、`sessions`、`documents`、`document_versions`。必要约束：`users.email` 唯一；`documents.owner_id` 外键；`document_versions(document_id, revision)` 联合主键；文档索引含 `(owner_id, deleted_at, updated_at)`。
- [x] **T10 Repository：** 实现用户、Session、文档和版本的 PG 持久化。所有文档查询必须带认证得到的 `owner_id`；不要做先按 ID 查出来再由 Handler 临时判断归属的默认实现。

**建议核心字段：** `documents(id UUID PK, owner_id, current_revision, title, source_url, deleted_at, created_at, updated_at)`；`document_versions(document_id, revision, operation, title, source_url, sha256, blob_key, size_bytes, mime_type, created_at)`。版本记录是不可变的快照；软删除写入新的 tombstone revision。

**M2 验收：** 空数据库执行 Migration 成功；重复执行无重复建表；在临时测试数据库上可验证 up/down/up；Server 能连接并进行 Repository CRUD。

### M3 — 多用户鉴权（1–2 天）

- [x] **T11 OTP 申请：** `POST /v1/auth/otp/request` 输入邮箱，检查白名单，生成安全随机验证码，有效期 10 分钟、最多 5 次尝试。数据库仅保存验证码的 HMAC/安全校验值，不能明文存储。对请求频率按 IP 和邮箱限流，响应避免泄漏白名单成员信息。
- [x] **T12 发信：** 本地使用 Mailpit 接收测试邮件；VPS 使用正式 SMTP/事务邮件服务。开发测试通过邮件收件箱或测试接口获取验证码，生产环境绝不记录验证码明文。
- [x] **T13 OTP 验证与建用户：** `POST /v1/auth/otp/verify` 成功后原子消费验证码；白名单用户首次验证时自动创建 `users` 记录，后续沿用原用户 UUID。失败和重复使用都要拒绝。
- [x] **T14 Session：** 生成高熵随机 Session Token，服务端仅保存其哈希；使用 `HttpOnly`、生产 `Secure`、合理 `SameSite` 的 Cookie。实现 `GET /v1/me`、`POST /v1/auth/logout`、过期校验、鉴权中间件和状态变更请求的 Origin/CSRF 防护。生产要求 HTTPS。

**M3 验收：** A、B 分别可以登录并得到不同 `user_id`；非白名单不能注册；无 Session、过期 Session、注销后的 Session 返回 401；OTP 不能重复使用；请求限流有效。

### M4 — 多用户 Document API（1.5–2 天）

- [x] **T15 创建：** `POST /v1/documents` 接收 multipart 的 `file`、`title`、可选 `source_url`。第一阶段仅允许 PDF、Markdown；用 `http.MaxBytesReader` 及流式临时文件限制内存和总大小。服务器验证类型（PDF magic bytes 等），计算 SHA-256，先 Put Blob 至 R2，再在 PostgreSQL 事务中创建稳定 UUIDv7 文档与 v1 版本。
- [x] **T16 读取：** `GET /v1/documents?limit=&page_token=`、`GET /v1/documents/{id}`、`GET /v1/documents/{id}/content`。列表分页与排序稳定，默认排除软删除；内容由 Server 授权后从 R2 流式返回。初期不要求 HTTP Range，但在下一阶段 Miru 在线 PDF 阅读前必须补上。
- [x] **T17 修改及版本：** `PATCH /v1/documents/{id}` 修改标题/来源；`PUT /v1/documents/{id}/content` 上传新内容。两者均需 `If-Match: "rev-N"`，成功创建 v(N+1) 快照。CAS 更新和版本插入处于同一数据库事务；并发版本不匹配返回 412，旧 Blob 保留。
- [x] **T18 软删除：** `DELETE /v1/documents/{id}` 必须认证并携带 If-Match；写 tombstone revision 和 `deleted_at`，不可物理删除 R2 原始内容。未授权访问返回 404，避免泄漏其他用户文档是否存在。
- [x] **T19 失败语义：** R2 上传失败则不发布 PG 文档；PG 提交失败可能留下未引用 Blob，记录日志并允许后续 GC，不执行会误删共享内容的立即清理；下载校验与错误响应统一。`source_url` 仅存元数据，不由服务端主动抓取 URL，避免引入 SSRF。

**M4 验收：** A 可以创建、列表、读取、修改、产生 v2 并软删除文档；B 对 A 的文档一律 404；相同文件上传与下载 SHA-256 一致；两个请求用相同 revision 更新同一文档时，只有一个成功，另一个 412。

### M5 — 测试、部署及交付（1–2 天）

- [ ] **T20 自动化测试：** Unit 测试覆盖端口和权限；PostgreSQL 集成测试使用临时数据库；R2 真实集成测试仅运行在独立 Bucket/测试前缀。新增 `scripts/remote-smoke.sh`：A 登录、上传 PDF、下载比对、B 越权测试、A 更新、过期版本冲突、软删除、服务重启后校验。
- [ ] **T21 CI/本地命令：** 把 Remote Server 纳入已有 `make check`、`test`、`test-race`、`build-all`，并增加可选 `make remote-integration`（需要真实外部资源时明确标记，不让无凭据的默认 CI 失败）。
- [ ] **T22 VPS 安装：** 部署 PostgreSQL（仅本地网络监听）、Go 编译产物、Migration 和 Systemd service。单独使用运行账户和权限受限的环境文件；利用 VPS 已有的 Nginx HTTPS 反向代理，不抢占已有 80/443 配置。只向公网开放 HTTPS，不暴露 PG 或 R2 凭据。
- [ ] **T23 生产准备：** 使用单独的 R2 正式 Bucket/Token 和 PostgreSQL DB，配置访问日志、结构化错误日志、每日 `pg_dump` 备份及恢复演练。为 OTP、登录失败、上传大小和请求频率设置限制；不要将验证码、Session Token、数据库密码写进日志。
- [ ] **T24 VPS 验收：** 通过真实域名 HTTPS 执行与本地同一组 Smoke Tests；重启服务并验证数据存在；确认 `mm`、mmd、Companion 的现有测试全部通过。写清部署、Migration、备份、恢复、回滚 SOP。

**M5 验收：** 一条命令可运行 Smoke Test；服务可以干净启动/停止和重启；HTTPS 正常、跨用户隔离正常、R2 存取正常；可以用备份恢复 PostgreSQL 数据。

## 4. API 契约（第一阶段）

| Method | Endpoint | 目的 |
|---|---|---|
| GET | `/healthz`, `/readyz` | 服务健康状态 |
| POST | `/v1/auth/otp/request` | 申请验证码 |
| POST | `/v1/auth/otp/verify` | 验证并建立 Session |
| POST | `/v1/auth/logout` | 注销 |
| GET | `/v1/me` | 获取当前登录用户 |
| POST | `/v1/documents` | 创建 PDF/Markdown 文档及 v1 |
| GET | `/v1/documents` | 用户自己的分页列表 |
| GET | `/v1/documents/{id}` | 读取元数据及当前 revision |
| GET | `/v1/documents/{id}/content` | 下载当前 Blob |
| PATCH | `/v1/documents/{id}` | 修改元数据，产生新 revision |
| PUT | `/v1/documents/{id}/content` | 替换内容，产生新 revision |
| DELETE | `/v1/documents/{id}` | 软删除，产生 tombstone revision |

`GET /v1/documents/{id}` 返回 `ETag: "rev-1"`，修改/删除时客户端提交同值 `If-Match`。跨用户资源访问返回 404；没有登录返回 401；缺少版本前置条件返回 428；版本不匹配返回 412；不支持的内容类型返回 415；超限返回 413。返回 JSON 错误码与稳定的错误描述，避免泄漏数据库/R2 内部信息。

## 5. 开发顺序、里程碑和实际操作

| 工作日（预估） | 完成内容 | 交付结果 |
|---|---|---|
| Day 1 | T01–T06 | Remote Server 启动 + R2 SDK Smoke Test |
| Day 2 | T07–T10 | PostgreSQL Migration + Repository |
| Day 3–4 | T11–T14 | OTP 白名单登录 + 多用户会话 |
| Day 5–6 | T15–T19 | Document API + 版本 + 软删除 |
| Day 7–8 | T20–T24 | 跨用户自动化测试 + VPS HTTPS 部署 |

以上是开发时间估计，不包括邮件域名验证、服务器环境故障或现有接口重构导致的延误。每个里程碑都应合并为一个可独立测试的 PR/Commit，而不是最后一次性提交。

**你现在应该执行的第一步：** 先阅读现有 `internal/domain`、`internal/application`、`internal/infrastructure`、`internal/web/backend`，完成 T01，输出可复用接口清单；然后立即执行 T02–T06，证明同一个 Go 项目可以启动 Remote Server，并且能通过受限凭据访问私有 R2 Bucket。不要先花时间写 Clipper 前端或同步协议。

## 6. 第一阶段结束后再启动的任务

- **第二阶段 A：** WXT Clipper 支持云端登录与 Save PDF，调用已实现的 Document API；解决扩展登录和 API 授权，不应把 R2 Key 发给浏览器。
- **第二阶段 B：** Miru 远端阅读 / HTTP Range 支持，文件按需缓存。
- **第三阶段：** `sync_log`、远端 Cursor API、本地 `mm sync pull`，再逐步加入 Outbox/双向同步、冲突处理与 Agent 写入。

## 7. 参考文档

- [Cloudflare R2 — AWS SDK for Go](https://developers.cloudflare.com/r2/examples/aws/aws-sdk-go/)
- [Cloudflare R2 — S3 API 与凭据](https://developers.cloudflare.com/r2/get-started/s3/)
- [golang-migrate/migrate](https://github.com/golang-migrate/migrate)
