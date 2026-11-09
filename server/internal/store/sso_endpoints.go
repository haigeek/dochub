package store

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

func (config *SSOConfig) ResolveEndpoints() error {
	config.EndpointBaseURL = strings.TrimRight(strings.TrimSpace(config.EndpointBaseURL), "/")
	paths := []*string{&config.AuthorizePath, &config.TokenPath, &config.UserInfoPath}
	endpoints := []*string{&config.AuthorizeURL, &config.TokenURL, &config.UserInfoURL}
	if config.EndpointBaseURL == "" {
		for _, path := range paths {
			if strings.TrimSpace(*path) != "" {
				return fmt.Errorf("请先填写端点基础地址")
			}
		}
		return nil
	}
	base, err := url.Parse(config.EndpointBaseURL)
	if err != nil || base.Hostname() == "" || base.User != nil || base.Opaque != "" ||
		(base.Scheme != "https" && base.Scheme != "http") ||
		base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || strings.Contains(config.EndpointBaseURL, "#") ||
		unsafeEndpointPath(base.Path) {
		return fmt.Errorf("端点基础地址必须为无凭据、查询串和 fragment 的 HTTP(S) 地址")
	}
	for index, path := range paths {
		*path = strings.TrimSpace(*path)
		if *path == "" {
			*endpoints[index] = ""
			continue
		}
		parsed, err := url.Parse(*path)
		if err != nil || !strings.HasPrefix(*path, "/") || parsed.IsAbs() || parsed.Host != "" ||
			parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
			unsafeEndpointPath(parsed.Path) || strings.Contains(*path, "#") {
			return fmt.Errorf("授权、Token 和用户信息 path 必须以 / 开头，不能包含域名、查询串或 fragment")
		}
		// 统一追加到基础地址，保留原其中的网关前缀，禁止 path 改写目标主机。
		*endpoints[index] = config.EndpointBaseURL + *path
	}
	return nil
}

func (config *SSOConfig) splitEndpoints() {
	if config.EndpointBaseURL == "" {
		for _, endpoint := range []string{config.AuthorizeURL, config.TokenURL, config.UserInfoURL} {
			if endpoint == "" {
				continue
			}
			parsed, err := url.Parse(endpoint)
			if err != nil || parsed.Host == "" {
				continue
			}
			config.EndpointBaseURL = parsed.Scheme + "://" + parsed.Host
			break
		}
	}
	config.EndpointBaseURL = strings.TrimRight(config.EndpointBaseURL, "/")
	endpoints := []string{config.AuthorizeURL, config.TokenURL, config.UserInfoURL}
	paths := []*string{&config.AuthorizePath, &config.TokenPath, &config.UserInfoPath}
	for index, endpoint := range endpoints {
		if endpoint == "" {
			*paths[index] = ""
			continue
		}
		if config.EndpointBaseURL != "" && strings.HasPrefix(endpoint, config.EndpointBaseURL+"/") {
			*paths[index] = strings.TrimPrefix(endpoint, config.EndpointBaseURL)
			continue
		}
		// 不完整或跨主机旧配置保持原值供管理员修正，加载时不阻断配置页。
		*paths[index] = endpoint
	}
}

func unsafeEndpointPath(path string) bool {
	if strings.HasPrefix(path, "//") || strings.Contains(path, "\\") ||
		strings.ContainsFunc(path, unicode.IsControl) {
		return true
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}
