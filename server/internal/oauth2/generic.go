package oauth2

import (
	"bytes"
	"encoding/json"
	"strings"
)

type GenericAdapter struct{}

func (GenericAdapter) LoginHint() string { return "" }

func (GenericAdapter) ValidateToken(token Token) error {
	if token.ExpiresIn < 0 {
		return Failure("invalid_identity")
	}
	return nil
}

func (GenericAdapter) ParseIdentity(body json.RawMessage, cfg Config, callbackIssuer string) (Identity, error) {
	var data map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if !json.Valid(body) || decoder.Decode(&data) != nil {
		return Identity{}, Failure("invalid_identity")
	}
	// 身份信息只取自后端固定端点；有签发方或受众字段时统一校验，不按厂商区分。
	issuer := cfg.Issuer
	if value, exists := data["iss"]; exists {
		sourceIssuer, ok := value.(string)
		if !ok || strings.TrimSpace(sourceIssuer) == "" || (issuer != "" && issuer != sourceIssuer) {
			return Identity{}, Failure("invalid_identity")
		}
		issuer = sourceIssuer
	}
	if issuer == "" {
		issuer = cfg.UserInfoURL
	}
	if callbackIssuer != "" && callbackIssuer != issuer {
		return Identity{}, Failure("invalid_identity")
	}
	if audience, exists := data["aud"]; exists {
		matches := false
		switch value := audience.(type) {
		case string:
			matches = value == cfg.ClientID
		case []any:
			for _, item := range value {
				if item == cfg.ClientID {
					matches = true
				}
			}
		}
		if !matches {
			return Identity{}, Failure("invalid_identity")
		}
	}
	return Identity{
		Issuer: issuer, Subject: mappedString(data, cfg.SubjectField),
		Username: mappedString(data, cfg.UsernameField),
	}, nil
}

func mappedString(data map[string]any, field string) string {
	var value any = data
	for _, key := range strings.Split(field, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = object[key]
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func newGenericProvider(cfg Config) (Provider, error) {
	if cfg.SubjectField == "" {
		cfg.SubjectField = "sub"
	}
	if cfg.UsernameField == "" {
		cfg.UsernameField = "name"
	}
	return NewClient(cfg, GenericAdapter{})
}
