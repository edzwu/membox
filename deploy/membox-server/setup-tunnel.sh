#!/usr/bin/env bash
# cloudflared tunnel one-time setup — run ON the VPS.
# Prereq: you created a tunnel in the Zero Trust dashboard (or CLI) and have
# the tunnel TOKEN (dashboard: Networks → Tunnels → your tunnel → Configure).
set -euo pipefail

if [ $# -ne 1 ]; then
  echo "usage: $0 <tunnel-token>"
  echo "token 在 Cloudflare Zero Trust → Networks → Tunnels → 你的 tunnel → Configure 页面"
  exit 1
fi

# Install cloudflared if missing (Debian/Ubuntu).
if ! command -v cloudflared >/dev/null; then
  curl -fsSL https://pkg.cloudflare.com/cloudflared-ascii-repo.txt | tee /etc/apt/trusted.gpg.d/cloudflared.asc
  curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null
  echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared $(lsb_release -cs) main" \
    | tee /etc/apt/sources.list.d/cloudflared.list
  apt-get update && apt-get install -y cloudflared
fi

# Token-based install: config lives Cloudflare-side (dashboard), the service
# just runs the token.
cloudflared service install "$1"
systemctl enable --now cloudflared
systemctl status cloudflared --no-pager | head -5
echo "TUNNEL OK — dashboard 里确认 ingress: 你的域名 → http://127.0.0.1:8081"
