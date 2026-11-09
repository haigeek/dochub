package oauth2

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

type Token struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

type Client struct {
	cfg     Config
	adapter Adapter
}

func (client *Client) Option() Option {
	return Option{
		ID: client.cfg.ID, Name: client.cfg.Name,
		LoginURL:  "/api/auth/oauth2/" + client.cfg.ID + "/start",
		PortalURL: client.cfg.PortalURL, LoginHint: client.adapter.LoginHint(),
	}
}

func (client *Client) AuthorizationURL(state, verifier string) (string, error) {
	endpoint, err := url.Parse(client.cfg.AuthorizeURL)
	if err != nil {
		return "", Failure("unavailable")
	}
	query := endpoint.Query()
	query.Set("response_type", "code")
	query.Set("client_id", client.cfg.ClientID)
	query.Set("redirect_uri", client.cfg.RedirectURI)
	query.Set("state", state)
	if len(client.cfg.Scopes) > 0 {
		query.Set("scope", strings.Join(client.cfg.Scopes, " "))
	}
	if client.cfg.PKCE {
		sum := sha256.Sum256([]byte(verifier))
		query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
		query.Set("code_challenge_method", "S256")
	}
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func (client *Client) ExchangeIdentity(ctx context.Context, code, verifier, callbackIssuer string) (Identity, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
		// 拒绝重定向，避免客户端密钥和访问令牌被转发到其他地址。
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {client.cfg.RedirectURI},
	}
	if client.cfg.PKCE {
		form.Set("code_verifier", verifier)
	}
	if client.cfg.TokenAuthMethod == "client_secret_post" {
		form.Set("client_id", client.cfg.ClientID)
		form.Set("client_secret", client.cfg.ClientSecret)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Identity{}, Failure("unavailable")
	}
	if client.cfg.TokenAuthMethod == "client_secret_basic" {
		request.SetBasicAuth(url.QueryEscape(client.cfg.ClientID), url.QueryEscape(client.cfg.ClientSecret))
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, err := requestJSON(httpClient, request)
	if err != nil {
		return Identity{}, err
	}
	var token Token
	if err := json.Unmarshal(body, &token); err != nil ||
		token.AccessToken == "" || len(token.AccessToken) > 4096 ||
		strings.ContainsFunc(token.AccessToken, unicode.IsSpace) ||
		strings.ContainsFunc(token.AccessToken, unicode.IsControl) ||
		!strings.EqualFold(token.TokenType, "Bearer") {
		return Identity{}, Failure("invalid_identity")
	}
	if err := client.adapter.ValidateToken(token); err != nil {
		return Identity{}, err
	}
	request, err = http.NewRequestWithContext(ctx, http.MethodGet, client.cfg.UserInfoURL, nil)
	if err != nil {
		return Identity{}, Failure("unavailable")
	}
	request.Header.Set("Authorization", "Bearer "+token.AccessToken)
	body, err = requestJSON(httpClient, request)
	if err != nil {
		return Identity{}, err
	}
	identity, err := client.adapter.ParseIdentity(body, client.cfg, callbackIssuer)
	if err != nil {
		return Identity{}, err
	}
	if strings.TrimSpace(identity.Issuer) == "" || strings.TrimSpace(identity.Subject) == "" ||
		len(identity.Issuer) > 256 || len(identity.Subject) > 256 {
		return Identity{}, Failure("invalid_identity")
	}
	return identity, nil
}

func requestJSON(client *http.Client, request *http.Request) (json.RawMessage, error) {
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, Failure("unavailable")
	}
	defer response.Body.Close()
	const maxResponseSize = 1 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil || len(body) > maxResponseSize {
		return nil, Failure("unavailable")
	}
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 || response.StatusCode < 400 {
			return nil, Failure("unavailable")
		}
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &payload)
		return nil, AuthorizationFailure(payload.Error)
	}
	return body, nil
}

func AuthorizationFailure(code string) Failure {
	switch code {
	case "login_required":
		return Failure("login_required")
	case "invalid_grant", "invalid_token":
		return Failure("authorization_expired")
	default:
		return Failure("authorization_failed")
	}
}
