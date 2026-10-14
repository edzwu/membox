# 部署 membox remote node 到 VPS

前置：VPS 装好 Docker（含 compose 插件）；Cloudflare 侧已有私有 bucket + 限定凭据（plan T04）。

## 1. 拉代码

```bash
git clone <repo> ~/membox && cd ~/membox
```

## 2. 配置

```bash
cp .env.example .env
```

编辑 `.env`（compose 会读取它）：

```dotenv
# compose 用：DB 密码（自定义一个强随机值）
POSTGRES_PASSWORD=<openssl rand -hex 24>

# Cloudflare R2（membox-prod bucket，和 dev 分开）
R2_ACCOUNT_ID=...
R2_BUCKET=membox-prod
R2_ACCESS_KEY_ID=...
R2_SECRET_ACCESS_KEY=...

# OTP 签名密钥
OTP_HMAC_SECRET=<openssl rand -hex 32>

# 发信（事务邮件服务，如 Resend/SES/Postmark 的 SMTP）
SMTP_HOST=smtp.resend.com
SMTP_PORT=587
SMTP_USERNAME=resend
SMTP_PASSWORD=...
SMTP_FROM=membox@你的域名

# 公网地址（cloudflared 隧道/Caddy 的域名）
PUBLIC_BASE_URL=https://membox.你的域名
```

注意：`DATABASE_URL` 和 `APP_ENV=production` 由 compose 注入，不用手填。

## 3. 启动 + 初始化

```bash
cd deploy/membox-server
docker compose up -d --build

# 建表（幂等，可重复跑）
docker compose exec server membox-server migrate

# 把你自己加进登录白名单（对每个用户跑一次）
docker compose exec server membox-server allow you@example.com
```

验证：

```bash
curl http://127.0.0.1:8081/healthz   # {"status":"ok"}
curl http://127.0.0.1:8081/readyz    # postgres / r2 都应为 ok
```

## 4. 暴露到公网（二选一）

**Cloudflare Tunnel（推荐，不开端口、自带 HTTPS）：**

```bash
cloudflared tunnel create membox
# 配置 ingress: membox.你的域名 → http://127.0.0.1:8081
cloudflared tunnel route dns membox membox.你的域名
cloudflared service install   # 或 systemd 跑 cloudflared tunnel run membox
```

**Caddy：**

```caddy
membox.你的域名 {
    reverse_proxy 127.0.0.1:8081
}
```

## 5. 使用

浏览器打开 `https://membox.你的域名` → 输入白名单邮箱 → 收验证码 → 登录。
⌘K 搜索文档；`POST /v1/documents`（multipart `file` 字段）上传 PDF/Markdown。

## 运维要点

- **升级**：`git pull && docker compose up -d --build`，再跑一次 `migrate`（无新迁移时 no-op）。
- **备份**：PG 卷 `pgdata`（元数据）+ R2（内容，Cloudflare 侧已有冗余）。
- **日志**：`docker compose logs -f server`。
- **Schema 安全**：server 启动只校验版本不自动迁移（T08）；dirty 状态需手工处理后重启。
