package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"dochub/internal/config"
	"dochub/internal/oauth2"
	"dochub/internal/secretbox"
	"dochub/internal/store"
)

func TestCollieIntegration(t *testing.T) {
	db := oauthTestDB(t)
	ctx := context.Background()
	local, err := db.CreateUser(ctx, "collie-user", "local-password", "admin")
	if err != nil {
		t.Fatal(err)
	}
	// 对已有本地用户执行迁移，验证升级不会破坏密码和本地权限。
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("迁移不幂等: %v", err)
	}
	project, err := db.CreateProject(ctx, "private", "git@git.test:team/private.git", []string{"docs"}, local.ID, []string{"main"}, false)
	if err != nil {
		t.Fatal(err)
	}
	var tokenCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/collie/oauth2/token" {
			tokenCalls.Add(1)
			_, _ = io.WriteString(writer, `{"access_token":"token","token_type":"Bearer","expires_in":120}`)
		} else {
			_, _ = io.WriteString(writer, `{"iss":"collie-a","sub":"user-id","aud":"client-id","userName":"collie-user","isCollieAdmin":true}`)
		}
	}))
	defer upstream.Close()
	callbackPath := oauth2.CallbackPath("collie")
	providerConfig := oauth2.Config{
		ID:       "collie",
		ClientID: "client-id", ClientSecret: "server-secret",
		RedirectURI: "https://docs.test" + callbackPath, PortalURL: "https://portal.test",
	}
	storageDir := t.TempDir()
	saveSSOForTest(t, db, storageDir, store.SSOConfig{
		ID: "collie", Enabled: true, ProviderName: "Collie",
		AuthorizeURL: upstream.URL + "/collie/oauth2/authorize",
		TokenURL:     upstream.URL + "/collie/oauth2/token", UserInfoURL: upstream.URL + "/collie/oauth2/userinfo",
		ClientID: "client-id", RedirectURI: providerConfig.RedirectURI,
		PortalURL: "https://portal.test", TokenAuthMethod: "client_secret_post",
		SubjectField: "sub", UsernameField: "userName",
	}, "server-secret")
	cfg := config.Config{StorageDir: storageDir, CookieSecure: true, SessionTTL: 168 * time.Hour}
	server := New(cfg, db, nil)
	router := server.Router()
	target := "/projects/collie?branch=feature%2Fdocs#intro"
	start := httptest.NewRecorder()
	router.ServeHTTP(start, httptest.NewRequest(http.MethodGet,
		"/api/auth/oauth2/collie/start?"+url.Values{"redirect": {target}}.Encode(), nil))
	location, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if start.Code != http.StatusFound || location.Query().Get("response_type") != "code" ||
		location.Query().Get("client_id") != "client-id" ||
		location.Query().Get("redirect_uri") != providerConfig.RedirectURI {
		t.Fatalf("授权跳转错误: %d %s", start.Code, location)
	}
	stateCookie := findCookie(t, start, oauthStateCookie("collie"))
	if !stateCookie.HttpOnly || !stateCookie.Secure || stateCookie.SameSite != http.SameSiteLaxMode ||
		stateCookie.Path != "/api/auth" || stateCookie.MaxAge != 600 {
		t.Fatal("登录事务 Cookie 属性错误")
	}
	state := location.Query().Get("state")
	var storedHash string
	if err := db.DB.QueryRow(ctx, `SELECT state_hash FROM oauth_login_transactions`).Scan(&storedHash); err != nil ||
		storedHash != hashToken(state) || storedHash == state {
		t.Fatal("数据库应只保存 state 哈希")
	}
	callbackURL := callbackPath + "?" + url.Values{
		"state": {state}, "code": {"secret-code"}, "iss": {"collie-a"},
	}.Encode()
	wrongBrowser := httptest.NewRecorder()
	router.ServeHTTP(wrongBrowser, httptest.NewRequest(http.MethodGet, callbackURL, nil))
	if !strings.Contains(wrongBrowser.Header().Get("Location"), "invalid_state") || tokenCalls.Load() != 0 {
		t.Fatal("其他浏览器不应消费授权")
	}

	var logs bytes.Buffer
	previousWriter := gin.DefaultWriter
	gin.DefaultWriter = &logs
	loggedRouter := server.Router()
	gin.DefaultWriter = previousWriter
	callback := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	request.AddCookie(stateCookie)
	loggedRouter.ServeHTTP(callback, request)
	if callback.Code != http.StatusFound || callback.Header().Get("Location") != target {
		t.Fatalf("回调失败: %d %s", callback.Code, callback.Header().Get("Location"))
	}
	if logs.Len() != 0 || strings.Contains(callback.Body.String(), "secret-code") ||
		callback.Header().Get("Cache-Control") != "no-store" ||
		callback.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("回调凭据泄漏或缺少安全响应头")
	}
	if findCookie(t, callback, oauthStateCookie("collie")).MaxAge != -1 {
		t.Fatal("回调后未清理事务 Cookie")
	}
	sessionCookieValue := findCookie(t, callback, sessionCookie)
	if sessionCookieValue.MaxAge != 604800 || !sessionCookieValue.HttpOnly || !sessionCookieValue.Secure {
		t.Fatal("应沿用 7 天本地会话")
	}
	user, csrf, err := db.UserBySession(ctx, hashToken(sessionCookieValue.Value))
	if err != nil || user.ID == local.ID || user.Role != "member" || user.Username == local.Username {
		t.Fatalf("外部用户不应接管同名本地管理员: %#v %v", user, err)
	}
	_, hash, err := db.UserByUsername(ctx, user.Username)
	if err != nil || hash != "" {
		t.Fatal("Collie 用户不应设置本地密码")
	}
	replay := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, callbackURL, nil)
	request.AddCookie(stateCookie)
	router.ServeHTTP(replay, request)
	if !strings.Contains(replay.Header().Get("Location"), "invalid_state") || tokenCalls.Load() != 1 {
		t.Fatal("重复回调不应再次兑换 Token")
	}
	me := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.AddCookie(sessionCookieValue)
	router.ServeHTTP(me, request)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), csrf) {
		t.Fatal("SSO 会话未接入现有 /auth/me")
	}
	projects := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	request.AddCookie(sessionCookieValue)
	router.ServeHTTP(projects, request)
	if projects.Code != http.StatusOK || !strings.Contains(projects.Body.String(), `"data":[]`) {
		t.Fatalf("新成员不应拥有既有项目: %s", projects.Body.String())
	}
	if err := db.SetProjectReadAccess(ctx, project.ID, []string{user.ID}, nil, local.ID); err != nil {
		t.Fatal(err)
	}
	authorized := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	request.AddCookie(sessionCookieValue)
	router.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusOK || !strings.Contains(authorized.Body.String(), project.ID) {
		t.Fatal("SSO 用户未接入已有项目授权")
	}
	for _, withCSRF := range []bool{false, true} {
		logout := httptest.NewRecorder()
		request = httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
		request.AddCookie(sessionCookieValue)
		if withCSRF {
			request.Header.Set("X-CSRF-Token", csrf)
		}
		router.ServeHTTP(logout, request)
		want := http.StatusForbidden
		if withCSRF {
			want = http.StatusNoContent
		}
		if logout.Code != want {
			t.Fatalf("退出的 CSRF 校验错误: %d", logout.Code)
		}
	}
	if _, _, err := db.UserBySession(ctx, hashToken(sessionCookieValue.Value)); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("退出后会话仍然有效")
	}
	login := httptest.NewRecorder()
	router.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"collie-user","password":"local-password"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("原本地账号无法登录: %s", login.Body.String())
	}

	t.Run("identity_concurrency", func(t *testing.T) {
		var group sync.WaitGroup
		results := make(chan store.User, 8)
		failures := make(chan error, 8)
		for index := 0; index < 8; index++ {
			group.Add(1)
			go func() {
				defer group.Done()
				created, err := db.OAuthUser(ctx, "collie", "collie-a", "concurrent-user", "same-name")
				results <- created
				failures <- err
			}()
		}
		group.Wait()
		close(results)
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		unique := make(map[string]bool)
		for created := range results {
			unique[created.ID] = true
		}
		if len(unique) != 1 {
			t.Fatalf("并发首次登录创建了多个账号: %v", unique)
		}
		other, err := db.OAuthUser(ctx, "collie", "collie-b", "concurrent-user", "same-name")
		if err != nil || unique[other.ID] || other.Username != "same-name_1" {
			t.Fatal("不同 issuer 的同名身份不应合并")
		}
		existing, err := db.OAuthUser(ctx, "collie", "collie-a", "user-id", "renamed")
		if err != nil || existing.ID != user.ID {
			t.Fatal("修改显示名称不应改变身份映射")
		}
	})

	t.Run("duplicate_names", func(t *testing.T) {
		for _, name := range []string{"duplicate-name", "duplicate-name_1"} {
			if _, err := db.OAuthUser(ctx, "collie", "collie-a", "reserved-"+name, name); err != nil {
				t.Fatal(err)
			}
		}
		var group sync.WaitGroup
		results := make(chan store.User, 8)
		failures := make(chan error, 8)
		for index := 0; index < 8; index++ {
			group.Add(1)
			go func(index int) {
				defer group.Done()
				created, err := db.OAuthUser(ctx, "collie", "collie-a",
					fmt.Sprintf("duplicate-subject-%d", index), "duplicate-name")
				results <- created
				failures <- err
			}(index)
		}
		group.Wait()
		close(results)
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		names := make(map[string]bool)
		for created := range results {
			names[created.Username] = true
		}
		for suffix := 2; suffix <= 9; suffix++ {
			if !names[fmt.Sprintf("duplicate-name_%d", suffix)] {
				t.Fatalf("并发重名应按数字后缀递增，实际为 %v", names)
			}
		}
		existing, err := db.OAuthUser(ctx, "collie", "collie-a", "duplicate-subject-0", "renamed")
		if err != nil || !names[existing.Username] {
			t.Fatalf("再次登录应保持已分配的登录名: %#v %v", existing, err)
		}
	})

	t.Run("expired_and_concurrent_transactions", func(t *testing.T) {
		if err := db.CreateOAuthLogin(ctx, "expired", "collie", "", "/projects", time.Now().Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ConsumeOAuthLogin(ctx, "expired", "collie"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("过期事务应被拒绝")
		}
		if err := db.CreateOAuthLogin(ctx, "once", "collie", "", target, time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		var remaining int
		if err := db.DB.QueryRow(ctx, `SELECT COUNT(*) FROM oauth_login_transactions WHERE state_hash = 'expired'`).Scan(&remaining); err != nil || remaining != 0 {
			t.Fatal("创建新事务时未清理过期事务")
		}
		var successes atomic.Int32
		var group sync.WaitGroup
		for index := 0; index < 8; index++ {
			group.Add(1)
			go func() {
				defer group.Done()
				if _, err := db.ConsumeOAuthLogin(ctx, "once", "collie"); err == nil {
					successes.Add(1)
				}
			}()
		}
		group.Wait()
		if successes.Load() != 1 {
			t.Fatal("登录事务未被原子消费")
		}
	})

	t.Run("duplicate_and_error_callbacks", func(t *testing.T) {
		for _, extra := range []string{"&state=duplicate", "&code=duplicate", "&iss=other", "&error=login_required"} {
			state := randomToken(32)
			if err := db.CreateOAuthLogin(ctx, hashToken(state), "collie", "", target, time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet,
				callbackPath+"?state="+state+"&code=secret&iss=collie-a"+extra, nil)
			request.AddCookie(&http.Cookie{Name: oauthStateCookie("collie"), Value: state})
			router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusFound || !strings.Contains(recorder.Header().Get("Location"), "oauthError=") ||
				tokenCalls.Load() != 1 {
				t.Fatal("无效回调应在兑换前被拒绝")
			}
		}
	})
}

func TestGenericOAuthIntegration(t *testing.T) {
	db := oauthTestDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var challenge atomic.Value
	challenge.Store("")
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			if err := request.ParseForm(); err != nil {
				t.Error(err)
				return
			}
			sum := sha256.Sum256([]byte(request.PostForm.Get("code_verifier")))
			if request.PostForm.Get("code_verifier") == "" ||
				base64.RawURLEncoding.EncodeToString(sum[:]) != challenge.Load().(string) {
				t.Error("PKCE verifier 与原始授权 challenge 不匹配")
			}
			_, _ = io.WriteString(writer, `{"access_token":"token","token_type":"Bearer"}`)
		case "/me":
			_, _ = io.WriteString(writer, `{"data":{"id":"same-subject","name":"generic-user"}}`)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	storageDir := t.TempDir()
	saveSSOForTest(t, db, storageDir, store.SSOConfig{
		ID: "sso", Enabled: true, ProviderName: "企业身份", Issuer: "same-issuer",
		AuthorizeURL: upstream.URL + "/authorize", TokenURL: upstream.URL + "/token",
		UserInfoURL: upstream.URL + "/me", ClientID: "alpha",
		RedirectURI: "https://docs.test" + oauth2.CallbackPath("sso"),
		PKCE:        true, TokenAuthMethod: "client_secret_post", SubjectField: "data.id", UsernameField: "data.name",
	}, "secret")
	cfg := config.Config{StorageDir: storageDir, SessionTTL: 168 * time.Hour}
	router := New(cfg, db, nil).Router()
	id := "sso"
	start := httptest.NewRecorder()
	router.ServeHTTP(start, httptest.NewRequest(http.MethodGet, "/api/auth/oauth2/"+id+"/start", nil))
	location, _ := url.Parse(start.Header().Get("Location"))
	state := location.Query().Get("state")
	challenge.Store(location.Query().Get("code_challenge"))
	if _, err := db.ConsumeOAuthLogin(ctx, hashToken(state), "another-provider"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("其他 provider 不应能消费事务")
	}
	callback := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, oauth2.CallbackPath(id)+"?state="+state+"&code=code", nil)
	request.AddCookie(findCookie(t, start, oauthStateCookie(id)))
	router.ServeHTTP(callback, request)
	if callback.Code != http.StatusFound || callback.Header().Get("Location") != "/projects" {
		t.Fatalf("通用服务回调失败: %d %s", callback.Code, callback.Header().Get("Location"))
	}
	session := findCookie(t, callback, sessionCookie)
	user, _, err := db.UserBySession(ctx, hashToken(session.Value))
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.OAuthUser(ctx, "other-provider", "same-issuer", "same-subject", "other-user")
	if err != nil || other.ID == user.ID {
		t.Fatal("不同 provider 的相同 issuer/sub 不应合并")
	}
}

func TestLegacyCollieMigration(t *testing.T) {
	db := oauthTestDB(t)
	ctx := context.Background()
	legacy, err := db.CreateUser(ctx, "legacy-user", "legacy-password", "admin")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.DB.Exec(ctx, `
		CREATE TABLE collie_identities (
			issuer TEXT NOT NULL, subject TEXT NOT NULL, user_id TEXT NOT NULL REFERENCES users(id),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), PRIMARY KEY (issuer, subject)
		);
		CREATE TABLE oauth_login_transactions (
			state_hash TEXT PRIMARY KEY, return_path TEXT NOT NULL, expires_at TIMESTAMPTZ NOT NULL
		)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(ctx, `INSERT INTO collie_identities (issuer, subject, user_id) VALUES ('collie-a', 'old-subject', $1)`, legacy.ID); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := db.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	mapped, err := db.OAuthUser(ctx, "collie", "collie-a", "old-subject", "renamed")
	if err != nil || mapped.ID != legacy.ID || mapped.Role != "admin" {
		t.Fatal("旧 Collie 绑定迁移后丢失账号或角色")
	}
	if err := db.CreateOAuthLogin(ctx, "new-state", "company", "verifier", "/projects", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	transaction, err := db.ConsumeOAuthLogin(ctx, "new-state", "company")
	if err != nil || transaction.Verifier != "verifier" {
		t.Fatal("旧事务表未正确增加 provider 和 PKCE 字段")
	}
}

func TestSSOAdminConfiguration(t *testing.T) {
	db := oauthTestDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := db.CreateUser(ctx, "sso-admin", "admin-password", "admin")
	if err != nil {
		t.Fatal(err)
	}
	member, err := db.CreateUser(ctx, "sso-member", "member-password", "member")
	if err != nil {
		t.Fatal(err)
	}
	storageDir := t.TempDir()
	router := New(config.Config{StorageDir: storageDir, SessionTTL: time.Hour}, db, nil).Router()
	adminCookie, adminCSRF := loginForTest(t, router, "sso-admin", "admin-password")

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/sso", nil)
	request.AddCookie(adminCookie)
	router.ServeHTTP(response, request)
	var initial struct {
		Data struct {
			Providers []ssoProviderView `json:"providers"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&initial); err != nil ||
		response.Code != http.StatusOK || len(initial.Data.Providers) != 0 {
		t.Fatalf("读取初始站点列表失败: %d %s", response.Code, response.Body.String())
	}

	settings := store.SSOConfig{
		Enabled: true, ProviderName: "企业认证",
		EndpointBaseURL: "http://127.0.0.1:1", AuthorizePath: "/collie/oauth2/authorize",
		TokenPath: "/collie/oauth2/token", UserInfoPath: "/collie/oauth2/userinfo", ClientID: "client-id",
		RedirectURI: "https://docs.test/api/auth/oauth2/sso/callback",
		PortalURL:   "https://portal.example.com", TokenAuthMethod: "client_secret_post",
		SubjectField: "sub", UsernameField: "name", Scopes: []string{},
	}
	body, _ := json.Marshal(gin.H{"clientSecret": "super-secret", "enabled": settings.Enabled,
		"providerName": settings.ProviderName, "endpointBaseUrl": settings.EndpointBaseURL,
		"authorizePath": "/collie/oauth2/authorize", "tokenPath": "/collie/oauth2/token",
		"userInfoPath": "/collie/oauth2/userinfo", "clientId": settings.ClientID,
		"redirectUri": settings.RedirectURI, "portalUrl": settings.PortalURL,
		"tokenAuthMethod": settings.TokenAuthMethod, "subjectField": "sub", "usernameField": "name"})
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/admin/sso", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", adminCSRF)
	request.AddCookie(adminCookie)
	router.ServeHTTP(response, request)
	var saved struct {
		Data ssoProviderView `json:"data"`
	}
	responseBody := response.Body.String()
	if err := json.Unmarshal([]byte(responseBody), &saved); err != nil ||
		response.Code != http.StatusOK || !saved.Data.HasClientSecret || saved.Data.ID == "" ||
		saved.Data.CallbackPath != oauth2.CallbackPath(saved.Data.ID) ||
		strings.Contains(response.Body.String(), "super-secret") {
		t.Fatalf("保存配置失败或密钥泄漏: %d %s", response.Code, responseBody)
	}
	var ciphertext string
	if err := db.DB.QueryRow(ctx, `SELECT client_secret_ciphertext FROM sso_settings WHERE id = $1`, saved.Data.ID).Scan(&ciphertext); err != nil ||
		ciphertext == "" || strings.Contains(ciphertext, "super-secret") {
		t.Fatal("客户端密钥未加密保存")
	}
	plaintext, err := secretbox.New(storageDir).Decrypt(ciphertext)
	if err != nil || plaintext != "super-secret" {
		t.Fatal("保存的客户端密钥无法解密")
	}

	list := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/admin/sso", nil)
	request.AddCookie(adminCookie)
	router.ServeHTTP(list, request)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), saved.Data.ID) ||
		!strings.Contains(list.Body.String(), `"callbackPath"`) {
		t.Fatalf("站点列表未返回: %s", list.Body.String())
	}

	secondBody, _ := json.Marshal(gin.H{
		"clientSecret": "second-secret", "enabled": true, "providerName": "第二身份站点",
		"endpointBaseUrl": "http://127.0.0.1:2", "authorizePath": "/authorize",
		"tokenPath": "/token", "userInfoPath": "/me", "clientId": "second-client",
		"redirectUri":     "https://docs.test/api/auth/oauth2/pending/callback",
		"tokenAuthMethod": "client_secret_post", "subjectField": "sub", "usernameField": "name",
	})
	second := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/admin/sso", bytes.NewReader(secondBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", adminCSRF)
	request.AddCookie(adminCookie)
	router.ServeHTTP(second, request)
	var secondSaved struct {
		Data ssoProviderView `json:"data"`
	}
	secondResponseBody := second.Body.String()
	if err := json.Unmarshal([]byte(secondResponseBody), &secondSaved); err != nil ||
		second.Code != http.StatusOK || secondSaved.Data.ID == saved.Data.ID {
		t.Fatalf("新增第二个站点失败: %d %s", second.Code, secondResponseBody)
	}
	list = httptest.NewRecorder()
	router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/admin/sso", nil))
	if list.Code != http.StatusUnauthorized {
		// 列表接口必须登录，先确认未认证请求被拒绝。
		t.Fatal("未登录请求不应读取站点列表")
	}
	request = httptest.NewRequest(http.MethodGet, "/api/admin/sso", nil)
	request.AddCookie(adminCookie)
	list = httptest.NewRecorder()
	router.ServeHTTP(list, request)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), saved.Data.ID) ||
		!strings.Contains(list.Body.String(), secondSaved.Data.ID) {
		t.Fatalf("多站点列表不完整: %s", list.Body.String())
	}

	options := httptest.NewRecorder()
	router.ServeHTTP(options, httptest.NewRequest(http.MethodGet, "/api/auth/options", nil))
	if options.Code != http.StatusOK || !strings.Contains(options.Body.String(), `"id":"`+saved.Data.ID+`"`) ||
		!strings.Contains(options.Body.String(), `"id":"`+secondSaved.Data.ID+`"`) ||
		strings.Contains(options.Body.String(), "super-secret") {
		t.Fatalf("启用的登录入口未公开: %s", options.Body.String())
	}
	start := httptest.NewRecorder()
	router.ServeHTTP(start, httptest.NewRequest(http.MethodGet, "/api/auth/oauth2/"+saved.Data.ID+"/start", nil))
	if start.Code != http.StatusFound || !strings.Contains(start.Header().Get("Location"), "127.0.0.1:1") {
		t.Fatalf("系统配置未驱动授权跳转: %d %s", start.Code, start.Header().Get("Location"))
	}

	memberCookie, _ := loginForTest(t, router, "sso-member", "member-password")
	forbidden := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/admin/sso", nil)
	request.AddCookie(memberCookie)
	router.ServeHTTP(forbidden, request)
	if forbidden.Code != http.StatusForbidden {
		t.Fatal("非管理员可以读取单点登录配置")
	}

	invalidBody := bytes.Replace(body, []byte(`"authorizePath":"/collie/oauth2/authorize"`), []byte(`"authorizePath":"//evil.example.com/authorize"`), 1)
	invalid := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPut, "/api/admin/sso/"+saved.Data.ID, bytes.NewReader(invalidBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", adminCSRF)
	request.AddCookie(adminCookie)
	router.ServeHTTP(invalid, request)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("无效启用配置未被拒绝: %d %s", invalid.Code, invalid.Body.String())
	}

	offBody, _ := json.Marshal(gin.H{
		"enabled": false, "providerName": "统一身份认证",
		"clientId": "", "redirectUri": "https://docs.test" + oauth2.CallbackPath(saved.Data.ID),
		"clearClientSecret": true,
	})
	off := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPut, "/api/admin/sso/"+saved.Data.ID, bytes.NewReader(offBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", adminCSRF)
	request.AddCookie(adminCookie)
	router.ServeHTTP(off, request)
	if off.Code != http.StatusOK || !strings.Contains(off.Body.String(), `"hasClientSecret":false`) {
		t.Fatalf("关闭并清除密钥失败: %d %s", off.Code, off.Body.String())
	}
	if _, err := db.SSOClientSecret(ctx, saved.Data.ID); err == nil {
		t.Fatal("清除后仍存在客户端密钥")
	}
	deleted := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodDelete, "/api/admin/sso/"+saved.Data.ID, nil)
	request.Header.Set("X-CSRF-Token", adminCSRF)
	request.AddCookie(adminCookie)
	router.ServeHTTP(deleted, request)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("删除站点失败: %d %s", deleted.Code, deleted.Body.String())
	}
	if admin.ID == member.ID {
		t.Fatal("测试用户创建错误")
	}
}

func TestSSOEndpointMigration(t *testing.T) {
	db := oauthTestDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := db.OAuthUser(ctx, "collie", "collie-a", "legacy-subject", "legacy-user")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.DB.Exec(ctx, `
		ALTER TABLE sso_settings DROP COLUMN provider_id;
		INSERT INTO sso_settings (id, enabled, provider_type, base_url, client_id, redirect_uri)
		VALUES ('default', true, 'collie', 'https://identity.test/',
			'client-id', 'https://docs.test/api/auth/oauth2/collie/callback')`)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := db.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	settings, err := db.SSOConfig(ctx, "collie")
	if err != nil || settings.ProviderID() != "collie" ||
		settings.AuthorizeURL != "https://identity.test/collie/oauth2/authorize" ||
		settings.TokenURL != "https://identity.test/collie/oauth2/token" ||
		settings.UserInfoURL != "https://identity.test/collie/oauth2/userinfo" ||
		settings.UsernameField != "userName" ||
		settings.RedirectURI != "https://docs.test/api/auth/oauth2/collie/callback" {
		t.Fatalf("旧配置端点展开失败: %#v %v", settings, err)
	}
	if settings.EndpointBaseURL != "https://identity.test" ||
		settings.AuthorizePath != "/collie/oauth2/authorize" ||
		settings.TokenPath != "/collie/oauth2/token" ||
		settings.UserInfoPath != "/collie/oauth2/userinfo" {
		t.Fatal("旧配置未自动拆分为基础地址和三个 path")
	}
	mapped, err := db.OAuthUser(ctx, settings.ProviderID(), "collie-a", "legacy-subject", "renamed")
	if err != nil || mapped.ID != user.ID {
		t.Fatal("升级后应保持既有账号绑定")
	}
	payload, _ := json.Marshal(settings)
	if bytes.Contains(payload, []byte("providerType")) || bytes.Contains(payload, []byte("baseUrl")) {
		t.Fatal("统一配置不应暴露旧版类型与基础地址")
	}
}

func loginForTest(t *testing.T, router *gin.Engine, username, password string) (*http.Cookie, string) {
	t.Helper()
	payload, _ := json.Marshal(gin.H{"username": username, "password": password})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(payload)))
	if response.Code != http.StatusOK {
		t.Fatalf("登录失败: %s", response.Body.String())
	}
	var body struct {
		Data struct {
			CSRF string `json:"csrfToken"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return findCookie(t, response, sessionCookie), body.Data.CSRF
}

func oauthTestDB(t *testing.T) *store.Store {
	t.Helper()
	databaseURL := os.Getenv("DOCHUB_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("设置 DOCHUB_TEST_DATABASE_URL 后运行 PostgreSQL 集成测试")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := "oauth_test_" + randomToken(8)
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+quotedSchema+" CASCADE")
		admin.Close()
	})
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	db := &store.Store{DB: pool}
	_, err = pool.Exec(ctx, `CREATE TABLE users (
		id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL,
		role TEXT NOT NULL CHECK (role IN ('admin', 'member')),
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`)
	if err != nil {
		t.Fatal(fmt.Errorf("准备旧用户表: %w", err))
	}
	return db
}

func saveSSOForTest(t *testing.T, db *store.Store, storageDir string, settings store.SSOConfig, secret string) {
	t.Helper()
	ciphertext := ""
	var err error
	if secret != "" {
		ciphertext, err = secretbox.New(storageDir).Encrypt(secret)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SaveSSOConfig(context.Background(), settings, ciphertext, ""); err != nil {
		t.Fatal(err)
	}
}

func findCookie(t *testing.T, recorder *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("响应缺少 Cookie %s", name)
	return nil
}
