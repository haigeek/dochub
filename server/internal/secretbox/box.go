package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const keyFileName = ".dochub-config-key"

type Box struct {
	path string
	aead cipher.AEAD
	once sync.Once
	err  error
}

func New(storageDir string) *Box {
	return &Box{path: filepath.Join(storageDir, keyFileName)}
}

func (box *Box) Encrypt(value string) (string, error) {
	if err := box.init(); err != nil {
		return "", err
	}
	nonce := make([]byte, box.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := box.aead.Seal(nil, nonce, []byte(value), nil)
	payload := append(nonce, sealed...)
	return "v1:" + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (box *Box) Decrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if err := box.init(); err != nil {
		return "", err
	}
	if len(value) < 4 || value[:3] != "v1:" {
		return "", errors.New("不支持的密文格式")
	}
	payload, err := base64.RawURLEncoding.DecodeString(value[3:])
	if err != nil || len(payload) < box.aead.NonceSize() {
		return "", errors.New("密文格式无效")
	}
	nonce, ciphertext := payload[:box.aead.NonceSize()], payload[box.aead.NonceSize():]
	plaintext, err := box.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("密文校验失败")
	}
	return string(plaintext), nil
}

func (box *Box) init() error {
	box.once.Do(func() {
		if box.path == "" || box.path == keyFileName {
			box.err = errors.New("未配置密钥存储目录")
			return
		}
		if err := os.MkdirAll(filepath.Dir(box.path), 0o700); err != nil {
			box.err = fmt.Errorf("创建密钥目录失败: %w", err)
			return
		}
		key, err := readOrCreateKey(box.path)
		if err != nil {
			box.err = err
			return
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			box.err = err
			return
		}
		box.aead, box.err = cipher.NewGCM(block)
	})
	return box.err
}

func readOrCreateKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != 32 {
			return nil, errors.New("配置加密密钥长度错误")
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		// 两个实例同时首次启动时，以先写入的密钥为准。
		return readOrCreateKey(path)
	}
	if err != nil {
		return nil, err
	}
	if _, err = file.Write(key); err != nil {
		file.Close()
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	return key, nil
}
