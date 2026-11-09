package oauth2

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchCollieIdentity(t *testing.T) {
	tests := []struct {
		name        string
		tokenBody   string
		userBody    string
		userStatus  int
		status      int
		issuer      string
		wantFailure string
	}{
		{name: "success", issuer: "collie-a"},
		{name: "optional_callback_issuer"},
		{name: "wrong_audience", userBody: `{"iss":"collie-a","sub":"user-id","aud":"other"}`, wantFailure: "invalid_identity"},
		{name: "empty_issuer", userBody: `{"iss":"","sub":"user-id","aud":"client-id"}`, wantFailure: "invalid_identity"},
		{name: "missing_subject", userBody: `{"iss":"collie-a","aud":"client-id"}`, wantFailure: "invalid_identity"},
		{name: "issuer_mismatch", issuer: "other", wantFailure: "invalid_identity"},
		{name: "invalid_json", userBody: `not json`, wantFailure: "invalid_identity"},
		{name: "invalid_access_token", userStatus: 401, userBody: `{"error":"invalid_token"}`, wantFailure: "authorization_expired"},
		{name: "oversized", userBody: strings.Repeat("x", (1<<20)+1), wantFailure: "unavailable"},
		{name: "bad_type", tokenBody: `{"access_token":"token","token_type":"Basic","expires_in":120}`, wantFailure: "invalid_identity"},
		{name: "expired_token", tokenBody: `{"access_token":"token","token_type":"Bearer","expires_in":-1}`, wantFailure: "invalid_identity"},
		{name: "header_injection", tokenBody: `{"access_token":"token\r\nX-Bad: yes","token_type":"Bearer","expires_in":120}`, wantFailure: "invalid_identity"},
		{name: "login_required", status: 401, tokenBody: `{"error":"login_required"}`, wantFailure: "login_required"},
		{name: "expired_code", status: 400, tokenBody: `{"error":"invalid_grant"}`, wantFailure: "authorization_expired"},
		{name: "invalid_client", status: 401, tokenBody: `{"error":"invalid_client","error_description":"secret"}`, wantFailure: "authorization_failed"},
		{name: "rate_limit", status: 429, wantFailure: "unavailable"},
		{name: "server_error", status: 500, wantFailure: "unavailable"},
		{name: "redirect", status: 302, wantFailure: "unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tokenCalls, userCalls := 0, 0
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/collie/oauth2/token":
					tokenCalls++
					if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
						t.Error("Token 请求必须以表单 POST 提交")
					}
					if err := request.ParseForm(); err != nil {
						t.Error(err)
					}
					for key, want := range map[string]string{
						"grant_type": "authorization_code", "code": "one-time-code",
						"client_id": "client-id", "client_secret": "server-secret",
						"redirect_uri": "https://docs.test/api/auth/collie/callback",
					} {
						if request.PostForm.Get(key) != want {
							t.Errorf("Token 表单字段 %s 错误", key)
						}
					}
					if test.status != 0 {
						writer.Header().Set("Location", "/unexpected")
						writer.WriteHeader(test.status)
					}
					body := test.tokenBody
					if body == "" {
						body = `{"access_token":"token","token_type":"Bearer","expires_in":120}`
					}
					_, _ = io.WriteString(writer, body)
				case "/collie/oauth2/userinfo":
					userCalls++
					if test.userStatus != 0 {
						writer.WriteHeader(test.userStatus)
					}
					if request.Header.Get("Authorization") != "Bearer token" || request.URL.RawQuery != "" {
						t.Error("用户信息请求必须只通过 Authorization 头传递 Token")
					}
					body := test.userBody
					if body == "" {
						body = `{"iss":"collie-a","sub":"user-id","aud":"client-id","userName":"collie-user","isCollieAdmin":true}`
					}
					_, _ = io.WriteString(writer, body)
				default:
					t.Errorf("访问了非预期端点: %s", request.URL.Path)
					writer.WriteHeader(http.StatusNotFound)
				}
			}))
			defer upstream.Close()
			provider, err := NewProvider(Config{
				ID: "collie", ClientID: "client-id", UsernameField: "userName",
				AuthorizeURL: upstream.URL + "/collie/oauth2/authorize",
				TokenURL:     upstream.URL + "/collie/oauth2/token", UserInfoURL: upstream.URL + "/collie/oauth2/userinfo",
				ClientSecret: "server-secret", RedirectURI: "https://docs.test/api/auth/collie/callback",
			})
			if err != nil {
				t.Fatal(err)
			}
			identity, err := provider.ExchangeIdentity(context.Background(), "one-time-code", "", test.issuer)
			if test.wantFailure == "" {
				if err != nil || identity.Subject != "user-id" || identity.Username != "collie-user" {
					t.Fatalf("获取用户失败: %#v %v", identity, err)
				}
			} else {
				var failure Failure
				if !errors.As(err, &failure) || string(failure) != test.wantFailure {
					t.Fatalf("期望 %s，得到 %v", test.wantFailure, err)
				}
			}
			if tokenCalls != 1 || userCalls > 1 {
				t.Fatalf("不应自动重试: token=%d userinfo=%d", tokenCalls, userCalls)
			}
		})
	}
}

func TestCollieTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		<-request.Context().Done()
	}))
	defer upstream.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	provider, err := NewProvider(Config{
		ID: "collie", ClientID: "client-id",
		AuthorizeURL: upstream.URL + "/authorize", TokenURL: upstream.URL + "/token", UserInfoURL: upstream.URL + "/userinfo",
		ClientSecret: "secret", RedirectURI: "https://docs.test/api/auth/oauth2/collie/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.ExchangeIdentity(ctx, "code", "", "")
	if err == nil || err.Error() != "unavailable" {
		t.Fatalf("超时未返回固定错误: %v", err)
	}
}
