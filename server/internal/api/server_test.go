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
