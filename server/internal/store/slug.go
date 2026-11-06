package store

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode"
)

// ProjectSlug 从 SSH/HTTP 仓库地址提取最后一个路径段，生成可分享的路由标识。
func ProjectSlug(repoURL string) (string, error) {
	raw := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(repoURL), "/"))
	if raw == "" {
		return "", fmt.Errorf("仓库地址为空")
	}

	var candidate string
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("解析仓库地址失败: %w", err)
		}
		candidate = path.Base(strings.TrimSuffix(parsed.Path, "/"))
	} else {
		lastSlash := strings.LastIndex(raw, "/")
		lastColon := strings.LastIndex(raw, ":")
		separator := lastSlash
		if lastColon > separator {
			separator = lastColon
		}
		candidate = raw[separator+1:]
	}

	if decoded, err := url.PathUnescape(candidate); err == nil {
		candidate = decoded
	}
	candidate = strings.TrimSuffix(candidate, ".git")
	candidate = strings.ToLower(strings.TrimSpace(candidate))

	var builder strings.Builder
	lastWasDash := false
	for _, char := range candidate {
		if unicode.IsLetter(char) || unicode.IsNumber(char) || char == '_' || char == '.' {
			builder.WriteRune(char)
			lastWasDash = false
			continue
		}
		if !lastWasDash {
			builder.WriteByte('-')
			lastWasDash = true
		}
	}
	slug := strings.Trim(builder.String(), "-.")
	if slug == "" {
		return "", fmt.Errorf("无法从仓库地址生成项目路径")
	}
	return slug, nil
}

func uniqueSlug(base string, used map[string]bool) string {
	slug := base
	for suffix := 2; used[slug]; suffix++ {
		slug = fmt.Sprintf("%s-%d", base, suffix)
	}
	used[slug] = true
	return slug
}
