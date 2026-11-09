package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr             string
	DatabaseURL      string
	StorageDir       string
	SSHKeyPath       string
	KnownHostsPath   string
	GitHostAllowlist []string
	SessionTTL       time.Duration
	CookieSecure     bool
	AdminUsername    string
	AdminPassword    string
	WebDir           string
}

func Load() (Config, error) {
	cfg := Config{
		Addr:           env("APP_ADDR", ":8080"),
		DatabaseURL:    strings.TrimSpace(os.Getenv("DATABASE_URL")),
		StorageDir:     env("STORAGE_DIR", "./data"),
		SSHKeyPath:     strings.TrimSpace(os.Getenv("SSH_KEY_PATH")),
		KnownHostsPath: strings.TrimSpace(os.Getenv("SSH_KNOWN_HOSTS_PATH")),
		SessionTTL:     7 * 24 * time.Hour,
		AdminUsername:  strings.TrimSpace(env("ADMIN_USERNAME", "admin")),
		AdminPassword:  os.Getenv("ADMIN_PASSWORD"),
		WebDir:         env("WEB_DIR", "./web/dist"),
	}
	cfg.GitHostAllowlist = splitList(os.Getenv("GIT_HOST_ALLOWLIST"))
	cfg.CookieSecure = envBool("COOKIE_SECURE", false)
	if raw := os.Getenv("SESSION_TTL"); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("SESSION_TTL 格式错误: %w", err)
		}
		cfg.SessionTTL = duration
	}
	if cfg.AdminPassword == "" {
		return Config{}, fmt.Errorf("必须通过 ADMIN_PASSWORD 设置初始管理员密码")
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("必须通过 DATABASE_URL 设置外置 PostgreSQL 连接串")
	}
	if len(cfg.GitHostAllowlist) == 0 {
		return Config{}, fmt.Errorf("必须通过 GIT_HOST_ALLOWLIST 配置允许访问的 Git 主机")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func splitList(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}
