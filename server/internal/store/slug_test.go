package store

import "testing"

func TestProjectSlug(t *testing.T) {
	tests := map[string]string{
		"git@git.example.com:team/docs.git":              "docs",
		"ssh://git@git.example.com:2222/team/DocHub.git": "dochub",
		"https://git.example.com/team/My Docs.git":       "my-docs",
		"git@git.example.com:team/中文仓库.git":              "中文仓库",
	}
	for repoURL, expected := range tests {
		actual, err := ProjectSlug(repoURL)
		if err != nil {
			t.Fatalf("%s: %v", repoURL, err)
		}
		if actual != expected {
			t.Fatalf("%s: expected %q, got %q", repoURL, expected, actual)
		}
	}
}
