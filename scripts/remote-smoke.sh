#!/usr/bin/env bash
# T06 smoke test: prove the R2 adapter round-trips real bytes.
# Uploads a real payload to smoke/<random>, reads it back, compares SHA-256,
# checks Head/List, then deletes ONLY its own key. It never touches anything
# outside the smoke/ prefix.
set -euo pipefail

cd "$(dirname "$0")/.."
BIN="${BIN:-go run ./cmd/membox-server}"

PREFIX="smoke/$(date +%s)-$RANDOM"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "==> generating payload"
head -c 1048576 /dev/urandom > "$WORK/payload.bin"
LOCAL_SHA=$(shasum -a 256 "$WORK/payload.bin" | awk '{print $1}')

echo "==> put $PREFIX"
$BIN r2 put "$WORK/payload.bin" --key "$PREFIX"

echo "==> head"
$BIN r2 head "$PREFIX" | grep -q "1048576 bytes" || { echo "FAIL: head size"; exit 1; }

echo "==> get + sha256 compare"
$BIN r2 get "$PREFIX" -o "$WORK/roundtrip.bin"
REMOTE_SHA=$(shasum -a 256 "$WORK/roundtrip.bin" | awk '{print $1}')
[ "$LOCAL_SHA" = "$REMOTE_SHA" ] || { echo "FAIL: sha mismatch $LOCAL_SHA != $REMOTE_SHA"; exit 1; }

echo "==> ls smoke/"
$BIN r2 ls smoke/ | grep -q "$PREFIX" || { echo "FAIL: list"; exit 1; }

echo "==> rm"
$BIN r2 rm "$PREFIX"
$BIN r2 head "$PREFIX" | grep -q "not found" || { echo "FAIL: delete"; exit 1; }

echo "SMOKE OK (sha256 $LOCAL_SHA)"
