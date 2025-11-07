#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<USAGE
Usage: manuals/new_page.sh <slug> "<Title>" [entries/<dir>/<file>.md]

Examples:
  manuals/new_page.sh desk-overview "デスク情報" entries/desk/overview.md
  manuals/new_page.sh day-temp "臨時ページ"
USAGE
}

if [ $# -lt 2 ]; then
  usage >&2
  exit 1
fi

slug="$1"
shift

title="$1"
shift || true

rel_path="manuals/entries/${slug}.md"
if [ $# -gt 0 ]; then
  rel_path="manuals/$(echo "$1" | sed 's#^manuals/##')"
fi

abs_path="${rel_path}"
mkdir -p "$(dirname "$abs_path")"

if [ -e "$abs_path" ]; then
  echo "Error: ${abs_path} already exists." >&2
  exit 1
fi

cat <<CONTENT > "$abs_path"
# ${title}

ここに ${title} の本文を書いてください。必要に応じて手順やチェックリストを追加しましょう。
CONTENT

cat <<SUMMARY
Created ${abs_path}
- slug : ${slug}
- title: ${title}

次の手順:
1. manuals/index.yaml に slug "${slug}" を追加して表示順を定義します。
2. サーバーを再起動してブラウザで確認します。
SUMMARY
