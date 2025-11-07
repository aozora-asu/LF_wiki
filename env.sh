# env.sh --- プロジェクトローカル Go 環境
# 使い方: `source env.sh` で有効化、`devenv_off` で解除

# スクリプトディレクトリ (Bash / Zsh / その他) を解決
if [ -n "${BASH_SOURCE:-}" ]; then
  _devroot_source="${BASH_SOURCE[0]}"
else
  _devroot_source="$0"
fi

if [ -n "${ZSH_VERSION:-}" ] && [ "$_devroot_source" = "$0" ]; then
  # zsh で source された場合は $0 がシェル名になるので、呼び出し元の pwd を使用
  _devroot_dir="$PWD"
else
  _devroot_dir="$(cd "$(dirname "$_devroot_source")" && pwd)"
fi

export DEVROOT="$_devroot_dir"

# .sdk/go を含むディレクトリを探索（source 位置に依存しないように）
if [ ! -x "$DEVROOT/.sdk/go/bin/go" ]; then
  _probe="$DEVROOT"
  while [ "$_probe" != "/" ]; do
    if [ -x "$_probe/.sdk/go/bin/go" ]; then
      DEVROOT="$_probe"
      break
    fi
    _probe="$(dirname "$_probe")"
  done
  unset _probe
fi

unset _devroot_source _devroot_dir

# Go 本体（GOROOT）: プロジェクト内
export GOROOT="$DEVROOT/.sdk/go"
export PATH="$GOROOT/bin:$PATH"

# GOPATH と各種キャッシュを全部ローカルへ
export GOPATH="$DEVROOT/.gopath"
export GOMODCACHE="$GOPATH/pkg/mod"
export GOCACHE="$DEVROOT/.gocache"

# 余計な自動ツールチェーンDLを防ぐ（ローカルSDK固定）
export GOTOOLCHAIN=local

# （任意）CGO無効でクロスビルドを安定させる
export CGO_ENABLED=0

# プロンプト表示（任意）
export PS1="(go-local) $PS1"

# 簡易確認
go version

devenv_off () {
  # 元のシェルに戻す: 変数類をアンセット
  unset GOROOT GOPATH GOMODCACHE GOCACHE GOTOOLCHAIN CGO_ENABLED
  # PATH から GOROOT/bin を除去
  export PATH="$(echo "$PATH" | awk -v rm="$DEVROOT/.sdk/go/bin" -v RS=: -v ORS=: '$0!=rm' | sed 's/:$//')"
  unset DEVROOT
  # プロンプト復旧（簡易）
  export PS1="$(echo "$PS1" | sed 's/^(go-local) //')"
  echo "Go local env deactivated."
}
