package docparse

import (
	"net/url"
	"path"
	"regexp"
	"strings"
)

var (
	inlineAssetPattern = regexp.MustCompile(
		`!?\[[^\]]*\]\(\s*(?:<([^>]+)>|([^\s)]+))(?:\s+(?:"[^"]*"|'[^']*'|\([^)]*\)))?\s*\)`,
	)
	referenceAssetPattern = regexp.MustCompile(
		`(?m)^\s{0,3}\[[^\]]+\]:\s*(?:<([^>]+)>|(\S+))`,
	)
)

// ExtractAssets 返回 Markdown 中引用的仓库内相对非 Markdown 资源路径。
func ExtractAssets(markdown, docPath string) []string {
	seen := make(map[string]struct{})
	var assets []string
	collect := func(candidate string) {
		assetPath, ok := normalizeAssetPath(docPath, candidate)
		if !ok {
			return
		}
		if _, exists := seen[assetPath]; exists {
			return
		}
		seen[assetPath] = struct{}{}
		assets = append(assets, assetPath)
	}

	for _, match := range inlineAssetPattern.FindAllStringSubmatch(markdown, -1) {
		if match[1] != "" {
			collect(match[1])
		} else {
			collect(match[2])
		}
	}
	for _, match := range referenceAssetPattern.FindAllStringSubmatch(markdown, -1) {
		if match[1] != "" {
			collect(match[1])
		} else {
			collect(match[2])
		}
	}
	return assets
}

func normalizeAssetPath(docPath, raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "/") ||
		strings.HasPrefix(raw, "//") || strings.ContainsRune(raw, '\x00') {
		return "", false
	}
	raw = strings.SplitN(raw, "#", 2)[0]
	raw = strings.SplitN(raw, "?", 2)[0]
	if decoded, err := url.PathUnescape(raw); err == nil {
		raw = decoded
	}
	raw = strings.ReplaceAll(raw, "\\", "/")
	if raw == "" {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return "", false
	}
	if strings.EqualFold(path.Ext(raw), ".md") {
		return "", false
	}
	resolved := path.Clean(path.Join(path.Dir(docPath), raw))
	if resolved == "." || resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", false
	}
	return resolved, true
}
