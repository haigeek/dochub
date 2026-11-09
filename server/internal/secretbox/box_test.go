package secretbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBoxRoundTrip(t *testing.T) {
	dir := t.TempDir()
	box := New(dir)
	encrypted, err := box.Encrypt("client-secret")
	if err != nil {
		t.Fatal(err)
	}
	if encrypted == "client-secret" {
		t.Fatal("密钥未加密")
	}
	decrypted, err := New(dir).Decrypt(encrypted)
	if err != nil || decrypted != "client-secret" {
		t.Fatalf("解密失败: %q %v", decrypted, err)
	}
	info, err := os.Stat(filepath.Join(dir, keyFileName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("密钥文件权限错误: %v %v", info, err)
	}
	if _, err := box.Decrypt("v1:broken"); err == nil {
		t.Fatal("错误密文未被拒绝")
	}
}

func TestBoxSeparatesStorageDirectories(t *testing.T) {
	first, second := New(t.TempDir()), New(t.TempDir())
	encrypted, err := first.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Decrypt(encrypted); err == nil {
		t.Fatal("不同存储密钥不应能互相解密")
	}
}
