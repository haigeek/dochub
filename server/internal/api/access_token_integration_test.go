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
	"dochub/internal/store"
)

func TestPersonalAccessToken(t *testing.T) {
	db := oauthTestDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner, err := db.CreateUser(ctx, "token-owner", "owner-password", "member")
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateUser(ctx, "token-other", "other-password", "member")
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.CreateProject(ctx, "Token project", "", "git@git.test:team/token.git",
		[]string{"docs"}, owner.ID, []string{"main"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetProjectReadAccess(ctx, project.ID, []string{other.ID}, nil, owner.ID); err != nil {
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
	if err := db.FinishBranchSync(ctx, project.ID, "main", "commit", snapshot, []store.DocFile{
		{Path: "docs/readme.md", Title: "Agent guide", Markdown: "# Agent guide", PlainText: "Agent guide"},
	}); err != nil {
		t.Fatal(err)
	}
	router := New(config.Config{StorageDir: t.TempDir(), SessionTTL: time.Hour}, db, nil).Router()
	otherCookie, otherCSRF := loginForTest(t, router, "token-other", "other-password")
	request := func(method, path string, body []byte, cookie *http.Cookie, csrf, bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}

	payload, _ := json.Marshal(map[string]any{"name": "Local Agent", "expiresInDays": 30})
	createdResponse := request(http.MethodPost, "/api/account/tokens", payload, otherCookie, otherCSRF, "")
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("创建凭证失败: %d %s", createdResponse.Code, createdResponse.Body.String())
	}
	var created struct {
		Data struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil ||
		created.Data.ID == "" || !strings.HasPrefix(created.Data.Token, "dochub_pat_") {
		t.Fatalf("凭证响应错误: %s", createdResponse.Body.String())
	}
	var storedHash, storedPrefix, storedCiphertext string
	if err := db.DB.QueryRow(ctx,
		`SELECT token_hash, token_prefix, token_ciphertext FROM access_tokens WHERE id = $1`,
		created.Data.ID,
	).Scan(&storedHash, &storedPrefix, &storedCiphertext); err != nil ||
		storedHash == created.Data.Token || storedCiphertext == created.Data.Token ||
		strings.Contains(storedCiphertext, created.Data.Token) {
		t.Fatal("数据库不应保存 Agent 凭证明文或明文哈希")
	}

	list := request(http.MethodGet, "/api/account/tokens", nil, otherCookie, "", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), created.Data.Token) ||
		!strings.Contains(list.Body.String(), `"name":"Local Agent"`) {
		t.Fatalf("登录后应能再次查看 Agent 凭证: %s", list.Body.String())
	}
	projects := request(http.MethodGet, "/api/projects", nil, nil, "", created.Data.Token)
	if projects.Code != http.StatusOK || !strings.Contains(projects.Body.String(), project.ID) {
		t.Fatalf("Agent 凭证应继承用户当前权限: %d %s", projects.Code, projects.Body.String())
	}
	doc := request(http.MethodGet, "/api/projects/"+project.ID+"/doc?branch=main&path=docs/readme.md", nil, nil, "", created.Data.Token)
	if doc.Code != http.StatusOK || !strings.Contains(doc.Body.String(), "# Agent guide") {
		t.Fatalf("Agent 凭证应能读取文档: %d %s", doc.Code, doc.Body.String())
	}
	if response := request(http.MethodGet, "/api/account/tokens", nil, nil, "", created.Data.Token); response.Code != http.StatusUnauthorized {
		t.Fatalf("Agent 凭证不应读取账号凭证列表: %d", response.Code)
	}
	if response := request(http.MethodPost, "/api/projects", []byte(`{}`), nil, "", created.Data.Token); response.Code != http.StatusUnauthorized {
		t.Fatalf("Agent 凭证不应执行写操作: %d", response.Code)
	}

	if err := db.SetProjectReadAccess(ctx, project.ID, nil, nil, owner.ID); err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodGet, "/api/projects/"+project.ID, nil, nil, "", created.Data.Token); response.Code != http.StatusForbidden {
		t.Fatalf("用户权限撤销后 Agent 凭证应同步失效: %d", response.Code)
	}
	if err := db.SetProjectReadAccess(ctx, project.ID, []string{other.ID}, nil, owner.ID); err != nil {
		t.Fatal(err)
	}

	otherUserCookie, otherUserCSRF := loginForTest(t, router, "token-owner", "owner-password")
	if response := request(http.MethodDelete, "/api/account/tokens/"+created.Data.ID, nil, otherUserCookie, otherUserCSRF, ""); response.Code != http.StatusNotFound {
		t.Fatalf("其他用户不应撤销凭证: %d", response.Code)
	}
	if _, err := db.DB.Exec(ctx, `UPDATE access_tokens SET expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, created.Data.ID); err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodGet, "/api/projects", nil, nil, "", created.Data.Token); response.Code != http.StatusUnauthorized {
		t.Fatalf("过期凭证不应继续访问: %d", response.Code)
	}
	if _, err := db.DB.Exec(ctx, `UPDATE access_tokens SET expires_at = NULL WHERE id = $1`, created.Data.ID); err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodDelete, "/api/account/tokens/"+created.Data.ID, nil, otherCookie, otherCSRF, ""); response.Code != http.StatusNoContent {
		t.Fatalf("撤销凭证失败: %d %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/projects", nil, nil, "", created.Data.Token); response.Code != http.StatusUnauthorized {
		t.Fatalf("已撤销凭证不应继续访问: %d", response.Code)
	}
}
