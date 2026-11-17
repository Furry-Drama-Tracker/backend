#!/bin/sh
# neo-server 启动脚本（nohup 备选，未使用 systemd 时）。
# 用法：sh deploy/run.sh [listen]
# 默认只监听回环（对外访问请用 nginx/caddy 反代，见 deploy/nginx.conf.example）；
# 可传 unix:/run/furry-drama-be-neo.sock。
set -e

BIN="$(dirname "$0")/../bin/furry-drama-be-neo"
CONFIG="${CONFIG:-/etc/furry-drama-tracker/backend.ini}"
LISTEN="${1:-tcp:127.0.0.1:5000}"
LOG="/var/log/furry-drama-be-neo.log"

if [ ! -x "$BIN" ]; then
  echo "错误: 二进制不存在 $BIN（先执行 go build -o bin/furry-drama-be-neo ./cmd/server）" >&2
  exit 1
fi

# 前台运行（配合外部日志收集）；需要后台可改为：
#   nohup "$BIN" --config="$CONFIG" --listen="$LISTEN" >>"$LOG" 2>&1 &
exec "$BIN" --config="$CONFIG" --listen="$LISTEN"
