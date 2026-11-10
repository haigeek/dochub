package docs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dochub/internal/store"
)

func TestValidateRepoURL(t *testing.T) {
	allowlist := []string{"git.example.com", "gitlab.example.com:2222"}
	valid := []string{
		"git@git.example.com:team/docs.git",
		"ssh://git@git.example.com:2222/team/docs.git",
		"ssh://git@gitlab.example.com:2222/team/docs.git",
	}
	for _, value := range valid {
		if err := ValidateRepoURL(value, allowlist); err != nil {
			t.Fatalf("预期地址合法 %q: %v", value, err)
		}
	}
	invalid := []string{
		"https://git.example.com/team/docs.git",
		"https://gitlab.example.com:2222/team/docs.git",
		"git@evil.example.com:team/docs.git",
		"--upload-pack=evil git@git.example.com:a.git",
		"git@git.example.com:",
	}
	for _, value := range invalid {
		if err := ValidateRepoURL(value, allowlist); err == nil {
			t.Fatalf("预期地址非法: %q", value)
		}
	}
}

func TestValidatePaths(t *testing.T) {
	for _, value := range []string{"docs", "src/docs", "./docs"} {
		if err := ValidateDocRoot(value); err != nil {
			t.Fatalf("预期目录合法 %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "/docs", "../docs", "docs/../../secret"} {
		if err := ValidateDocRoot(value); err == nil {
			t.Fatalf("预期目录非法: %q", value)
		}
	}
	for _, value := range []string{"main", "feature/docs"} {
		if err := ValidateBranch(value); err != nil {
			t.Fatalf("预期分支合法 %q: %v", value, err)
		}
	}
	for _, value := range []string{"-main", "a..b", "bad branch", "x/"} {
		if err := ValidateBranch(value); err == nil {
			t.Fatalf("预期分支非法: %q", value)
		}
	}
}

func TestNormalizeDocRoots(t *testing.T) {
	roots, err := NormalizeDocRoots([]string{"./docs", "design/"})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || roots[0] != "docs" || roots[1] != "design" {
		t.Fatalf("文档目录规范化结果错误: %#v", roots)
	}
	for _, values := range [][]string{
		nil,
		{"docs", "docs"},
		{"docs", "docs/api"},
		{"/docs"},
		{"../docs"},
	} {
		if _, err := NormalizeDocRoots(values); err == nil {
			t.Fatalf("预期文档目录非法: %#v", values)
		}
	}
}

func TestCleanRelative(t *testing.T) {
	if value, err := cleanRelative("guide/../intro.md"); err != nil || value != "intro.md" {
		t.Fatalf("路径规范化错误: %q %v", value, err)
	}
	for _, value := range []string{"../secret", "guide/../../secret", "/absolute"} {
		if _, err := cleanRelative(value); err == nil {
			t.Fatalf("预期路径越界: %q", value)
		}
	}
}

func TestGitErrorUsesActionableRepositoryPermissionMessage(t *testing.T) {
	output := []byte(`remote: remote: ========================================
remote: The project you were looking for could not be found or you don't have permission to view it.
remote: fatal: Could not read from remote repository.
remote: Please make sure you have the correct access rights and the repository exists.`)
	err := gitError("克隆仓库", output, errors.New("exit status 128"))
	message := err.Error()
	if !strings.Contains(message, "Jenkinsadmin") ||
		!strings.Contains(message, "只读权限") ||
		strings.Contains(message, "remote:") {
		t.Fatalf("仓库权限错误提示不够简洁明确: %s", message)
	}
}

func TestIndexDocuments(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "guide"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# 项目说明\n\n正文"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "guide", "start.md"), []byte("无标题"), 0o644); err != nil {
		t.Fatal(err)
	}
	docs, err := indexDocuments(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].Title != "项目说明" {
		t.Fatalf("文档索引结果不正确: %#v", docs)
	}
}

func TestIndexDocumentsKeepsRepositoryPaths(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{
		filepath.Join("docs", "README.md"),
		filepath.Join("design", "README.md"),
	} {
		fullPath := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte("# "+file+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	documents, err := indexDocuments(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 2 || documents[0].Path != "design/README.md" || documents[1].Path != "docs/README.md" {
		t.Fatalf("多目录索引应保留完整仓库路径: %#v", documents)
	}
}

func TestCopyDocumentRootsSkipsMissingDirectories(t *testing.T) {
	worktree := t.TempDir()
	document := filepath.Join(worktree, "docs", "start.md")
	if err := os.MkdirAll(filepath.Dir(document), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(document, []byte("# 开始\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot := t.TempDir()
	available, err := copyDocumentRoots(
		worktree, snapshot, []string{"docs", "dev-docs"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(available) != 1 || available[0] != "docs" {
		t.Fatalf("缺失目录应被跳过，可用目录应保留: %#v", available)
	}
	if _, err := os.Stat(filepath.Join(snapshot, "docs", "start.md")); err != nil {
		t.Fatalf("存在的文档目录未复制: %v", err)
	}
	if _, err := os.Stat(filepath.Join(snapshot, "dev-docs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("缺失的文档目录不应创建: %v", err)
	}
}

func TestIndexDocumentsSupportsHTML(t *testing.T) {
	root := t.TempDir()
	source := `<!doctype html>
<html>
<head>
  <title>HTML 接入指南</title>
  <link rel="stylesheet" href="./assets/guide.css">
</head>
<body>
  <h1>备用标题</h1>
  <p>这里是正文内容。</p>
  <img src="assets/architecture.png" alt="架构图">
</body>
</html>`
	fullPath := filepath.Join(root, "docs", "guide.html")
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs", "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "docs", "assets", "guide.css"),
		[]byte(`@font-face { src: url("../fonts/inter.woff2"); }`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	documents, err := indexDocuments(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 1 {
		t.Fatalf("HTML 文档索引数量错误: %#v", documents)
	}
	document := documents[0]
	if document.Format != store.DocumentFormatHTML ||
		document.Title != "HTML 接入指南" ||
		document.HTML != source ||
		document.Markdown != "" ||
		document.PlainText != "备用标题 这里是正文内容。" {
		t.Fatalf("HTML 文档索引错误: %#v", document)
	}
	if len(document.Assets) != 3 ||
		document.Assets[0] != "docs/assets/guide.css" ||
		document.Assets[1] != "docs/assets/architecture.png" ||
		document.Assets[2] != "docs/fonts/inter.woff2" {
		t.Fatalf("HTML 资源索引错误: %#v", document.Assets)
	}
}
