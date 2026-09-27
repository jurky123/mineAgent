#!/usr/bin/env bash
# 安全部署：先构建，再检查有没有进行中的对局；有人在下棋就拒绝重启（除非 --force）。
# 用法：scripts/deploy.sh [--force]
set -euo pipefail
cd "$(dirname "$0")/.."

CONFIG="${CONFIG:-config.json}"
LISTEN=$(python3 -c "import json;print(json.load(open('$CONFIG')).get('web',{}).get('listen',''))" 2>/dev/null || true)
# 管理员令牌默认取 config.json 里 web.dataDir 下的 deploy-token.txt（可用 TOKEN_FILE 覆盖）
DATA_DIR=$(python3 -c "import json;print(json.load(open('$CONFIG')).get('web',{}).get('dataDir','data/webui'))" 2>/dev/null || echo "data/webui")
TOKEN_FILE="${TOKEN_FILE:-$DATA_DIR/deploy-token.txt}"

echo "==> 构建"
make build

if [[ "${1:-}" == "--force" ]]; then
  echo "==> --force：跳过对局检查"
else
  if [[ -z "$LISTEN" || ! -f "$TOKEN_FILE" ]]; then
    echo "==> 提示：未配置 $TOKEN_FILE（管理员登录令牌），跳过对局检查"
    echo "    创建方法：在门户登录后把 data/webui/tokens.json 里自己的令牌写入该文件（0600）"
  else
    HOST="${LISTEN/0.0.0.0/127.0.0.1}"; HOST="${HOST/\[::\]/127.0.0.1}"
    ACTIVE=$(curl -s -m 5 -H "Authorization: Bearer $(cat "$TOKEN_FILE")" "http://$HOST/api/games/active" || echo '')
    PLAYING=$(printf '%s' "$ACTIVE" | python3 -c "import json,sys;print(json.load(sys.stdin).get('playing',0))" 2>/dev/null || echo "?")
    if [[ "$PLAYING" == "?" ]]; then
      echo "==> 警告：查不到对局状态（接口未上线或令牌无效），继续部署"
    elif [[ "$PLAYING" != "0" ]]; then
      echo "==> 有 $PLAYING 局进行中，拒绝重启（会清空房间）。确认要重启用：scripts/deploy.sh --force"
      exit 1
    fi
    echo "==> 没有进行中的对局，可以重启"
  fi
fi

echo "==> 重启 mineagent"
sudo systemctl restart mineagent
sleep 2
systemctl is-active mineagent
echo "==> 完成：$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8766/) /"
