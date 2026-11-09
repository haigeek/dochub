package oauth2

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestGenericProvider(t *testing.T) {
	for _, authMethod := range []string{"client_secret_post", "client_secret_basic"} {
		t.Run(authMethod, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/token":
					if err := request.ParseForm(); err != nil {
						t.Error(err)
						return
					}
					if request.PostForm.Get("redirect_uri") != "https://docs.test/gateway/login/callback" {
						t.Error("Token 请求未保留自定义回调地址")
					}
					if request.PostForm.Get("code_verifier") != "verifier" {
						t.Error("未传递 PKCE verifier")
					}
					if authMethod == "client_secret_basic" {
						id, secret, ok := request.BasicAuth()
						if !ok || id != url.QueryEscape("client:id") || secret != url.QueryEscape("secret+&:") ||
							request.PostForm.Has("client_id") || request.PostForm.Has("client_secret") {
							t.Error("Basic 客户端认证未正确编码或重复发送")
						}
					} else if request.PostForm.Get("client_id") != "client:id" || request.PostForm.Get("client_secret") != "secret+&:" {
						t.Error("表单客户端认证错误")
					}
					_, _ = io.WriteString(writer, `{"access_token":"token","token_type":"Bearer"}`)
				case "/me":
					if request.Header.Get("Authorization") != "Bearer token" {
						t.Error("未通过 Bearer 请求用户信息")
					}
					_, _ = io.WriteString(writer, `{"data":{"id":1234567890123456789,"displayName":"generic-user"}}`)
				default:
					t.Errorf("访问非预期路径: %s", request.URL.Path)
				}
			}))
			defer upstream.Close()
			provider, err := NewProvider(Config{
				ID: "company", Name: "企业身份服务", Issuer: "company-identity",
				AuthorizeURL: upstream.URL + "/authorize", TokenURL: upstream.URL + "/token", UserInfoURL: upstream.URL + "/me",
				ClientID: "client:id", ClientSecret: "secret+&:", TokenAuthMethod: authMethod,
				RedirectURI: "https://docs.test/gateway/login/callback",
				Scopes:      []string{"profile", "email"}, PKCE: true, SubjectField: "data.id", UsernameField: "data.displayName",
			})
			if err != nil {
				t.Fatal(err)
			}
			location, err := provider.AuthorizationURL("state", "verifier")
			if err != nil {
				t.Fatal(err)
			}
			parsed, _ := url.Parse(location)
			sum := sha256.Sum256([]byte("verifier"))
			if parsed.Query().Get("redirect_uri") != "https://docs.test/gateway/login/callback" ||
				parsed.Query().Get("scope") != "profile email" || parsed.Query().Get("state") != "state" ||
				parsed.Query().Get("code_challenge_method") != "S256" ||
				parsed.Query().Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) {
				t.Fatal("授权参数或 PKCE challenge 错误")
			}
			identity, err := provider.ExchangeIdentity(context.Background(), "code", "verifier", "")
			if err != nil || identity.Issuer != "company-identity" || identity.Subject != "1234567890123456789" ||
				identity.Username != "generic-user" {
				t.Fatalf("通用身份映射错误: %#v %v", identity, err)
			}
			if _, err := provider.ExchangeIdentity(context.Background(), "code", "verifier", "other-issuer"); err == nil {
				t.Fatal("回调签发者不匹配时未拒绝")
			}
		})
	}
}

func TestProviderConfiguration(t *testing.T) {
	valid := Config{
		ID: "company", Issuer: "company",
		AuthorizeURL: "https://identity.test/authorize", TokenURL: "https://identity.test/token",
		UserInfoURL: "https://identity.test/me", ClientID: "id", ClientSecret: "secret",
		RedirectURI: "https://docs.test/api/auth/oauth2/company/callback",
	}
	for _, edit := range []func(*Config){
		func(cfg *Config) { cfg.ID = "../bad" },
		func(cfg *Config) { cfg.TokenURL = "https://user:secret@identity.test/token" },
		func(cfg *Config) { cfg.AuthorizeURL += "?client_id=other" },
		func(cfg *Config) { cfg.UserInfoURL += "#fragment" },
		func(cfg *Config) { cfg.ClientSecret = "" },
		func(cfg *Config) { cfg.RedirectURI = "javascript:alert(1)" },
		func(cfg *Config) { cfg.PortalURL = "javascript:alert(1)" },
		func(cfg *Config) { cfg.TokenAuthMethod = "unsupported" },
	} {
		cfg := valid
		edit(&cfg)
		if _, err := NewProvider(cfg); err == nil {
			t.Fatal("错误配置未被拒绝")
		}
	}
	if _, err := NewProvider(valid); err != nil {
		t.Fatal(err)
	}
	provider, err := NewProvider(valid)
	if err != nil {
		t.Fatal(err)
	}
	location, _ := provider.AuthorizationURL("state", "verifier")
	parsed, _ := url.Parse(location)
	if parsed.Query().Has("code_challenge") {
		t.Fatal("未启用 PKCE 时不应发送 challenge")
	}
}

func TestUnifiedIdentityFields(t *testing.T) {
	for _, test := range []struct {
		name           string
		body           string
		issuer         string
		callbackIssuer string
		wantIssuer     string
		wantError      bool
	}{
		{"response_issuer", `{"iss":"issuer","sub":"id","aud":"client"}`, "", "issuer", "issuer", false},
		{"configured_issuer", `{"sub":"id"}`, "configured", "", "configured", false},
		{"endpoint_issuer", `{"sub":"id"}`, "", "", "https://identity.test/me", false},
		{"audience_list", `{"sub":"id","aud":["other","client"]}`, "", "", "https://identity.test/me", false},
		{"wrong_audience", `{"sub":"id","aud":"other"}`, "", "", "", true},
		{"wrong_response_issuer", `{"iss":"other","sub":"id"}`, "configured", "", "", true},
		{"wrong_callback_issuer", `{"iss":"issuer","sub":"id"}`, "", "other", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity, err := (GenericAdapter{}).ParseIdentity([]byte(test.body), Config{
				ClientID: "client", Issuer: test.issuer, SubjectField: "sub",
				UserInfoURL: "https://identity.test/me",
			}, test.callbackIssuer)
			if (err != nil) != test.wantError || (!test.wantError && identity.Issuer != test.wantIssuer) {
				t.Fatalf("统一身份校验错误: %#v %v", identity, err)
			}
		})
	}
}
