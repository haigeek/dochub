package oauth2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

type Config struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	AuthorizeURL    string   `json:"authorizeUrl"`
	TokenURL        string   `json:"tokenUrl"`
	UserInfoURL     string   `json:"userInfoUrl"`
	ClientID        string   `json:"clientId"`
	ClientSecret    string   `json:"clientSecret"`
	RedirectURI     string   `json:"redirectUri"`
	PortalURL       string   `json:"portalUrl"`
	Issuer          string   `json:"issuer"`
	Scopes          []string `json:"scopes"`
	TokenAuthMethod string   `json:"tokenAuthMethod"`
	PKCE            bool     `json:"pkce"`
	SubjectField    string   `json:"subjectField"`
	UsernameField   string   `json:"usernameField"`
}

type Identity struct {
	Issuer   string
	Subject  string
	Username string
}

type Option struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	LoginURL  string `json:"loginUrl"`
	PortalURL string `json:"portalUrl,omitempty"`
	LoginHint string `json:"loginHint,omitempty"`
}

// Provider 封装身份服务差异，业务路由和本地会话只依赖统一身份模型。
type Provider interface {
	Option() Option
	AuthorizationURL(state, verifier string) (string, error)
	ExchangeIdentity(ctx context.Context, code, verifier, callbackIssuer string) (Identity, error)
}

// Adapter 是新增身份服务的扩展点；传输、凭据和授权码交换由 Client 复用。
type Adapter interface {
	ParseIdentity(body json.RawMessage, cfg Config, callbackIssuer string) (Identity, error)
	ValidateToken(token Token) error
	LoginHint() string
}

type Failure string

func (failure Failure) Error() string { return string(failure) }

func CallbackPath(id string) string { return "/api/auth/oauth2/" + id + "/callback" }

func NewProvider(cfg Config) (Provider, error) {
	return newGenericProvider(cfg)
}

func NewClient(cfg Config, adapter Adapter) (*Client, error) {
	if cfg.Name == "" {
		cfg.Name = cfg.ID
	}
	if cfg.TokenAuthMethod == "" {
		cfg.TokenAuthMethod = "client_secret_post"
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if adapter == nil {
		return nil, fmt.Errorf("OAuth2 Adapter 必填")
	}
	return &Client{cfg: cfg, adapter: adapter}, nil
}

func validateConfig(cfg Config) error {
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`).MatchString(cfg.ID) {
		return fmt.Errorf("OAuth2 Provider id 必须为小写字母开头的字母、数字或连字符，最多 32 位")
	}
	if strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
		return fmt.Errorf("OAuth2 Provider 必须配置 clientId 和 clientSecret")
	}
	if cfg.TokenAuthMethod != "client_secret_post" && cfg.TokenAuthMethod != "client_secret_basic" {
		return fmt.Errorf("OAuth2 tokenAuthMethod 只支持 client_secret_post 或 client_secret_basic")
	}
	for name, raw := range map[string]string{
		"authorizeUrl": cfg.AuthorizeURL, "tokenUrl": cfg.TokenURL,
		"userInfoUrl": cfg.UserInfoURL, "redirectUri": cfg.RedirectURI, "portalUrl": cfg.PortalURL,
	} {
		if name == "portalUrl" && raw == "" {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Hostname() == "" || parsed.User != nil ||
			(parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Fragment != "" ||
			parsed.RawQuery != "" || parsed.ForceQuery {
			return fmt.Errorf("OAuth2 %s 必须是无凭据、查询串和 fragment 的 HTTP(S) 地址", name)
		}
	}
	return nil
}
