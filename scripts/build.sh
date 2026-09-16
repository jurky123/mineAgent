#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

make build
make plugin

if [[ "${1:-}" == "--install" ]]; then
  jar="$(ls -t paper-plugin/build/libs/MineAgent-*.jar | head -1)"
  cp "$jar" /home/ubuntu/minecraft/plugins/MineAgent.jar
  echo "installed $jar -> /home/ubuntu/minecraft/plugins/MineAgent.jar"
fi
