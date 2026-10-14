#!/usr/bin/env bash
# membox-server manual deploy — run ON the VPS.
# Usage:
#   ./deploy.sh           # update: pull code, rebuild, migrate, restart
#   ./deploy.sh init      # first time: everything + allowlist email prompt
set -euo pipefail
cd "$(dirname "$0")"

REPO_DIR="${REPO_DIR:-$HOME/membox}"

echo "==> [1/4] update code"
if [ -d "$REPO_DIR/.git" ]; then
  git -C "$REPO_DIR" pull --ff-only
else
  echo "clone your repo to $REPO_DIR first, or set REPO_DIR"; exit 1
fi

echo "==> [2/4] build + restart containers"
cd "$REPO_DIR/deploy/membox-server"
docker compose up -d --build

echo "==> [3/4] migrate database"
docker compose exec -T server membox-server migrate

echo "==> [4/4] health check"
sleep 2
curl -sf http://127.0.0.1:8081/healthz && echo
docker compose exec -T server curl -sf http://127.0.0.1:8081/readyz 2>/dev/null || \
  curl -sf http://127.0.0.1:8081/readyz || echo "WARN: readyz not green yet, check: docker compose logs server"
echo

if [ "${1:-}" = "init" ]; then
  read -rp "allowlist email (你的登录邮箱): " EMAIL
  docker compose exec -T server membox-server allow "$EMAIL"
  echo "==> allowed $EMAIL — 现在去浏览器打开你的域名登录"
fi

echo "DEPLOY OK"
