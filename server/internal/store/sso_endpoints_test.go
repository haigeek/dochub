package store

import "testing"

func TestSSOEndpointPaths(t *testing.T) {
	config := SSOConfig{
		EndpointBaseURL: "https://identity.test/gateway/",
		AuthorizePath:   "/oauth/authorize", TokenPath: "/oauth/token", UserInfoPath: "/api/me",
	}
	if err := config.ResolveEndpoints(); err != nil {
		t.Fatal(err)
	}
	if config.AuthorizeURL != "https://identity.test/gateway/oauth/authorize" ||
		config.TokenURL != "https://identity.test/gateway/oauth/token" ||
		config.UserInfoURL != "https://identity.test/gateway/api/me" {
		t.Fatal("端点拼接应保留基础地址中的网关前缀")
	}
	httpConfig := SSOConfig{EndpointBaseURL: "http://127.0.0.1:8080", AuthorizePath: "/authorize"}
	if err := httpConfig.ResolveEndpoints(); err != nil || httpConfig.AuthorizeURL != "http://127.0.0.1:8080/authorize" {
		t.Fatal("可信内网 HTTP 端点应被接受")
	}
	config.splitEndpoints()
	if config.AuthorizePath != "/oauth/authorize" {
		t.Fatal("保存后应保持相同的基础地址和 path")
	}
	for _, invalid := range []string{
		"https://evil.test/token", "//evil.test/token", "token", "/token?secret=value", "/token#fragment",
		"/../token", "/%2e%2e/token", "/%5cevil", "/%0atoken",
	} {
		config.AuthorizePath = invalid
		if err := config.ResolveEndpoints(); err == nil {
			t.Errorf("非法 path 被接受: %s", invalid)
		}
	}
}

func TestSSOEndpointBase(t *testing.T) {
	for _, invalid := range []string{
		"", "javascript:alert(1)", "//identity.test", "https://user:secret@identity.test",
		"https://identity.test?query=value", "https://identity.test#fragment", "https://identity.test/../gateway",
	} {
		config := SSOConfig{EndpointBaseURL: invalid, AuthorizePath: "/authorize"}
		if err := config.ResolveEndpoints(); err == nil {
			t.Errorf("非法基础地址被接受: %s", invalid)
		}
	}
	legacy := SSOConfig{
		AuthorizeURL: "https://identity.test/oauth/authorize",
		TokenURL:     "https://identity.test/oauth/token", UserInfoURL: "https://identity.test/api/me",
	}
	legacy.splitEndpoints()
	if legacy.EndpointBaseURL != "https://identity.test" ||
		legacy.AuthorizePath != "/oauth/authorize" {
		t.Fatal("旧完整端点地址未正确拆分")
	}
	partial := SSOConfig{AuthorizeURL: "/authorize", TokenURL: "/token"}
	partial.splitEndpoints()
	if partial.AuthorizePath != "/authorize" || partial.TokenPath != "/token" {
		t.Fatal("不完整旧配置应保持可编辑状态")
	}
}
