package api

import (
	"testing"
)

func TestNormalizeSSORedirectURI(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		want string
	}{
		{"new_site", "https://docs.test/api/auth/oauth2/pending/callback", "https://docs.test/api/auth/oauth2/team/callback"},
		{"origin", "https://docs.test", "https://docs.test/api/auth/oauth2/team/callback"},
		{"custom_origin", "https://public.test:8443/api/auth/oauth2/team/callback", "https://public.test:8443/api/auth/oauth2/team/callback"},
		{"custom_path", "https://public.test/gateway/oauth/callback", "https://public.test/gateway/oauth/callback"},
		{"encoded_path", "https://public.test/gateway%2Fdocs/callback", "https://public.test/gateway%2Fdocs/callback"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeSSORedirectURI(test.raw, "team")
			if err != nil || got != test.want {
				t.Fatalf("回调地址未按预期保留或填充: %q %v", got, err)
			}
		})
	}
	for _, raw := range []string{
		"", "/callback", "javascript:alert(1)", "https://user:secret@docs.test/callback",
		"https://docs.test/callback?code=value", "https://docs.test/callback?",
		"https://docs.test/callback#fragment",
		"https://docs.test/api/auth/oauth2/collie/callback",
		"https://docs.test/api/auth/collie/callback",
	} {
		if _, err := normalizeSSORedirectURI(raw, "team"); err == nil {
			t.Errorf("无效回调地址未被拒绝: %q", raw)
		}
	}
}

func TestReplaceStandardAuthCallback(t *testing.T) {
	if got := replaceStandardAuthCallback(
		"https://docs.test/api/auth/oauth2/old/callback", "collie",
	); got != "https://docs.test/api/auth/oauth2/collie/callback" {
		t.Fatalf("标准回调路径未随认证标识更新: %s", got)
	}
	custom := "https://docs.test/gateway/oauth/callback"
	if got := replaceStandardAuthCallback(custom, "collie"); got != custom {
		t.Fatalf("自定义回调路径不应被修改: %s", got)
	}
}

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
