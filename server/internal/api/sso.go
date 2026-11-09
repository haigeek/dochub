package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"dochub/internal/oauth2"
	"dochub/internal/store"
)

type ssoSettingsInput struct {
	store.SSOConfig
	ClientSecret      string `json:"clientSecret"`
	ClearClientSecret bool   `json:"clearClientSecret"`
}

type ssoProviderView struct {
	store.SSOConfig
	CallbackPath string `json:"callbackPath"`
}

func newSSOProviderView(settings store.SSOConfig) ssoProviderView {
	return ssoProviderView{SSOConfig: settings, CallbackPath: oauth2.CallbackPath(settings.ProviderID())}
}

func (s *Server) listSSOSettings(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	settings, err := s.db.ListSSOConfigs(c)
	if err != nil {
		internal(c, err)
		return
	}
	views := make([]ssoProviderView, 0, len(settings))
	for _, item := range settings {
		views = append(views, newSSOProviderView(item))
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"providers": views}})
}

func (s *Server) getSSOSettings(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	settings, err := s.db.SSOConfig(c, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		fail(c, http.StatusNotFound, "NOT_FOUND", "单点登录站点不存在", "")
		return
	}
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": newSSOProviderView(settings)})
}

func (s *Server) createSSOSettings(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	settings, input, ok := bindSSOSettings(c, "")
	if !ok {
		return
	}
	settings.ID = "sso-" + randomToken(9)
	s.saveSSOSettings(c, settings, input, true)
}

func (s *Server) updateSSOSettings(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	settings, input, ok := bindSSOSettings(c, c.Param("id"))
	if !ok {
		return
	}
	s.saveSSOSettings(c, settings, input, false)
}

func (s *Server) deleteSSOSettings(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	if err := s.db.DeleteSSOConfig(c, c.Param("id")); errors.Is(err, store.ErrNotFound) {
		fail(c, http.StatusNotFound, "NOT_FOUND", "单点登录站点不存在", "")
		return
	} else if err != nil {
		internal(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func bindSSOSettings(c *gin.Context, id string) (store.SSOConfig, ssoSettingsInput, bool) {
	var input ssoSettingsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_REQUEST", "单点登录配置格式错误", err.Error())
		return store.SSOConfig{}, input, false
	}
	settings := normalizeSSOSettings(input.SSOConfig)
	settings.ID = id
	if err := settings.ResolveEndpoints(); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_SSO_CONFIG", err.Error(), "")
		return store.SSOConfig{}, input, false
	}
	return settings, input, true
}

func (s *Server) saveSSOSettings(c *gin.Context, settings store.SSOConfig, input ssoSettingsInput, create bool) {
	redirectURI, err := normalizeSSORedirectURI(settings.RedirectURI, settings.ID)
	if err != nil {
		fail(c, http.StatusBadRequest, "INVALID_SSO_CONFIG", err.Error(), "")
		return
	}
	settings.RedirectURI = redirectURI
	_, lookupErr := s.db.SSOConfig(c, settings.ID)
	if create && lookupErr == nil {
		fail(c, http.StatusConflict, "SSO_EXISTS", "单点登录站点已存在", "")
		return
	}
	if !create && errors.Is(lookupErr, store.ErrNotFound) {
		fail(c, http.StatusNotFound, "NOT_FOUND", "单点登录站点不存在", "")
		return
	}
	if lookupErr != nil && !errors.Is(lookupErr, store.ErrNotFound) {
		internal(c, lookupErr)
		return
	}
	if input.ClientSecret != "" && len(input.ClientSecret) > 4096 {
		fail(c, http.StatusBadRequest, "INVALID_SSO_CONFIG", "客户端密钥长度不能超过 4096", "")
		return
	}
	existingCiphertext, err := s.db.SSOClientSecret(c, settings.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		internal(c, err)
		return
	}
	secret := ""
	if existingCiphertext != "" && !input.ClearClientSecret {
		secret, err = s.secrets.Decrypt(existingCiphertext)
		if err != nil {
			internal(c, err)
			return
		}
	}
	if input.ClientSecret != "" {
		secret = input.ClientSecret
	}
	if settings.Enabled {
		if secret == "" {
			fail(c, http.StatusBadRequest, "INVALID_SSO_CONFIG", "启用单点登录前必须配置客户端密钥", "")
			return
		}
		if _, err := oauth2.NewProvider(settingsToOAuth2(settings, secret)); err != nil {
			fail(c, http.StatusBadRequest, "INVALID_SSO_CONFIG", err.Error(), "")
			return
		}
	}
	ciphertext := existingCiphertext
	if secret != "" {
		ciphertext, err = s.secrets.Encrypt(secret)
		if err != nil {
			internal(c, err)
			return
		}
	}
	if input.ClearClientSecret {
		ciphertext = ""
	}
	if err := s.db.SaveSSOConfig(c, settings, ciphertext, currentUser(c).ID); err != nil {
		internal(c, err)
		return
	}
	saved, err := s.db.SSOConfig(c, settings.ID)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": newSSOProviderView(saved)})
}

func normalizeSSOSettings(settings store.SSOConfig) store.SSOConfig {
	if settings.ProviderName == "" {
		settings.ProviderName = "统一身份认证"
	}
	settings.ProviderName = strings.TrimSpace(settings.ProviderName)
	settings.EndpointBaseURL = strings.TrimRight(strings.TrimSpace(settings.EndpointBaseURL), "/")
	settings.AuthorizePath = strings.TrimSpace(settings.AuthorizePath)
	settings.TokenPath = strings.TrimSpace(settings.TokenPath)
	settings.UserInfoPath = strings.TrimSpace(settings.UserInfoPath)
	settings.ClientID = strings.TrimSpace(settings.ClientID)
	settings.RedirectURI = strings.TrimSpace(settings.RedirectURI)
	settings.PortalURL = strings.TrimSpace(settings.PortalURL)
	settings.Issuer = strings.TrimSpace(settings.Issuer)
	settings.TokenAuthMethod = strings.TrimSpace(settings.TokenAuthMethod)
	if settings.TokenAuthMethod == "" {
		settings.TokenAuthMethod = "client_secret_post"
	}
	settings.SubjectField = strings.TrimSpace(settings.SubjectField)
	if settings.SubjectField == "" {
		settings.SubjectField = "sub"
	}
	settings.UsernameField = strings.TrimSpace(settings.UsernameField)
	if settings.UsernameField == "" {
		settings.UsernameField = "name"
	}
	settings.Scopes = uniqueNonBlank(settings.Scopes)
	return settings
}

func normalizeSSORedirectURI(raw, id string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("回调地址必须为无查询串和 fragment 的 HTTP(S) 地址")
	}
	parsed.Path = oauth2.CallbackPath(id)
	return parsed.String(), nil
}

func uniqueNonBlank(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
