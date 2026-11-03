#!/bin/bash

current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -euo pipefail

# 清理旧产物
rm -rf dist
mkdir -p dist

LDFLAGS="-s -w"
FLAGS=(-trimpath -ldflags="$LDFLAGS")

# 依次构建 linux/amd64 和 linux/arm64
for arch in amd64 arm64; do
    echo "==> building linux/${arch}"
    GOOS=linux GOARCH=${arch} CGO_ENABLED=0 \
        go build "${FLAGS[@]}" -o "dist/steel-linux-${arch}" .
done

# 打印产物清单 + 大小,方便确认两个架构都构建成功
echo
echo "==> build artifacts:"
ls -lh dist/
