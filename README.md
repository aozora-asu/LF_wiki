# LFWiki プロジェクトローカル環境

## セットアップ

```
mkdir -p .sdk .gopath .gocache build src
# 例: バージョンは必要に応じて調整してください
curl -LO https://go.dev/dl/go1.25.4.darwin-arm64.tar.gz
tar -xzf go1.25.4.darwin-arm64.tar.gz
mv go .sdk/go
rm go1.25.4.darwin-arm64.tar.gz
```

## ビルド例

```
# Macバイナリ
go build -o ../../build/wiki-mac .

# Windowsバイナリ（CGO無効でクロスビルド）
GOOS=windows GOARCH=amd64 go build -o ../../build/wiki.exe .
```

## マニュアルトップページの起動

```
cd src
source ../env.sh        # GOROOT/GOPATH/GOCACHEをプロジェクト内に閉じ込める
go run .
```

ブラウザで `http://localhost:8080/` を開くと、マニュアルのトップページが表示されます。

トップでは更新履歴が先頭に並び、各項目をクリックするとその時点でのトップページを閲覧できます（Gitのコミット履歴が存在する場合）。

## 目次とページ構成

- `manuals/index.yaml` にカテゴリ・ページの目次構造を定義します（JSON表記ですが YAML として扱っています）。
- 目次に登録された各ページは `manuals/entries/` 以下の Markdown ファイルに対応し、URL は `http://localhost:8080/pages/<slug>` です。
- 例: 出稿簿ガイドは `/pages/shukkobo`、曜日別の月曜ページは `/pages/weekday-monday`。
- トップページは `/` 固定で、ここから目次全体を確認できます。

## GUIでの編集と履歴の残し方

1. サーバーを起動した状態でトップページ右上の「このページを編集」を押します。
2. 本文をMarkdownで編集し、記録する名前と更新メモを入力して「保存して履歴に記録」を選択します。
3. 変更内容が `manuals/entries/top.md` に保存され、Gitコミットとして履歴に追加されます（`git init` 済みであることが前提）。

## 差分の確認

- 「更新履歴」の各項目にある「差分を見る」から、選択した履歴と現在の内容の差分をハイライト表示できます。
- 「未コミット差分」は最新コミットと作業コピーの差分を表示します。

### UIアセットの構成

- `web/templates/page.html` … HTMLテンプレート
- `web/static/style.css` … 表示スタイル
- `web/static/app.js` … 振る舞い（必要に応じて拡張）
