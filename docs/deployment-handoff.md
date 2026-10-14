# Deployment Handoff — membox remote node

> 写给接手部署的 agent：目标是把 `membox-server` 跑在 VPS 上，公网可达，
> 邮箱 OTP 登录，PDF/Markdown 上传到 Cloudflare R2。
> 权威细节：`docs/membox-remote-stage1-plan.md`（计划+验收）、
> `docs/remote-stage1.md`（ADR）、`docs/remote-deploy.md`（操作手册）。

## 1. 代码现状（main 分支之上的未提交工作）

全部是**新增包**，不改动现有 `mm`/`mmd`/Companion 行为：

| 路径 | 内容 |
|---|---|
| `cmd/membox-server/` | 远端节点二进制。子命令：`serve`（默认）、`migrate`、`allow <email>`、`r2 put/get/head/rm/ls` |
| `internal/interfaces/httpserver/` | HTTP API（`/healthz`、`/readyz`、`/v1/auth/*`、`/v1/documents/*`、`/v1/topics|issues/*`）+ React 主页（`home/` 源码 → vite → `static/` 嵌入） |
| `internal/infrastructure/r2/` | Cloudflare R2 adapter（S3 兼容，aws-sdk-go-v2） |
| `internal/infrastructure/postgres/` | pgx 连接池、golang-migrate 内嵌迁移（`migrations/000001_init`、`000002_topics`）、Document/Auth/Topic 三个 store |
| `internal/remoteauth/` | 邮箱 OTP（HMAC 存储、10min、5 次、防重放）+ session（哈希存储）+ 限流 + SMTP/日志 mailer |
| `internal/remotedocs/` | 文档服务：流式上传、PDF magic 校验、sha256 内容寻址、If-Match CAS、软删除 tombstone |
| `internal/remotetopics/` | topics（集合）+ issues（独立问题实体）+ 两张关系表 |
| `deploy/membox-server/` | Dockerfile（node+go 多阶段）+ compose.yml（PG 内网不暴露，server 绑 loopback） |
| `scripts/remote-smoke.sh` | R2 端到端冒烟（put/head/get/sha256 比对/ls/rm） |
| `.env.example` | 全部配置字段模板；`.env` 已 gitignore |

## 2. 已验证（在开发机上跑通真实 PG + 真实 R2）

- `./scripts/remote-smoke.sh` 通过：1MB payload 上 R2、sha256 回环一致、删除确认
- 完整 curl 链路：OTP 登录 → 上传 PDF → 列表 → 下载（sha256 一致）→ PATCH rev-1 → 重放 412 → 软删 204 → 删后 404
- topics/issues：跨 topic 挂同一 issue、answered 自动写 resolved_at、删 topic 保 issue、未登录 401
- `go build ./...` + `go test ./...` 全绿（**两个例外见第 5 节**）

## 3. 部署前必须做的事（按顺序）

```bash
# 0. 代码先入库（当前未提交！）
git add -A && git commit -m "feat: membox remote node (M0-M4 + topics)" && git push

# 1. VPS 上
git clone <repo> ~/membox && cd ~/membox
cp .env.example .env
```

`.env` 必须填的真实值（开发机 `.env` 里已有 R2 dev 凭据可参考，**生产建议换 membox-prod bucket + 新凭据**，plan T04）：

```
POSTGRES_PASSWORD=<openssl rand -hex 24>
R2_ACCOUNT_ID / R2_BUCKET / R2_ACCESS_KEY_ID / R2_SECRET_ACCESS_KEY
OTP_HMAC_SECRET=<openssl rand -hex 32>
SMTP_HOST/PORT/USERNAME/PASSWORD/FROM   # 事务邮件服务（Resend/SES/Postmark）
PUBLIC_BASE_URL=https://<域名>           # 决定 cookie Secure + CSRF Origin 白名单
```

`APP_ENV=production` 和 `DATABASE_URL` 由 compose 注入，不要手填。

```bash
# 2. 启动 + 初始化
cd deploy/membox-server
docker compose up -d --build
docker compose exec server membox-server migrate
docker compose exec server membox-server allow <你的邮箱>
curl http://127.0.0.1:8081/healthz   # 期望 {"status":"ok"}
curl http://127.0.0.1:8081/readyz    # postgres/r2 均应为 ok
```

## 4. 公网暴露

推荐 Cloudflare Tunnel（不开端口、自带 HTTPS，cookie `Secure` 依赖它）：

```bash
cloudflared tunnel create membox
cloudflared tunnel route dns membox <域名>
# ingress: <域名> → http://127.0.0.1:8081，然后 cloudflared tunnel run（或装 systemd service）
```

验收：`https://<域名>` 打开出现登录页 → 白名单邮箱收验证码 → 登录 → ⌘K 搜索（空列表正常）→ 上传一个 PDF 后列表可见、可下载。

## 5. 已知坑 / 注意事项

1. **main 分支有两个既有测试失败**（与本次工作无关，勿顺手"修"出新问题）：
   `TestWebServerAutomaticallyMigratesExistingClipNotes`、`TestMiruFrontendHasNoMemboxBackendDependency`。
2. **golang-migrate scheme**：URL 会被重写为 `pgx5://`（pgx/v5 driver 注册名），排查连接问题时注意日志里的 scheme 不是笔误。
3. **生产环境 OTP_HMAC_SECRET 为空会拒绝启动**（故意的）；SMTP 未配置时生产环境不发码（LogMailer 在生产会报错）。
4. **server 启动只校验 schema 版本、不自动迁移**（T08）。升级流程：`git pull && docker compose up -d --build && docker compose exec server membox-server migrate`。
5. **删除不碰 R2**：软删除只写 tombstone revision；blob 是内容寻址共享的，永远不在请求路径里删。
6. 文档列表**禁止**用 R2 ListObjects 实现——一律查 PG（T05）。
7. `home/` 前端改动后需要 `make remote-web` 重新嵌入，再构建镜像。

## 6. 数据库速览

`auth_allowlist / users / otp_challenges / sessions / documents / document_versions / topics / issues / topic_members / topic_issues`（+ golang-migrate 的 `schema_migrations`）。设计原则：秘密只存哈希/HMAC，blob 只在 R2，documents 走不可变版本链 + CAS，所有查询 owner 级隔离（越权=404）。
