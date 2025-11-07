#!/usr/bin/env bash
set -e
# ビルド成果物
rm -rf build
# Goキャッシュ類
rm -rf .gocache .gopath
# 依存モジュールを含む go のモジュールキャッシュは .gopath/pkg/mod 内にある
# SDKごと消す場合（Go本体も削除）
read -p "Also remove local Go SDK (.sdk/go)? [y/N] " yn
if [[ "$yn" =~ ^[Yy]$ ]]; then
  rm -rf .sdk/go
fi
echo "Clean done."