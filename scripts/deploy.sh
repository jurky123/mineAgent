#!/usr/bin/env bash
# 安全部署：暂存构建 → 成功查询对局 → 原子替换二进制 → 重启。
# 用法：scripts/deploy.sh [--force]；查询失败也必须显式 --force 才能绕过。
set -euo pipefail
cd "$(dirname "$0")/.."

if [[ $# -gt 1 || ($# -eq 1 && "$1" != "--force") ]]; then
  echo "用法：scripts/deploy.sh [--force]" >&2
  exit 1
fi

CONFIG="${CONFIG:-config.json}"
DEPLOY_BUILD_DIR=$(mktemp -d "${TMPDIR:-/tmp}/mineagent-deploy.XXXXXX")
trap 'rm -rf "$DEPLOY_BUILD_DIR"' EXIT

echo "==> 构建（暂存，不覆盖运行中的二进制）"
make build BUILD_OUTPUT="$DEPLOY_BUILD_DIR/mineagent"

if [[ "${1:-}" == "--force" ]]; then
  echo "==> --force：跳过对局检查"
else
  if ! WEB_SETTINGS=$(python3 - "$CONFIG" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    web = json.load(f).get('web', {})
listen = web.get('listen', '')
data_dir = web.get('dataDir', 'data/webui')
if not isinstance(listen, str) or not listen or not isinstance(data_dir, str) or not data_dir:
    raise ValueError('web.listen / web.dataDir 无效')
print(listen)
print(data_dir)
PY
  ); then
    echo "==> 无法读取网页监听配置，拒绝部署；确认要跳过检查用 --force" >&2
    exit 1
  fi
  LISTEN="${WEB_SETTINGS%%$'\n'*}"
  DATA_DIR="${WEB_SETTINGS#*$'\n'}"
  TOKEN_FILE="${TOKEN_FILE:-$DATA_DIR/deploy-token.txt}"
  if ! python3 - "$TOKEN_FILE" "$DEPLOY_BUILD_DIR/auth-header" <<'PY'
import sys
with open(sys.argv[1]) as f:
    token = f.read().strip()
if not token or '\n' in token or '\r' in token:
    raise ValueError('部署令牌为空或格式无效')
with open(sys.argv[2], 'w') as f:
    f.write('Authorization: Bearer ' + token + '\n')
PY
  then
    echo "==> 无法读取部署令牌，拒绝部署；将有效管理员登录令牌保存到 $TOKEN_FILE（0600）" >&2
    exit 1
  fi
  HOST="${LISTEN/0.0.0.0/127.0.0.1}"
  HOST="${HOST/\[::\]/127.0.0.1}"
  if [[ "$HOST" == :* ]]; then HOST="127.0.0.1$HOST"; fi
  if ! ACTIVE=$(curl -fsS -m 5 -H "@$DEPLOY_BUILD_DIR/auth-header" "http://$HOST/api/games/active"); then
    echo "==> 对局查询失败（网络/HTTP/令牌错误），拒绝部署；确认要跳过检查用 --force" >&2
    exit 1
  fi
  if ! PLAYING=$(printf '%s' "$ACTIVE" | python3 -c '
import json, sys
data = json.load(sys.stdin)
if not isinstance(data, dict) or type(data.get("playing")) is not int or data["playing"] < 0:
    raise ValueError("缺少有效的 playing 数量")
print(data["playing"])
'); then
    echo "==> 对局状态响应无效，拒绝部署；确认要跳过检查用 --force" >&2
    exit 1
  fi
  if [[ "$PLAYING" != "0" ]]; then
    echo "==> 有 $PLAYING 局进行中，拒绝重启（会清空房间）。确认要重启用：scripts/deploy.sh --force" >&2
    exit 1
  fi
  echo "==> 没有进行中的对局，可以重启"
fi

mkdir -p bin
install -m 755 "$DEPLOY_BUILD_DIR/mineagent" bin/.mineagent.new
mv -f bin/.mineagent.new bin/mineagent
echo "==> 重启 mineagent"
sudo systemctl restart mineagent
sleep 2
systemctl is-active mineagent
echo "==> 完成：$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8766/) /"
