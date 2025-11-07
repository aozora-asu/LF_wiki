package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/sergi/go-diff/diffmatchpatch"
)

const siteTitle = "社内マニュアル"

var (
	errNoChanges = errors.New("no changes to commit")
	errNoRepo    = errors.New("git repository not available")
)

type app struct {
	projectRoot string
	manualRoot  string
	topRelFile  string
	topGitPath  string
	pages       map[string]pageMeta
	toc         []tocSection
	tmpl        *template.Template
	repo        *git.Repository
}

type historyEntry struct {
	Label     string
	Link      string
	Timestamp string
	Hash      string
	Active    bool
}

type manualPage struct {
	Title     string
	Content   template.HTML
	UpdatedAt time.Time
}

type flashMessage struct {
	Type    string
	Message string
}

type pageView struct {
	Mode             string
	SiteTitle        string
	PageTitle        string
	Content          template.HTML
	UpdatedAt        string
	History          []historyEntry
	Flash            *flashMessage
	EditContent      string
	EditAuthor       string
	EditMessage      string
	DiffTitle        string
	DiffBaseLabel    string
	DiffCompareLabel string
	DiffHTML         template.HTML
	DiffIsEmpty      bool
	TOC              []tocSection
	CanEdit          bool
}

type tocSection struct {
	Title string
	Pages []tocEntry
}

type tocEntry struct {
	Title    string
	Slug     string
	Href     string
	Children []tocEntry
}

type pageMeta struct {
	Title   string
	RelFile string
	GitPath string
}

type indexFile struct {
	Title       string          `json:"title" yaml:"title"`
	Description string          `json:"description" yaml:"description"`
	Categories  []indexCategory `json:"categories" yaml:"categories"`
}

type indexCategory struct {
	ID    string      `json:"id" yaml:"id"`
	Title string      `json:"title" yaml:"title"`
	Pages []indexPage `json:"pages" yaml:"pages"`
}

type indexPage struct {
	Slug     string      `json:"slug" yaml:"slug"`
	Title    string      `json:"title" yaml:"title"`
	File     string      `json:"file" yaml:"file"`
	Children []indexPage `json:"children" yaml:"children"`
}

func main() {
	manualRoot, err := findManualRoot()
	if err != nil {
		log.Fatalf("マニュアルディレクトリが見つかりません: %v", err)
	}

	projectRoot := filepath.Dir(manualRoot)

	tmpl, err := template.ParseFiles(filepath.Join(projectRoot, "web", "templates", "page.html"))
	if err != nil {
		log.Fatalf("テンプレートの読み込みに失敗しました: %v", err)
	}

	repo, err := git.PlainOpenWithOptions(projectRoot, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		if errors.Is(err, git.ErrRepositoryNotExists) {
			repo, err = git.PlainInit(projectRoot, false)
			if err != nil {
				log.Printf("Git リポジトリの初期化に失敗: %v", err)
			} else {
				log.Printf("Git リポジトリを初期化しました: %s", projectRoot)
			}
		} else {
			log.Printf("Git リポジトリを開けません: %v", err)
		}
	}

	pageMap, toc, err := loadManualIndex(projectRoot, manualRoot)
	if err != nil {
		log.Fatalf("index.yaml の読み込みに失敗しました: %v", err)
	}

	topMeta, ok := pageMap["top"]
	if !ok {
		log.Fatalf("index.yaml にトップページ (slug: top) が定義されていません")
	}

	app := &app{
		projectRoot: projectRoot,
		manualRoot:  manualRoot,
		topRelFile:  topMeta.RelFile,
		topGitPath:  topMeta.GitPath,
		pages:       pageMap,
		toc:         toc,
		tmpl:        tmpl,
		repo:        repo,
	}

	mux := http.NewServeMux()
	staticDir := http.Dir(filepath.Join(projectRoot, "web", "static"))
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(staticDir)))
	mux.HandleFunc("/", app.handleManual)
	mux.HandleFunc("/pages/", app.handlePage)
	mux.HandleFunc("/edit", app.handleEdit)
	mux.HandleFunc("/diff", app.handleDiff)

	addr := ":8080"
	log.Printf("マニュアルを http://localhost%s/ で提供中…", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("サーバ起動に失敗しました: %v", err)
	}
}

func (a *app) handleManual(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	commitHash := strings.TrimSpace(r.URL.Query().Get("commit"))

	page, err := a.loadManualPage(a.topRelFile, a.topGitPath, commitHash)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, plumbing.ErrObjectNotFound) {
			http.Error(w, "指定の履歴が見つかりません", http.StatusNotFound)
			return
		}
		log.Printf("マニュアルの読み込みに失敗しました: %v", err)
		http.Error(w, "マニュアルの読み込みに失敗しました", http.StatusInternalServerError)
		return
	}

	view := pageView{
		Mode:      "view",
		SiteTitle: siteTitle,
		PageTitle: page.Title,
		Content:   page.Content,
		UpdatedAt: page.UpdatedAt.Format("2006-01-02 15:04"),
		History:   a.buildHistory(commitHash),
		TOC:       a.toc,
		CanEdit:   true,
	}

	if r.URL.Query().Get("saved") == "1" {
		view.Flash = &flashMessage{
			Type:    "success",
			Message: "マニュアルを保存し、履歴に記録しました。",
		}
	}

	a.render(w, view)
}

func (a *app) handlePage(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/pages/") {
		http.NotFound(w, r)
		return
	}
	slug := strings.Trim(strings.TrimPrefix(r.URL.Path, "/pages/"), "/")
	if slug == "" {
		http.NotFound(w, r)
		return
	}
	if slug == "top" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	meta, ok := a.pages[slug]
	if !ok {
		http.NotFound(w, r)
		return
	}

	page, err := a.loadManualPage(meta.RelFile, meta.GitPath, "")
	if err != nil {
		log.Printf("ページ %s の読み込みに失敗しました: %v", slug, err)
		http.Error(w, "ページを読み込めませんでした", http.StatusInternalServerError)
		return
	}

	view := pageView{
		Mode:      "page",
		SiteTitle: siteTitle,
		PageTitle: meta.Title,
		Content:   page.Content,
		UpdatedAt: page.UpdatedAt.Format("2006-01-02 15:04"),
		TOC:       a.toc,
	}

	a.render(w, view)
}

func (a *app) handleEdit(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		content, err := os.ReadFile(a.manualAbsPath(a.topRelFile))
		if err != nil {
			log.Printf("マニュアルの読み込みに失敗しました: %v", err)
			http.Error(w, "マニュアルを読み込めませんでした", http.StatusInternalServerError)
			return
		}

		view := pageView{
			Mode:        "edit",
			SiteTitle:   siteTitle,
			PageTitle:   "トップページを編集",
			History:     a.buildHistory(""),
			TOC:         a.toc,
			EditContent: string(content),
			EditAuthor:  "マニュアル編集者",
		}

		a.render(w, view)

	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "フォームの解析に失敗しました", http.StatusBadRequest)
			return
		}

		content := strings.TrimRight(r.PostFormValue("content"), "\r\n")
		author := strings.TrimSpace(r.PostFormValue("author"))
		message := strings.TrimSpace(r.PostFormValue("message"))

		if content == "" {
			a.render(w, pageView{
				Mode:        "edit",
				SiteTitle:   siteTitle,
				PageTitle:   "トップページを編集",
				History:     a.buildHistory(""),
				EditContent: "",
				EditAuthor:  author,
				EditMessage: message,
				Flash: &flashMessage{
					Type:    "error",
					Message: "内容が空のため保存できません。",
				},
			})
			return
		}

		if author == "" {
			author = "マニュアル編集者"
		}
		if message == "" {
			message = "マニュアル更新"
		}

		filePath := a.manualAbsPath(a.topRelFile)
		if err := os.WriteFile(filePath, []byte(content+"\n"), 0o644); err != nil {
			log.Printf("マニュアルの保存に失敗しました: %v", err)
			a.render(w, pageView{
				Mode:        "edit",
				SiteTitle:   siteTitle,
				PageTitle:   "トップページを編集",
				History:     a.buildHistory(""),
				TOC:         a.toc,
				EditContent: content,
				EditAuthor:  author,
				EditMessage: message,
				Flash: &flashMessage{
					Type:    "error",
					Message: "ファイルの保存に失敗しました。",
				},
			})
			return
		}

		if err := a.commitManual(author, message); err != nil {
			log.Printf("コミット処理に失敗しました: %v", err)
			var note string
			switch {
			case errors.Is(err, errNoRepo):
				note = "Git が設定されていないため履歴に残せませんでした。`git init` を実行してから再度お試しください。"
			case errors.Is(err, errNoChanges):
				note = "内容に変更がないため、履歴は追加されませんでした。"
			default:
				note = "履歴への記録に失敗しました。Git の設定を確認してください。"
			}
			a.render(w, pageView{
				Mode:        "edit",
				SiteTitle:   siteTitle,
				PageTitle:   "トップページを編集",
				History:     a.buildHistory(""),
				TOC:         a.toc,
				EditContent: content,
				EditAuthor:  author,
				EditMessage: message,
				Flash: &flashMessage{
					Type:    "error",
					Message: note,
				},
			})
			return
		}

		http.Redirect(w, r, "/?saved=1", http.StatusSeeOther)

	default:
		http.Error(w, "許可されていないメソッドです", http.StatusMethodNotAllowed)
	}
}

func (a *app) handleDiff(w http.ResponseWriter, r *http.Request) {
	commitHash := strings.TrimSpace(r.URL.Query().Get("commit"))

	if a.repo == nil {
		http.Error(w, "差分を表示するには Git が必要です。", http.StatusServiceUnavailable)
		return
	}

	var (
		baseContent    []byte
		compareContent []byte
		baseLabel      string
		compareLabel   string
		diffTitle      string
		activeCommit   string
		err            error
	)

	workingPath := a.manualAbsPath(a.topRelFile)
	compareContent, err = os.ReadFile(workingPath)
	if err != nil {
		log.Printf("作業コピーの読み込みに失敗しました: %v", err)
		http.Error(w, "作業コピーを読み込めませんでした", http.StatusInternalServerError)
		return
	}

	if commitHash == "" {
		headRef, err := a.repo.Head()
		if err != nil {
			view := pageView{
				Mode:             "diff",
				SiteTitle:        siteTitle,
				PageTitle:        "差分ビュー",
				History:          a.buildHistory(""),
				DiffTitle:        "差分はまだありません",
				DiffBaseLabel:    "まだコミットがありません",
				DiffCompareLabel: "最新 (作業コピー)",
				DiffIsEmpty:      true,
				Flash: &flashMessage{
					Type:    "error",
					Message: "保存済みの履歴がまだないため、差分を表示できません。",
				},
			}
			a.render(w, view)
			return
		}
		commit, err := a.repo.CommitObject(headRef.Hash())
		if err != nil {
			http.Error(w, "履歴の読み込みに失敗しました", http.StatusInternalServerError)
			return
		}

		file, err := commit.File(a.topGitPath)
		if err != nil {
			http.Error(w, "比較対象のファイルが見つかりません", http.StatusNotFound)
			return
		}
		reader, err := file.Reader()
		if err != nil {
			http.Error(w, "履歴の読み込みに失敗しました", http.StatusInternalServerError)
			return
		}
		defer reader.Close()

		baseContent, err = io.ReadAll(reader)
		if err != nil {
			http.Error(w, "履歴の読み込みに失敗しました", http.StatusInternalServerError)
			return
		}

		baseLabel = fmt.Sprintf("最新コミット (%s)", commit.Author.When.Format("2006-01-02 15:04"))
		compareLabel = "最新 (作業コピー)"
		diffTitle = "最新コミットと作業コピーの差分"
		activeCommit = ""
	} else {
		hash := plumbing.NewHash(commitHash)
		commit, err := a.repo.CommitObject(hash)
		if err != nil {
			http.Error(w, "指定の履歴が見つかりません", http.StatusNotFound)
			return
		}

		file, err := commit.File(a.topGitPath)
		if err != nil {
			http.Error(w, "履歴のファイルが見つかりません", http.StatusNotFound)
			return
		}
		reader, err := file.Reader()
		if err != nil {
			http.Error(w, "履歴のファイルが読み込めません", http.StatusInternalServerError)
			return
		}
		defer reader.Close()

		baseContent, err = io.ReadAll(reader)
		if err != nil {
			http.Error(w, "履歴のファイルが読み込めません", http.StatusInternalServerError)
			return
		}

		message := strings.Split(commit.Message, "\n")[0]
		if message == "" {
			message = "更新"
		}
		baseLabel = fmt.Sprintf("%s (%s)", message, commit.Author.When.Format("2006-01-02 15:04"))
		compareLabel = "最新 (作業コピー)"
		diffTitle = "選択した履歴と最新の差分"
		activeCommit = commitHash
	}

	diffHTML, empty := renderDiff(baseContent, compareContent)

	view := pageView{
		Mode:             "diff",
		SiteTitle:        siteTitle,
		PageTitle:        "差分ビュー",
		History:          a.buildHistory(activeCommit),
		DiffTitle:        diffTitle,
		DiffBaseLabel:    baseLabel,
		DiffCompareLabel: compareLabel,
		DiffHTML:         diffHTML,
		DiffIsEmpty:      empty,
		TOC:              a.toc,
	}

	a.render(w, view)
}

func (a *app) loadManualPage(relPath, gitPath, commitHash string) (manualPage, error) {
	normalized := filepath.ToSlash(relPath)
	if normalized == "" {
		return manualPage{}, fmt.Errorf("読み込むファイルパスが指定されていません")
	}

	if commitHash == "" {
		return loadManualFromFile(a.manualAbsPath(normalized))
	}
	if a.repo == nil {
		return manualPage{}, fmt.Errorf("履歴を参照するにはGitリポジトリが必要です")
	}
	if gitPath == "" {
		return manualPage{}, fmt.Errorf("履歴参照用のファイルパスが指定されていません")
	}

	hash := plumbing.NewHash(commitHash)
	commit, err := a.repo.CommitObject(hash)
	if err != nil {
		return manualPage{}, err
	}

	file, err := commit.File(gitPath)
	if err != nil {
		return manualPage{}, err
	}

	reader, err := file.Reader()
	if err != nil {
		return manualPage{}, err
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return manualPage{}, err
	}

	return manualPageFromMarkdown(data, commit.Author.When), nil
}

func (a *app) buildHistory(activeCommit string) []historyEntry {
	workingCopyTime := time.Now()
	if info, err := os.Stat(a.manualAbsPath(a.topRelFile)); err == nil {
		workingCopyTime = info.ModTime()
	}

	history := []historyEntry{
		{
			Label:     "最新 (作業コピー)",
			Link:      "/",
			Timestamp: workingCopyTime.Format("2006-01-02 15:04"),
			Active:    activeCommit == "",
			Hash:      "",
		},
	}

	if a.repo == nil {
		return history
	}

	iter, err := a.repo.Log(&git.LogOptions{FileName: stringPtr(a.topGitPath)})
	if err != nil {
		if !errors.Is(err, plumbing.ErrObjectNotFound) && !errors.Is(err, plumbing.ErrReferenceNotFound) {
			log.Printf("履歴を取得できませんでした: %v", err)
		}
		return history
	}
	defer iter.Close()

	count := 0
	err = iter.ForEach(func(commit *object.Commit) error {
		if count >= 30 {
			return storer.ErrStop
		}
		count++

		message := strings.Split(commit.Message, "\n")[0]
		if message == "" {
			message = "更新"
		}
		hash := commit.Hash.String()
		history = append(history, historyEntry{
			Label:     message,
			Link:      "/?commit=" + hash,
			Timestamp: commit.Author.When.Format("2006-01-02 15:04"),
			Hash:      hash,
			Active:    hash == activeCommit,
		})
		return nil
	})

	if err != nil && !errors.Is(err, storer.ErrStop) {
		log.Printf("履歴の走査に失敗しました: %v", err)
	}

	return history
}

func (a *app) commitManual(author, message string) error {
	if a.repo == nil {
		return errNoRepo
	}

	worktree, err := a.repo.Worktree()
	if err != nil {
		return err
	}

	status, err := worktree.Status()
	if err != nil {
		return err
	}
	if status.IsClean() {
		return errNoChanges
	}

	stageManual := func(path string) error {
		return worktree.AddWithOptions(&git.AddOptions{
			Path:       path,
			SkipStatus: true,
		})
	}

	stageErr := stageManual(a.topGitPath)
	if stageErr != nil {
		if alt := filepath.FromSlash(a.topGitPath); alt != a.topGitPath {
			if err := stageManual(alt); err == nil {
				stageErr = nil
			} else {
				stageErr = err
			}
		}
	}

	if stageErr != nil {
		if err := worktree.AddWithOptions(&git.AddOptions{
			Glob:       "manuals/**",
			SkipStatus: true,
		}); err != nil {
			if err := worktree.AddWithOptions(&git.AddOptions{All: true}); err != nil {
				return fmt.Errorf("ステージングに失敗しました: %w", stageErr)
			}
		}
	}

	now := time.Now()
	signature := &object.Signature{
		Name:  author,
		Email: makeAuthorEmail(author),
		When:  now,
	}

	_, err = worktree.Commit(message, &git.CommitOptions{
		Author:    signature,
		Committer: signature,
	})
	return err
}

func renderDiff(base, compare []byte) (template.HTML, bool) {
	if bytes.Equal(base, compare) {
		return "", true
	}

	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(string(base), string(compare), false)
	dmp.DiffCleanupSemantic(diffs)

	var b strings.Builder
	hasChanges := false

	for _, diff := range diffs {
		if diff.Text == "" {
			continue
		}
		lines := strings.Split(diff.Text, "\n")
		for i, line := range lines {
			// 末尾の改行で出来た空行は除外
			if i == len(lines)-1 && line == "" {
				continue
			}
			escaped := html.EscapeString(line)
			switch diff.Type {
			case diffmatchpatch.DiffInsert:
				hasChanges = true
				b.WriteString(`<div class="diff__line diff__line-add">+ ` + escaped + `</div>`)
			case diffmatchpatch.DiffDelete:
				hasChanges = true
				b.WriteString(`<div class="diff__line diff__line-del">- ` + escaped + `</div>`)
			default:
				b.WriteString(`<div class="diff__line diff__line-eq">&nbsp; ` + escaped + `</div>`)
			}
		}
	}

	return template.HTML(b.String()), !hasChanges
}

func loadManualFromFile(path string) (manualPage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manualPage{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return manualPage{}, err
	}
	return manualPageFromMarkdown(data, info.ModTime()), nil
}

func manualPageFromMarkdown(data []byte, updatedAt time.Time) manualPage {
	htmlBody := markdownToHTML(string(data))

	title := extractTitle(htmlBody)
	if title == "" {
		title = "トップページ"
	}

	return manualPage{
		Title:     title,
		Content:   htmlBody,
		UpdatedAt: updatedAt,
	}
}

func extractTitle(content template.HTML) string {
	re := regexp.MustCompile(`<h1>(.*?)</h1>`)
	match := re.FindStringSubmatch(string(content))
	if len(match) < 2 {
		return ""
	}
	return html.UnescapeString(match[1])
}

func markdownToHTML(md string) template.HTML {
	lines := strings.Split(md, "\n")
	var b strings.Builder

	writeParagraph := func(text string) {
		if text == "" {
			return
		}
		b.WriteString("<p>")
		b.WriteString(html.EscapeString(text))
		b.WriteString("</p>")
	}

	var (
		inUL bool
		inOL bool
	)

	closeLists := func() {
		if inUL {
			b.WriteString("</ul>")
			inUL = false
		}
		if inOL {
			b.WriteString("</ol>")
			inOL = false
		}
	}

	numbered := regexp.MustCompile(`^\d+\.\s+`)

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "### "):
			closeLists()
			b.WriteString("<h3>")
			b.WriteString(html.EscapeString(strings.TrimPrefix(line, "### ")))
			b.WriteString("</h3>")
		case strings.HasPrefix(line, "## "):
			closeLists()
			b.WriteString("<h2>")
			b.WriteString(html.EscapeString(strings.TrimPrefix(line, "## ")))
			b.WriteString("</h2>")
		case strings.HasPrefix(line, "# "):
			closeLists()
			b.WriteString("<h1>")
			b.WriteString(html.EscapeString(strings.TrimPrefix(line, "# ")))
			b.WriteString("</h1>")
		case strings.HasPrefix(line, "- "):
			if inOL {
				b.WriteString("</ol>")
				inOL = false
			}
			if !inUL {
				b.WriteString("<ul>")
				inUL = true
			}
			b.WriteString("<li>")
			b.WriteString(html.EscapeString(strings.TrimPrefix(line, "- ")))
			b.WriteString("</li>")
		case numbered.MatchString(line):
			if inUL {
				b.WriteString("</ul>")
				inUL = false
			}
			if !inOL {
				b.WriteString("<ol>")
				inOL = true
			}
			item := numbered.ReplaceAllString(line, "")
			b.WriteString("<li>")
			b.WriteString(html.EscapeString(item))
			b.WriteString("</li>")
		case line == "":
			closeLists()
		default:
			closeLists()
			writeParagraph(line)
		}
	}
	closeLists()

	return template.HTML(b.String())
}

func (a *app) render(w http.ResponseWriter, view pageView) {
	if err := a.tmpl.Execute(w, view); err != nil {
		log.Printf("テンプレート描画に失敗しました: %v", err)
		http.Error(w, "内部エラー", http.StatusInternalServerError)
	}
}

func findManualRoot() (string, error) {
	start, err := os.Getwd()
	if err != nil {
		return "", err
	}
	current := start
	for {
		if exists(filepath.Join(current, "manuals", "entries", "top.md")) {
			return filepath.Join(current, "manuals"), nil
		}
		next := filepath.Dir(current)
		if next == current {
			break
		}
		current = next
	}
	return "", errors.New("manuals ディレクトリが見つかりませんでした")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}

func stringPtr(s string) *string {
	return &s
}

func makeAuthorEmail(name string) string {
	if name == "" {
		return "manual@local"
	}
	normalized := strings.ToLower(name)
	normalized = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '@':
			return -1
		default:
			return '.'
		}
	}, normalized)
	normalized = strings.Trim(normalized, ".")
	if normalized == "" {
		normalized = "editor"
	}
	return normalized + "@manual.local"
}

func loadManualIndex(projectRoot, manualRoot string) (map[string]pageMeta, []tocSection, error) {
	indexPath := filepath.Join(manualRoot, "index.yaml")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, nil, err
	}

	var idx indexFile
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, nil, err
	}

	slugMap := make(map[string]pageMeta)
	toc := make([]tocSection, 0, len(idx.Categories))
	for _, cat := range idx.Categories {
		if len(cat.Pages) == 0 {
			continue
		}
		entries, err := convertIndexPages(cat.Pages, slugMap, projectRoot, manualRoot)
		if err != nil {
			return nil, nil, err
		}
		toc = append(toc, tocSection{
			Title: cat.Title,
			Pages: entries,
		})
	}

	return slugMap, toc, nil
}

func convertIndexPages(pages []indexPage, slugMap map[string]pageMeta, projectRoot, manualRoot string) ([]tocEntry, error) {
	result := make([]tocEntry, 0, len(pages))
	for _, p := range pages {
		if p.Slug == "" {
			return nil, fmt.Errorf("index.yaml に slug が設定されていないページがあります")
		}
		if _, exists := slugMap[p.Slug]; exists {
			return nil, fmt.Errorf("slug %s が重複しています", p.Slug)
		}

		relFile := strings.TrimSpace(p.File)
		if relFile == "" {
			relFile = filepath.ToSlash(filepath.Join("entries", p.Slug+".md"))
		} else {
			relFile = filepath.ToSlash(relFile)
		}

		gitPath, err := computeGitPath(projectRoot, manualRoot, relFile)
		if err != nil {
			return nil, err
		}

		absPath := filepath.Join(manualRoot, filepath.FromSlash(relFile))
		if _, err := os.Stat(absPath); err != nil {
			log.Printf("警告: 目次で参照しているファイル %s の確認に失敗しました: %v", absPath, err)
		}

		slugMap[p.Slug] = pageMeta{
			Title:   p.Title,
			RelFile: relFile,
			GitPath: gitPath,
		}

		children, err := convertIndexPages(p.Children, slugMap, projectRoot, manualRoot)
		if err != nil {
			return nil, err
		}

		result = append(result, tocEntry{
			Title:    p.Title,
			Slug:     p.Slug,
			Href:     makePageLink(p.Slug),
			Children: children,
		})
	}
	return result, nil
}

func computeGitPath(projectRoot, manualRoot, relFile string) (string, error) {
	abs := filepath.Join(manualRoot, filepath.FromSlash(relFile))
	rel, err := filepath.Rel(projectRoot, abs)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func makePageLink(slug string) string {
	if slug == "top" {
		return "/"
	}
	return "/pages/" + slug
}

func (a *app) manualAbsPath(rel string) string {
	return filepath.Join(a.manualRoot, filepath.FromSlash(rel))
}
