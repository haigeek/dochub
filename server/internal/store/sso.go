package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type SSOConfig struct {
	ID                     string     `json:"id"`
	Enabled                bool       `json:"enabled"`
	ProviderName           string     `json:"providerName"`
	EndpointBaseURL        string     `json:"endpointBaseUrl"`
	AuthorizePath          string     `json:"authorizePath"`
	TokenPath              string     `json:"tokenPath"`
	UserInfoPath           string     `json:"userInfoPath"`
	AuthorizeURL           string     `json:"-"`
	TokenURL               string     `json:"-"`
	UserInfoURL            string     `json:"-"`
	ClientID               string     `json:"clientId"`
	ClientSecretCiphertext string     `json:"-"`
	HasClientSecret        bool       `json:"hasClientSecret"`
	RedirectURI            string     `json:"redirectUri"`
	PortalURL              string     `json:"portalUrl"`
	Issuer                 string     `json:"issuer"`
	Scopes                 []string   `json:"scopes"`
	TokenAuthMethod        string     `json:"tokenAuthMethod"`
	PKCE                   bool       `json:"pkce"`
	SubjectField           string     `json:"subjectField"`
	UsernameField          string     `json:"usernameField"`
	UpdatedBy              string     `json:"updatedBy"`
	UpdatedAt              *time.Time `json:"updatedAt"`
}

func DefaultSSOConfig() SSOConfig {
	return SSOConfig{
		ProviderName: "统一身份认证", TokenAuthMethod: "client_secret_post",
		SubjectField: "sub", UsernameField: "name", Scopes: []string{},
	}
}

func (config SSOConfig) ProviderID() string {
	return config.ID
}

func (s *Store) ListSSOConfigs(ctx context.Context) ([]SSOConfig, error) {
	rows, err := s.DB.Query(ctx, ssoConfigSelect+` ORDER BY provider_name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	configs := make([]SSOConfig, 0)
	for rows.Next() {
		config, err := scanSSOConfig(rows)
		if err != nil {
			return nil, err
		}
		configs = append(configs, config)
	}
	return configs, rows.Err()
}

func (s *Store) SSOConfig(ctx context.Context, id string) (SSOConfig, error) {
	config, err := scanSSOConfig(s.DB.QueryRow(ctx, ssoConfigSelect+` WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return SSOConfig{}, ErrNotFound
	}
	return config, err
}

func (s *Store) SaveSSOConfig(ctx context.Context, config SSOConfig, clientSecretCiphertext, updatedBy string) error {
	if err := config.ResolveEndpoints(); err != nil {
		return err
	}
	scopes, err := json.Marshal(config.Scopes)
	if err != nil {
		return err
	}
	config.ID = strings.TrimSpace(config.ID)
	if config.ID == "" {
		return errors.New("单点登录站点 ID 不能为空")
	}
	_, err = s.DB.Exec(ctx, `
		INSERT INTO sso_settings (
			id, provider_id, enabled, provider_type, provider_name, endpoint_base_url,
			authorize_path, token_path, user_info_path, authorize_url, token_url,
			user_info_url, client_id, client_secret_ciphertext, redirect_uri, portal_url,
			issuer, scopes, token_auth_method, pkce, subject_field, username_field,
			updated_by, updated_at
		) VALUES ($1, $1, $2, 'generic', $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
			$13, $14, $15, $16, $17, $18, $19, $20, NULLIF($21, ''), NOW())
		ON CONFLICT (id) DO UPDATE SET
			provider_id = EXCLUDED.provider_id,
			enabled = EXCLUDED.enabled,
			provider_name = EXCLUDED.provider_name,
			endpoint_base_url = EXCLUDED.endpoint_base_url,
			authorize_path = EXCLUDED.authorize_path,
			token_path = EXCLUDED.token_path,
			user_info_path = EXCLUDED.user_info_path,
			authorize_url = EXCLUDED.authorize_url,
			token_url = EXCLUDED.token_url,
			user_info_url = EXCLUDED.user_info_url,
			client_id = EXCLUDED.client_id,
			client_secret_ciphertext = EXCLUDED.client_secret_ciphertext,
			redirect_uri = EXCLUDED.redirect_uri,
			portal_url = EXCLUDED.portal_url,
			issuer = EXCLUDED.issuer,
			scopes = EXCLUDED.scopes,
			token_auth_method = EXCLUDED.token_auth_method,
			pkce = EXCLUDED.pkce,
			subject_field = EXCLUDED.subject_field,
			username_field = EXCLUDED.username_field,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()`,
		config.ID, config.Enabled, config.ProviderName, config.EndpointBaseURL,
		config.AuthorizePath, config.TokenPath, config.UserInfoPath,
		config.AuthorizeURL, config.TokenURL, config.UserInfoURL, config.ClientID,
		clientSecretCiphertext, config.RedirectURI, config.PortalURL, config.Issuer,
		string(scopes), config.TokenAuthMethod, config.PKCE, config.SubjectField,
		config.UsernameField, strings.TrimSpace(updatedBy),
	)
	return err
}

func (s *Store) DeleteSSOConfig(ctx context.Context, id string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM sso_settings WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SSOClientSecret(ctx context.Context, id string) (string, error) {
	var ciphertext string
	err := s.DB.QueryRow(ctx, `SELECT client_secret_ciphertext FROM sso_settings WHERE id = $1`, id).Scan(&ciphertext)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && ciphertext == "") {
		return "", ErrNotFound
	}
	return ciphertext, err
}

const ssoConfigSelect = `
	SELECT id, enabled, provider_name, endpoint_base_url, authorize_path, token_path,
		user_info_path, authorize_url, token_url, user_info_url, client_id,
		client_secret_ciphertext, redirect_uri, portal_url, issuer, scopes,
		token_auth_method, pkce, subject_field, username_field,
		COALESCE(updated_by, ''), updated_at
	FROM sso_settings`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSSOConfig(row rowScanner) (SSOConfig, error) {
	config := DefaultSSOConfig()
	var scopes []byte
	err := row.Scan(
		&config.ID, &config.Enabled, &config.ProviderName, &config.EndpointBaseURL,
		&config.AuthorizePath, &config.TokenPath, &config.UserInfoPath,
		&config.AuthorizeURL, &config.TokenURL, &config.UserInfoURL, &config.ClientID,
		&config.ClientSecretCiphertext, &config.RedirectURI, &config.PortalURL,
		&config.Issuer, &scopes, &config.TokenAuthMethod, &config.PKCE,
		&config.SubjectField, &config.UsernameField, &config.UpdatedBy, &config.UpdatedAt,
	)
	if err != nil {
		return SSOConfig{}, err
	}
	if len(scopes) > 0 {
		_ = json.Unmarshal(scopes, &config.Scopes)
	}
	config.HasClientSecret = config.ClientSecretCiphertext != ""
	config.ClientSecretCiphertext = ""
	// 旧配置或保存后读取时补全派生端点；不完整配置的配置仍返回给管理员修正。
	config.splitEndpoints()
	_ = config.ResolveEndpoints()
	return config, nil
}
