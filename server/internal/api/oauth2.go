package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"

	"dochub/internal/oauth2"
	"dochub/internal/store"
)

const oauthTransactionTTL = 10 * time.Minute

func (s *Server) authOptions(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	settings, err := s.db.ListSSOConfigs(c)
	if err != nil {
		fail(c, http.StatusServiceUnavailable, "SSO_UNAVAILABLE", "单点登录配置不可用", "")
		return
	}
	options := make([]oauth2.Option, 0, len(settings))
	for _, settings := range settings {
		if !settings.Enabled {
			continue
		}
		// 登录入口只取决于站点是否启用，密钥与认证配置在发起登录时校验。
		name := settings.ProviderName
		if name == "" {
			name = settings.AuthKey
		}
		options = append(options, oauth2.Option{
			ID: settings.AuthKey, Name: name,
			LoginURL:  "/api/auth/oauth2/" + settings.AuthKey + "/start",
			PortalURL: settings.PortalURL,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"providers": options}})
}

func (s *Server) oauthStart(c *gin.Context) {
	provider, settings, err := s.oauthProvider(c.Request.Context(), c.Param("provider"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "NOT_FOUND", "登录方式未启用", "")
			return
		}
		log.Printf("单点登录站点 %q 初始化失败: %v", c.Param("provider"), err)
		s.oauthError(c, "unavailable", c.Query("redirect"))
		return
	}
	c.Header("Cache-Control", "no-store")
	state, verifier := randomToken(32), randomToken(32)
	id := settings.ProviderID()
	returnPath := safeReturnPath(c.Query("redirect"))
	if err := s.db.CreateOAuthLogin(c, hashToken(state), id, verifier, returnPath, time.Now().Add(oauthTransactionTTL)); err != nil {
		s.oauthError(c, "unavailable", returnPath)
		return
	}
	// Cookie 按 provider 隔离；路径同时覆盖标准入口和兼容入口，允许不同服务并行登录。
	http.SetCookie(c.Writer, &http.Cookie{
		Name: oauthStateCookie(id), Value: state, Path: "/api/auth",
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
		MaxAge: int(oauthTransactionTTL.Seconds()), Expires: time.Now().Add(oauthTransactionTTL),
	})
	location, err := provider.AuthorizationURL(state, verifier)
	if err != nil {
		s.oauthError(c, "unavailable", returnPath)
		return
	}
	c.Redirect(http.StatusFound, location)
}

func (s *Server) oauthCallback(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	provider, settings, err := s.oauthProvider(c.Request.Context(), c.Param("provider"))
	if err != nil {
		fail(c, http.StatusNotFound, "NOT_FOUND", "登录方式未启用", "")
		return
	}
	id := settings.ProviderID()
	http.SetCookie(c.Writer, &http.Cookie{
		Name: oauthStateCookie(id), Path: "/api/auth", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		s.oauthError(c, "invalid_state", "")
		return
	}
	state := query.Get("state")
	browserState, cookieErr := c.Cookie(oauthStateCookie(id))
	if cookieErr != nil || len(state) != 64 || len(query["state"]) != 1 ||
		subtle.ConstantTimeCompare([]byte(state), []byte(browserState)) != 1 {
		s.oauthError(c, "invalid_state", "")
		return
	}
	// 消费时校验 provider，避免将其他服务签发的登录事务用于当前回调。
	transaction, err := s.db.ConsumeOAuthLogin(c, hashToken(state), id)
	if err != nil {
		s.oauthError(c, "invalid_state", "")
		return
	}
	if len(query["error"]) > 0 {
		s.oauthError(c, string(oauth2.AuthorizationFailure(query.Get("error"))), transaction.ReturnPath)
		return
	}
	code := query.Get("code")
	if len(query["code"]) != 1 || code == "" || len(code) > 2048 || len(query["iss"]) > 1 ||
		(len(query["iss"]) == 1 && strings.TrimSpace(query.Get("iss")) == "") {
		s.oauthError(c, "authorization_failed", transaction.ReturnPath)
		return
	}
	identity, err := provider.ExchangeIdentity(c.Request.Context(), code, transaction.Verifier, query.Get("iss"))
	if err != nil {
		errorCode := "unavailable"
		var failure oauth2.Failure
		if errors.As(err, &failure) {
			errorCode = string(failure)
		}
		s.oauthError(c, errorCode, transaction.ReturnPath)
		return
	}
	user, err := s.db.OAuthUser(c, id, identity.Issuer, identity.Subject, identity.Username)
	if err != nil {
		s.oauthError(c, "unavailable", transaction.ReturnPath)
		return
	}
	if _, err = s.establishSession(c, user.ID); err != nil {
		s.oauthError(c, "unavailable", transaction.ReturnPath)
		return
	}
	c.Redirect(http.StatusFound, safeReturnPath(transaction.ReturnPath))
}

func (s *Server) oauthProvider(ctx context.Context, authKey string) (oauth2.Provider, store.SSOConfig, error) {
	settings, err := s.db.SSOConfigByAuthKey(ctx, authKey)
	if err != nil {
		return nil, store.SSOConfig{}, err
	}
	if !settings.Enabled {
		return nil, store.SSOConfig{}, store.ErrNotFound
	}
	ciphertext, err := s.db.SSOClientSecret(ctx, settings.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, store.SSOConfig{}, errors.New("已启用站点未配置客户端密钥")
	}
	if err != nil {
		return nil, store.SSOConfig{}, err
	}
	secret, err := s.secrets.Decrypt(ciphertext)
	if err != nil {
		return nil, store.SSOConfig{}, err
	}
	provider, err := oauth2.NewProvider(settingsToOAuth2(settings, secret))
	if err != nil {
		return nil, store.SSOConfig{}, err
	}
	return provider, settings, nil
}

func settingsToOAuth2(settings store.SSOConfig, clientSecret string) oauth2.Config {
	return oauth2.Config{
		ID: settings.ProviderID(), AuthKey: settings.AuthKey, Name: settings.ProviderName,
		AuthorizeURL: settings.AuthorizeURL,
		TokenURL:     settings.TokenURL, UserInfoURL: settings.UserInfoURL,
		ClientID: settings.ClientID, ClientSecret: clientSecret,
		RedirectURI: settings.RedirectURI, PortalURL: settings.PortalURL,
		Issuer: settings.Issuer, Scopes: settings.Scopes,
		TokenAuthMethod: settings.TokenAuthMethod, PKCE: settings.PKCE,
		SubjectField: settings.SubjectField, UsernameField: settings.UsernameField,
	}
}

func (s *Server) oauthError(c *gin.Context, errorCode, returnPath string) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	query := url.Values{"oauthError": {errorCode}, "redirect": {safeReturnPath(returnPath)}}
	c.Redirect(http.StatusFound, "/login?"+query.Encode())
}

func oauthStateCookie(id string) string { return "dochub_oauth_state_" + id }

func safeReturnPath(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || !strings.HasPrefix(raw, "/") ||
		strings.HasPrefix(raw, "//") || parsed.IsAbs() || parsed.Host != "" || parsed.Opaque != "" ||
		strings.ContainsAny(raw, "\\\r\n") || strings.ContainsAny(parsed.Path, "\\\r\n") ||
		strings.ContainsFunc(raw, unicode.IsControl) || strings.ContainsFunc(parsed.Path, unicode.IsControl) ||
		strings.HasPrefix(parsed.Path, "//") || strings.HasPrefix(parsed.Path, "/api/") ||
		strings.HasPrefix(path.Clean(parsed.Path), "/api/") || path.Clean(parsed.Path) == "/api" ||
		path.Clean(parsed.Path) == "/login" {
		return "/projects"
	}
	return raw
}
