package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dochub/internal/config"
	"dochub/internal/oauth2"
)

func TestAuthOptionsUsesEnabledSettings(t *testing.T) {
	db := oauthTestDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		id      string
		enabled bool
		secret  string
	}{
		{id: "broken-secret", enabled: true, secret: "invalid-ciphertext"},
		{id: "missing-config", enabled: true},
		{id: "disabled", secret: "invalid-ciphertext"},
	} {
		if _, err := db.DB.Exec(ctx, `
			INSERT INTO sso_settings
				(id, auth_key, provider_id, enabled, provider_name, portal_url, client_secret_ciphertext)
			VALUES ($1, $1, $1, $2, $1, 'https://portal.test', $3)`,
			fixture.id, fixture.enabled, fixture.secret,
		); err != nil {
			t.Fatal(err)
		}
	}
	router := New(config.Config{StorageDir: t.TempDir()}, db, nil).Router()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/auth/options", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("读取登录方式失败: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Data struct {
			Providers []oauth2.Option `json:"providers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.Providers) != 2 {
		t.Fatalf("已启用但配置异常的站点仍应展示: %#v", payload.Data.Providers)
	}
	for _, option := range payload.Data.Providers {
		if option.ID == "disabled" || option.Name != option.ID ||
			option.LoginURL != "/api/auth/oauth2/"+option.ID+"/start" ||
			option.PortalURL != "https://portal.test" {
			t.Fatalf("登录入口内容错误: %#v", option)
		}
		start := httptest.NewRecorder()
		router.ServeHTTP(start, httptest.NewRequest(
			http.MethodGet, option.LoginURL+"?redirect=%2Fprojects%2Fexample", nil,
		))
		location, err := url.Parse(start.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if start.Code != http.StatusFound || location.Path != "/login" ||
			location.Query().Get("oauthError") != "unavailable" ||
			location.Query().Get("redirect") != "/projects/example" {
			t.Fatalf("认证配置异常应回到登录页提示不可用: %d %s", start.Code, location)
		}
	}
	if strings.Contains(response.Body.String(), "invalid-ciphertext") ||
		strings.Contains(response.Body.String(), "clientSecret") {
		t.Fatalf("公开登录方式不应暴露密钥: %s", response.Body.String())
	}
	for _, id := range []string{"disabled", "not-found"} {
		start := httptest.NewRecorder()
		router.ServeHTTP(start, httptest.NewRequest(
			http.MethodGet, "/api/auth/oauth2/"+id+"/start", nil,
		))
		if start.Code != http.StatusNotFound {
			t.Fatalf("未启用站点 %s 不应允许发起登录: %d", id, start.Code)
		}
	}
}
