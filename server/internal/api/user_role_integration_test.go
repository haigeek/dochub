package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dochub/internal/config"
	"dochub/internal/store"
)

func TestUserRoleManagement(t *testing.T) {
	const (
		admin1Secret = "role-secret-1"
		admin2Secret = "role-secret-2"
		memberSecret = "role-secret-3"
		readerSecret = "role-secret-4"
	)
	db := oauthTestDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	admin1, err := db.CreateUser(ctx, "role-admin-1", admin1Secret, "admin")
	if err != nil {
		t.Fatal(err)
	}
	admin2, err := db.CreateUser(ctx, "role-admin-2", admin2Secret, "admin")
	if err != nil {
		t.Fatal(err)
	}
	member, err := db.CreateUser(ctx, "role-member", memberSecret, "member")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := db.CreateUser(ctx, "role-reader", readerSecret, "member")
	if err != nil {
		t.Fatal(err)
	}
	router := New(config.Config{StorageDir: t.TempDir(), SessionTTL: time.Hour}, db, nil).Router()
	adminCookie, adminCSRF := loginForTest(t, router, admin1.Username, admin1Secret)
	readerCookie, readerCSRF := loginForTest(t, router, reader.Username, readerSecret)
	changeRole := func(cookie *http.Cookie, csrf, userID, role string) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(map[string]string{"role": role})
		request := httptest.NewRequest(http.MethodPatch, "/api/users/"+userID+"/role", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", csrf)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}

	if response := changeRole(readerCookie, readerCSRF, member.ID, "admin"); response.Code != http.StatusForbidden {
		t.Fatalf("普通成员不应修改角色: %d", response.Code)
	}
	if response := changeRole(adminCookie, adminCSRF, member.ID, "owner"); response.Code != http.StatusBadRequest {
		t.Fatalf("非法角色应被拒绝: %d", response.Code)
	}
	if response := changeRole(adminCookie, adminCSRF, admin1.ID, "member"); response.Code != http.StatusBadRequest {
		t.Fatalf("管理员不应修改自己的角色: %d", response.Code)
	}
	if response := changeRole(adminCookie, adminCSRF, member.ID, "admin"); response.Code != http.StatusOK {
		t.Fatalf("提升成员失败: %d %s", response.Code, response.Body.String())
	}
	if response := changeRole(adminCookie, adminCSRF, member.ID, "member"); response.Code != http.StatusOK {
		t.Fatalf("降级成员失败: %d %s", response.Code, response.Body.String())
	}
	if response := changeRole(adminCookie, adminCSRF, admin2.ID, "member"); response.Code != http.StatusOK {
		t.Fatalf("降级其他管理员失败: %d %s", response.Code, response.Body.String())
	}
	if _, err := db.UpdateUserRole(ctx, admin1.ID, "member"); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("最后一个管理员不应被降级: %v", err)
	}
	updated, _, err := db.UserByUsername(ctx, member.Username)
	if err != nil || updated.Role != "member" {
		t.Fatalf("角色修改未持久化: %#v %v", updated, err)
	}
}
