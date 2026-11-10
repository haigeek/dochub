package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dochub/internal/config"
	"dochub/internal/docs"
	"dochub/internal/store"
)

func TestPasswordChangeAndBasicAuth(t *testing.T) {
	db := oauthTestDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser(ctx, "basic-user", "old-password", "member")
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.CreateProject(ctx, "Basic project", "", "git@git.test:team/basic.git",
		[]string{"docs"}, user.ID, []string{"main"}, true)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{StorageDir: t.TempDir(), SessionTTL: time.Hour, GitHostAllowlist: []string{"git.test"}}
	service, err := docs.NewService(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := t.TempDir()
	snapshot, err = filepath.EvalSymlinks(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(snapshot, "docs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "docs", "asset.txt"), []byte("asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishBranchSync(ctx, project.ID, "main", "commit", snapshot, []store.DocFile{
		{Path: "docs/readme.md", Title: "Guide", Markdown: "# Guide\n[asset](asset.txt)", PlainText: "Guide"},
	}); err != nil {
		t.Fatal(err)
	}
	router := New(cfg, db, service).Router()
	currentCookie, currentCSRF := loginForTest(t, router, "basic-user", "old-password")
	otherCookie, _ := loginForTest(t, router, "basic-user", "old-password")
	assetPath := "/api/projects/" + project.ID + "/assets/docs/asset.txt?branch=main"
	request := func(method, path string, body []byte, cookie *http.Cookie, csrf string, basicUser, basicPassword string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if basicUser != "" {
			req.SetBasicAuth(basicUser, basicPassword)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}

	challenge := request(http.MethodGet, assetPath, nil, nil, "", "", "")
	if challenge.Code != http.StatusUnauthorized ||
		!strings.HasPrefix(challenge.Header().Get("WWW-Authenticate"), "Basic ") {
		t.Fatalf("资源应返回 Basic Auth 挑战: %d %s", challenge.Code, challenge.Header().Get("WWW-Authenticate"))
	}
	if response := request(http.MethodGet, assetPath, nil, nil, "", "basic-user", "old-password"); response.Code != http.StatusOK {
		t.Fatalf("Basic Auth 读取资源失败: %d %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, assetPath, nil, nil, "", "basic-user", "wrong-password"); response.Code != http.StatusUnauthorized ||
		!strings.HasPrefix(response.Header().Get("WWW-Authenticate"), "Basic ") {
		t.Fatalf("错误 Basic Auth 应返回 401: %d", response.Code)
	}
	if response := request(http.MethodPost, "/api/projects", []byte(`{}`), nil, "", "basic-user", "old-password"); response.Code != http.StatusUnauthorized {
		t.Fatalf("Basic Auth 不应允许写请求: %d", response.Code)
	}

	wrongBody, _ := json.Marshal(map[string]string{"currentPassword": "wrong", "newPassword": "new-password"})
	if response := request(http.MethodPut, "/api/auth/password", wrongBody, currentCookie, currentCSRF, "", ""); response.Code != http.StatusBadRequest {
		t.Fatalf("错误旧密码不应修改成功: %d", response.Code)
	}
	body, _ := json.Marshal(map[string]string{"currentPassword": "old-password", "newPassword": "new-password"})
	if response := request(http.MethodPut, "/api/auth/password", body, currentCookie, currentCSRF, "", ""); response.Code != http.StatusNoContent {
		t.Fatalf("修改密码失败: %d %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/auth/me", nil, otherCookie, "", "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("其他会话应失效: %d", response.Code)
	}
	me := request(http.MethodGet, "/api/auth/me", nil, currentCookie, "", "", "")
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"hasPassword":true`) {
		t.Fatalf("当前会话应继续有效: %d %s", me.Code, me.Body.String())
	}
	if response := request(http.MethodGet, assetPath, nil, nil, "", "basic-user", "new-password"); response.Code != http.StatusOK {
		t.Fatalf("新密码应可用于 Basic Auth: %d", response.Code)
	}
	if response := request(http.MethodGet, assetPath, nil, nil, "", "basic-user", "old-password"); response.Code != http.StatusUnauthorized {
		t.Fatalf("旧密码不应继续使用: %d", response.Code)
	}

	oauthUser, err := db.OAuthUser(ctx, "collie", "issuer", "subject", "sso-basic")
	if err != nil || oauthUser.HasPassword {
		t.Fatalf("SSO 用户初始不应有本地密码: %#v %v", oauthUser, err)
	}
	rawToken, rawCSRF := randomToken(32), randomToken(24)
	if err := db.CreateSession(ctx, oauthUser.ID, hashToken(rawToken), rawCSRF, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	ssoCookie := &http.Cookie{Name: sessionCookie, Value: rawToken}
	ssoBody, _ := json.Marshal(map[string]string{"currentPassword": "", "newPassword": "sso-password"})
	if response := request(http.MethodPut, "/api/auth/password", ssoBody, ssoCookie, rawCSRF, "", ""); response.Code != http.StatusNoContent {
		t.Fatalf("SSO 用户首次设置密码失败: %d %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, assetPath, nil, nil, "", "sso-basic", "sso-password"); response.Code != http.StatusOK {
		t.Fatalf("SSO 用户设置密码后应可使用 Basic Auth: %d", response.Code)
	}
}
