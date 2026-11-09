package api

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"dochub/internal/store"
)

func TestAttachmentDisposition(t *testing.T) {
	header := attachmentDisposition("guide/使用说明.md")
	if !strings.Contains(header, "filename*=UTF-8''") {
		t.Fatalf("缺少 UTF-8 文件名: %s", header)
	}
	encoded := url.PathEscape("使用说明.md")
	if !strings.Contains(header, encoded) {
		t.Fatalf("中文文件名编码不正确: %s", header)
	}
	if strings.ContainsAny(header, "\r\n") {
		t.Fatal("下载文件名不应包含换行符")
	}
}

func TestSafeAttachmentFilename(t *testing.T) {
	if got := safeAttachmentFilename("../a\r\nb.md"); got != ".._a__b.md" {
		t.Fatalf("文件名清理结果错误: %q", got)
	}
	if got := safeAttachmentFilename("  "); got != "document.md" {
		t.Fatalf("空文件名应有默认值: %q", got)
	}
}

func TestDocumentJSONFields(t *testing.T) {
	payload, err := json.Marshal(store.DocFile{Path: "README.md", Markdown: "# 标题"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"path"`, `"markdown"`} {
		if !strings.Contains(string(payload), field) {
			t.Fatalf("文档 JSON 缺少字段 %s: %s", field, payload)
		}
	}
	if strings.Contains(string(payload), `"Markdown"`) {
		t.Fatalf("文档 JSON 不应输出大写字段: %s", payload)
	}
}

func TestVisibleDocRoots(t *testing.T) {
	documents := []store.Document{
		{Path: "docs/guide/start.md", Title: "开始"},
		{Path: "design/ui.md", Title: "设计"},
	}
	roots := visibleDocRoots([]string{"docs", "design", "api"}, documents, false)
	if len(roots) != 2 || roots[0] != "docs" || roots[1] != "design" {
		t.Fatalf("受限目录计算结果错误: %#v", roots)
	}
	legacy := visibleDocRoots([]string{"docs"}, []store.Document{{Path: "guide/start.md"}}, false)
	if len(legacy) != 1 || legacy[0] != "docs" {
		t.Fatalf("旧版单目录兼容失败: %#v", legacy)
	}
}

func TestNormalizeProjectAccess(t *testing.T) {
	access := normalizeProjectAccess(store.ProjectAccess{
		Documents: []store.DocumentAccess{{Path: "docs/start.md"}},
	})
	if access.ProjectUsers == nil {
		t.Fatal("项目级用户列表应序列化为空数组")
	}
	if access.ProjectOrganizations == nil {
		t.Fatal("项目级组织列表应序列化为空数组")
	}
	if access.Documents[0].Users == nil {
		t.Fatal("文档级用户列表应序列化为空数组")
	}
	if access.Documents[0].Organizations == nil {
		t.Fatal("文档级组织列表应序列化为空数组")
	}
}
