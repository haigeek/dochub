package docs

import (
	"os"
	"path/filepath"
	"testing"
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

func TestIndexMarkdown(t *testing.T) {
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
	docs, err := indexMarkdown(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].Title != "项目说明" {
		t.Fatalf("文档索引结果不正确: %#v", docs)
	}
}

func TestIndexMarkdownKeepsRepositoryPaths(t *testing.T) {
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
	documents, err := indexMarkdown(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 2 || documents[0].Path != "design/README.md" || documents[1].Path != "docs/README.md" {
		t.Fatalf("多目录索引应保留完整仓库路径: %#v", documents)
	}
}
