package docparse

import (
	"reflect"
	"testing"
)

func TestExtractAssets(t *testing.T) {
	markdown := `
![架构图](../images/arch.png "架构")
[接口说明](api.md)
[附件](./files/spec.pdf)
[外部链接](https://example.com/logo.png)
[页内链接](#section)
[引用图][diagram]

[diagram]: <../images/diagram.svg>
`
	got := ExtractAssets(markdown, "docs/guide/start.md")
	want := []string{
		"docs/images/arch.png",
		"docs/guide/files/spec.pdf",
		"docs/images/diagram.svg",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("资源索引错误:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestExtractAssetsRejectsEscapes(t *testing.T) {
	if assets := ExtractAssets("![x](../../secret.png)", "docs/start.md"); len(assets) != 0 {
		t.Fatalf("不应索引仓库外资源: %#v", assets)
	}
}
