package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dochub/internal/config"
	"dochub/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func TestUserEditingAndDeletion(t *testing.T) {
	db := oauthTestDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	const secret = "test-user-secret"
	admin, err := db.CreateUser(ctx, "user-admin", secret, "admin")
	if err != nil {
		t.Fatal(err)
	}
	member, err := db.CreateUser(ctx, "user-member", secret, "member")
	if err != nil {
		t.Fatal(err)
	}
	router := New(config.Config{StorageDir: t.TempDir(), SessionTTL: time.Hour}, db, nil).Router()
	adminCookie, adminCSRF := loginForTest(t, router, admin.Username, secret)
	memberCookie, memberCSRF := loginForTest(t, router, member.Username, secret)
	request := func(method, id string, body map[string]string, cookie *http.Cookie, csrf string, want int) {
		t.Helper()
		payload, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "/api/users/"+id, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(cookie)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, id, response.Code, want, response.Body.String())
		}
	}
	request(http.MethodPut, admin.ID, map[string]string{"username": admin.Username, "password": "admin-new-secret"}, adminCookie, adminCSRF, http.StatusOK)
	edit := map[string]string{"username": "renamed-member"}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		request(method, admin.ID, edit, memberCookie, memberCSRF, http.StatusForbidden)
	}
	request(http.MethodDelete, admin.ID, nil, adminCookie, adminCSRF, http.StatusBadRequest)
	request(http.MethodPut, member.ID, map[string]string{"username": admin.Username}, adminCookie, adminCSRF, http.StatusConflict)
	request(http.MethodPut, member.ID, edit, adminCookie, adminCSRF, http.StatusOK)
	_, hash, err := db.UserByUsername(ctx, edit["username"])
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret)) != nil {
		t.Fatalf("留空密码应保持不变: %v", err)
	}
	edit["password"] = "changed-user-secret"
	request(http.MethodPut, member.ID, edit, adminCookie, adminCSRF, http.StatusOK)
	_, hash, err = db.UserByUsername(ctx, edit["username"])
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(edit["password"])) != nil {
		t.Fatalf("新密码未保存: %v", err)
	}
	var sessions int
	if err = db.DB.QueryRow(ctx, `SELECT COUNT(*) FROM sessions WHERE user_id = $1`, member.ID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("旧会话未清理: %d %v", sessions, err)
	}
	_, err = db.DB.Exec(ctx, `INSERT INTO projects (id, slug, name, repo_url, doc_root, owner_id) VALUES ('owned-project', 'owned-project', 'test', 'git@example.com:test.git', 'docs', $1)`, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 删除时同时覆盖本人授权的清理和其他成员授权的保留。
	for _, query := range []string{
		`INSERT INTO organizations (id, name) VALUES ('user-test-org', 'test org')`,
		`INSERT INTO organization_members (organization_id, user_id) VALUES ('user-test-org', $1)`,
		`INSERT INTO oauth_identities (provider_id, issuer, subject, user_id) VALUES ('test', 'test', 'test', $1)`,
		`INSERT INTO project_read_permissions (project_id, user_id, created_by) VALUES ('owned-project', $1, $1), ('owned-project', $2, $1)`,
		`INSERT INTO document_read_permissions (project_id, document_path, user_id, created_by) VALUES ('owned-project', 'docs/test.md', $1, $1), ('owned-project', 'docs/test.md', $2, $1)`,
		`INSERT INTO project_read_organizations (project_id, organization_id, created_by) VALUES ('owned-project', 'user-test-org', $1)`,
		`INSERT INTO document_read_organizations (project_id, document_path, organization_id, created_by) VALUES ('owned-project', 'docs/test.md', 'user-test-org', $1)`,
	} {
		args := []any{}
		if strings.Contains(query, "$1") {
			args = append(args, member.ID)
		}
		if strings.Contains(query, "$2") {
			args = append(args, admin.ID)
		}
		if _, err = db.DB.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.DeleteUser(ctx, admin.ID, member.ID); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("最后管理员保护失败: %v", err)
	}
	request(http.MethodDelete, member.ID, nil, adminCookie, adminCSRF, http.StatusNoContent)
	var owner string
	if err = db.DB.QueryRow(ctx, `SELECT owner_id FROM projects WHERE id = 'owned-project'`).Scan(&owner); err != nil || owner != admin.ID {
		t.Fatalf("项目没有转交: %s %v", owner, err)
	}
	if _, _, err = db.UserByUsername(ctx, edit["username"]); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("用户未删除: %v", err)
	}
	for _, table := range []string{"project_read_permissions", "document_read_permissions", "project_read_organizations", "document_read_organizations"} {
		var creator string
		if err = db.DB.QueryRow(ctx, "SELECT created_by FROM "+table+" WHERE project_id = 'owned-project'").Scan(&creator); err != nil || creator != admin.ID {
			t.Fatalf("%s 授权未保留或转交: %s %v", table, creator, err)
		}
	}
	for _, table := range []string{"organization_members", "oauth_identities", "sessions", "access_tokens"} {
		var count int
		if err = db.DB.QueryRow(ctx, "SELECT COUNT(*) FROM "+table+" WHERE user_id = $1", member.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s 关联未清理: %d %v", table, count, err)
		}
	}
	request(http.MethodDelete, member.ID, nil, adminCookie, adminCSRF, http.StatusNotFound)
}
