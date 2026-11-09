package api

import (
	"testing"
)

func TestSafeReturnPath(t *testing.T) {
	for _, raw := range []string{
		"", "https://evil.test", "//evil.test", "/\\evil.test", "/%2fevil.test",
		"/%5cevil.test", "/projects\r\nX-Test: value", "/%0d%0aevil", "/api/auth/oauth2/team/start",
		"/login?redirect=/projects", "javascript:alert(1)", "/%",
		"/projects/../api/auth/oauth2/team/start", "/%6cogin", "/%00", "/api",
	} {
		if got := safeReturnPath(raw); got != "/projects" {
			t.Errorf("危险回跳 %q 被接受为 %q", raw, got)
		}
	}
	path := "/projects/collie?branch=feature%2Fdocs&path=docs%2Fstart.md#标题"
	if got := safeReturnPath(path); got != path {
		t.Fatalf("有效文档地址未保留: %q", got)
	}
}
