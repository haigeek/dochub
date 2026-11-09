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

func TestProjectVisibility(t *testing.T) {
	db := oauthTestDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner, err := db.CreateUser(ctx, "owner", "visibility-test-password", "member")
	if err != nil {
		t.Fatal(err)
	}
	member, err := db.CreateUser(ctx, "reader", "visibility-test-password", "member")
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.CreateProject(ctx, "Visibility", "git@git.test:team/visibility.git",
		[]string{"docs"}, owner.ID, []string{"main"}, false)
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
	ownerCookie, ownerCSRF := loginForTest(t, router, "owner", "visibility-test-password")
	memberCookie, memberCSRF := loginForTest(t, router, "reader", "visibility-test-password")
	base := "/api/projects/" + project.ID
	readPaths := []string{base, base + "/tree?branch=main", base + "/doc?branch=main&path=docs/readme.md",
		base + "/doc/download?branch=main&path=docs/readme.md", base + "/search?branch=main&q=Guide",
		base + "/assets/docs/asset.txt?branch=main"}
	request := func(method, path string, body []byte, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	assertRead := func(want int) {
		t.Helper()
		for _, path := range readPaths {
			if response := request(http.MethodGet, path, nil, memberCookie, ""); response.Code != want {
				t.Fatalf("%s: want %d, got %d: %s", path, want, response.Code, response.Body.String())
			}
		}
	}
	assertRead(http.StatusForbidden)
	setVisibility := func(value any, cookie *http.Cookie, csrf string, want int) {
		t.Helper()
		body := map[string]any{"name": project.Name, "repoUrl": project.RepoURL, "docRoots": project.DocRoots}
		if value != nil {
			body["isPublic"] = value
		}
		payload, _ := json.Marshal(body)
		if response := request(http.MethodPatch, base, payload, cookie, csrf); response.Code != want {
			t.Fatalf("update visibility: want %d, got %d: %s", want, response.Code, response.Body.String())
		}
	}
	setVisibility(true, memberCookie, memberCSRF, http.StatusForbidden)
	setVisibility(true, ownerCookie, ownerCSRF, http.StatusOK)
	assertRead(http.StatusOK)
	setVisibility(nil, ownerCookie, ownerCSRF, http.StatusOK)
	assertRead(http.StatusOK)
	list := request(http.MethodGet, "/api/projects", nil, memberCookie, "")
	if !strings.Contains(list.Body.String(), project.ID) || !strings.Contains(list.Body.String(), `"isPublic":true`) {
		t.Fatalf("公开项目未出现在成员列表: %s", list.Body.String())
	}
	for _, path := range append([]string{"/api/projects"}, readPaths...) {
		if response := request(http.MethodGet, path, nil, nil, ""); response.Code != http.StatusUnauthorized {
			t.Fatalf("未登录访问 %s: %d", path, response.Code)
		}
	}
	for _, operation := range []struct{ method, path, body string }{
		{http.MethodPost, base + "/sync", `{"branch":"main"}`},
		{http.MethodPost, base + "/branches", `{"name":"new"}`},
		{http.MethodPut, base + "/access/project", `{"userIds":[]}`},
		{http.MethodDelete, base, ``},
	} {
		if response := request(operation.method, operation.path, []byte(operation.body), memberCookie, memberCSRF); response.Code != http.StatusForbidden {
			t.Fatalf("普通成员不应执行项目管理操作 %s: %d", operation.path, response.Code)
		}
	}
	setVisibility(false, ownerCookie, ownerCSRF, http.StatusOK)
	assertRead(http.StatusForbidden)
	if response := request(http.MethodGet, "/api/projects", nil, memberCookie, ""); strings.Contains(response.Body.String(), project.ID) {
		t.Fatal("私有项目不应对未授权成员可见")
	}
	if err := db.SetProjectReadAccess(ctx, project.ID, []string{member.ID}, nil, owner.ID); err != nil {
		t.Fatal(err)
	}
	setVisibility(true, ownerCookie, ownerCSRF, http.StatusOK)
	setVisibility(false, ownerCookie, ownerCSRF, http.StatusOK)
	assertRead(http.StatusOK)
	updated, err := db.Project(ctx, project.ID)
	if err != nil || updated.IsPublic || updated.Branches[0].CommitSHA != "commit" {
		t.Fatalf("切换可见范围应保留快照: %#v %v", updated, err)
	}
	for _, isPublic := range []bool{false, true} {
		name := "private-created"
		if isPublic {
			name = "public-created"
		}
		input := map[string]any{
			"name": name, "repoUrl": "git@git.test:team/" + name + ".git",
			"docRoots": []string{"docs"}, "branches": []string{"main"},
		}
		if isPublic {
			input["isPublic"] = true
		}
		payload, _ := json.Marshal(input)
		response := request(http.MethodPost, "/api/projects", payload, ownerCookie, ownerCSRF)
		var result struct {
			Data store.Project `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil ||
			response.Code != http.StatusCreated || result.Data.IsPublic != isPublic {
			t.Fatalf("创建项目公开性错误: %d %s", response.Code, response.Body.String())
		}
	}
}
