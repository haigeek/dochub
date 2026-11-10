package docparse

import (
	"strings"
	"testing"
)

func TestParseHTML(t *testing.T) {
	source := `<!doctype html>
<html>
<head>
  <title>部署手册</title>
  <link rel="stylesheet" href="assets/manual.css">
  <style>.hero { background: url("images/hero.png"); }</style>
</head>
<body>
  <h1>备用标题</h1>
  <p>生产环境部署说明</p>
  <script>console.log("不进入检索")</script>
  <img src="./images/overview.png" srcset="images/overview-2x.png 2x">
  <a href="https://example.com">外部链接</a>
</body>
</html>`
	metadata := ParseHTML(source, "docs/guide/deploy.html")
	if metadata.Title != "部署手册" {
		t.Fatalf("HTML 标题错误: %q", metadata.Title)
	}
	if metadata.PlainText != "备用标题 生产环境部署说明 外部链接" {
		t.Fatalf("HTML 纯文本错误: %q", metadata.PlainText)
	}
	expectedAssets := []string{
		"docs/guide/assets/manual.css",
		"docs/guide/images/hero.png",
		"docs/guide/images/overview.png",
		"docs/guide/images/overview-2x.png",
	}
	if strings.Join(metadata.Assets, ",") != strings.Join(expectedAssets, ",") {
		t.Fatalf("HTML 资源提取错误: %#v", metadata.Assets)
	}
}

func TestPrepareHTMLInjectsControlledBase(t *testing.T) {
	source := `<!doctype html><html><head><base href="https://evil.example/"><meta http-equiv="refresh" content="0;url=https://evil.example"><link rel="stylesheet" href="style.css"></head><body>正文</body></html>`
	prepared, err := PrepareHTML(source, "/api/projects/p1/branch-assets/b1/docs/")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.ToLower(prepared), "<base") != 1 ||
		!strings.Contains(prepared, `href="/api/projects/p1/branch-assets/b1/docs/"`) {
		t.Fatalf("受控 base 注入错误: %s", prepared)
	}
	if strings.Contains(strings.ToLower(prepared), "evil.example") ||
		strings.Contains(strings.ToLower(prepared), `http-equiv="refresh"`) {
		t.Fatalf("危险 HTML 标签未清理: %s", prepared)
	}
}

func TestExtractCSSAssets(t *testing.T) {
	source := `@import "theme/base.css";
.hero { background: url('./images/hero.png'); }
.icon { mask: url("../icons/mask.svg#shape"); }`
	assets := ExtractCSSAssets(source, "docs/styles/site.css")
	expected := []string{
		"docs/styles/images/hero.png",
		"docs/icons/mask.svg",
		"docs/styles/theme/base.css",
	}
	if strings.Join(assets, ",") != strings.Join(expected, ",") {
		t.Fatalf("CSS 资源提取错误: %#v", assets)
	}
}
