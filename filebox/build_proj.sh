#!/bin/bash
# filebox/build_proj.sh
# 一键构建脚本：检查环境、编译并输出可执行文件

set -e  # 遇到错误立即退出

PROJECT_ROOT="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="$PROJECT_ROOT/bin"
BINARY="$BIN_DIR/filebox"

echo "=== filebox 构建脚本 ==="

# 检查 Go 是否安装
if ! command -v go &> /dev/null; then
    echo "错误: 未找到 Go 命令，请安装 Go 1.22+"
    exit 1
fi

# 进入 src 目录
cd "$PROJECT_ROOT/src"

echo "-> 下载依赖..."
go mod tidy

echo "-> 编译..."
mkdir -p "$BIN_DIR"
go build -o "$BINARY" .

echo "✅ 构建成功！二进制文件位于: $BINARY"
echo "运行方式: $BINARY  (或 ./bin/filebox)"
echo "注意: 需要保持 page/ 目录与二进制同级，或使用绝对路径访问静态文件。"
