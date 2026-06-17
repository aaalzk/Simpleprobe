#!/bin/sh
# Simpleprobe Agent 升级脚本
# 用法: ./upgrade-agent.sh [版本号]
# 示例: ./upgrade-agent.sh         # 升级到最新版
#       ./upgrade-agent.sh v1.0.6  # 升级到指定版本

set -e

REPO="aaalzk/Simpleprobe"
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  *)       echo "不支持的架构: $ARCH"; exit 1 ;;
esac

VERSION="${1:-latest}"
DOWNLOAD_URL="https://github.com/${REPO}/releases/${VERSION}/download/simpleprobe-agent_linux_${ARCH}.tar.gz"

echo ">>> 下载: $DOWNLOAD_URL"
TMPDIR=$(mktemp -d)
cd "$TMPDIR"

# 下载
if command -v wget >/dev/null 2>&1; then
  wget -q --show-progress "$DOWNLOAD_URL" -O simpleprobe.tar.gz
elif command -v curl >/dev/null 2>&1; then
  curl -L --progress-bar "$DOWNLOAD_URL" -o simpleprobe.tar.gz
else
  echo "错误: 需要 wget 或 curl"
  exit 1
fi

# 解压
tar xzf simpleprobe.tar.gz

# 查找当前二进制位置
BIN_PATH=$(command -v simpleprobe-agent 2>/dev/null || echo "/usr/local/bin/simpleprobe-agent")

# 停止服务
if systemctl is-active --quiet simpleprobe-agent 2>/dev/null; then
  echo ">>> 停止 simpleprobe-agent 服务"
  sudo systemctl stop simpleprobe-agent
fi

# 替换二进制
echo ">>> 安装到 $BIN_PATH"
sudo cp simpleprobe-agent "$BIN_PATH"
sudo chmod +x "$BIN_PATH"

# 启动服务
if systemctl is-enabled --quiet simpleprobe-agent 2>/dev/null; then
  echo ">>> 启动 simpleprobe-agent 服务"
  sudo systemctl start simpleprobe-agent
  sudo systemctl status simpleprobe-agent --no-pager
else
  echo ">>> 手动启动: $BIN_PATH -c /path/to/agent.yml"
fi

# 清理
cd /
rm -rf "$TMPDIR"
echo ">>> 升级完成"