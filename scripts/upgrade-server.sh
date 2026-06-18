#!/bin/sh
# Simpleprobe Server 升级脚本
# 用法: ./upgrade-server.sh [版本号]
# 示例: ./upgrade-server.sh         # 升级到最新版
#       ./upgrade-server.sh v1.1.0  # 升级到指定版本
#
# 从 systemd service 文件读取 ExecStart 路径，
# 停止服务 → 替换二进制 → 启动服务 → 清理下载文件

set -e

REPO="aaalzk/Simpleprobe"
SERVICE_NAME="simpleprobe-server"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  *)       echo "不支持的架构: $ARCH"; exit 1 ;;
esac

VERSION="${1:-latest}"
DOWNLOAD_URL="https://github.com/${REPO}/releases/${VERSION}/download/simpleprobe-server_linux_${ARCH}.tar.gz"

# --- 从 systemd service 文件读取 ExecStart 路径 ---
if [ -f "$SERVICE_FILE" ]; then
  BIN_PATH=$(grep -oP '^ExecStart=\K\S+' "$SERVICE_FILE" | head -1)
  if [ -z "$BIN_PATH" ]; then
    echo "警告: 无法从 $SERVICE_FILE 解析 ExecStart，使用默认路径"
    BIN_PATH="/usr/local/bin/simpleprobe-server"
  else
    echo ">>> 从 systemd service 读取到路径: $BIN_PATH"
  fi
else
  echo "警告: 未找到 $SERVICE_FILE，使用默认路径"
  BIN_PATH="/usr/local/bin/simpleprobe-server"
fi

# --- 下载 ---
echo ">>> 下载: $DOWNLOAD_URL"
TMPDIR=$(mktemp -d)
cd "$TMPDIR"

if command -v wget >/dev/null 2>&1; then
  wget -q --show-progress "$DOWNLOAD_URL" -O simpleprobe.tar.gz
elif command -v curl >/dev/null 2>&1; then
  curl -L --progress-bar "$DOWNLOAD_URL" -o simpleprobe.tar.gz
else
  echo "错误: 需要 wget 或 curl"
  exit 1
fi

# --- 解压 ---
tar xzf simpleprobe.tar.gz

# --- 停止服务 ---
if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
  echo ">>> 停止 $SERVICE_NAME 服务"
  sudo systemctl stop "$SERVICE_NAME"
fi

# --- 替换二进制 ---
echo ">>> 安装到 $BIN_PATH"
sudo mkdir -p "$(dirname "$BIN_PATH")"
sudo cp simpleprobe-server "$BIN_PATH"
sudo chmod +x "$BIN_PATH"

# --- 启动服务 ---
if [ -f "$SERVICE_FILE" ]; then
  echo ">>> 启动 $SERVICE_NAME 服务"
  sudo systemctl start "$SERVICE_NAME"
  sudo systemctl status "$SERVICE_NAME" --no-pager
else
  echo ">>> 手动启动: $BIN_PATH -c /path/to/server.yml"
fi

# --- 清理下载文件 ---
cd /
rm -rf "$TMPDIR"
echo ">>> 升级完成，临时文件已清理"