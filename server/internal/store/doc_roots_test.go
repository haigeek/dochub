package store

import "testing"

func TestDocRootsStorageCompatibility(t *testing.T) {
	raw, err := encodeDocRoots([]string{"docs", "design"})
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeDocRoots(raw)
	if len(decoded) != 2 || decoded[0] != "docs" || decoded[1] != "design" {
		t.Fatalf("多目录解码失败: %#v", decoded)
	}

	legacy := decodeDocRoots("docs")
	if len(legacy) != 1 || legacy[0] != "docs" {
		t.Fatalf("旧版单目录数据兼容失败: %#v", legacy)
	}
}
