package filestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var ErrRootRequired = errors.New("存储根目录不能为空")

// StoredFile 描述一次原文件保存的结果。
type StoredFile struct {
	SHA256     string
	SizeBytes  int64
	StorageKey string
}

// LocalStore 把上传原文件保存到本地磁盘。
type LocalStore struct {
	root string
}

// NewLocalStore 创建一个基于本地目录的文件存储。
func NewLocalStore(root string) (*LocalStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, ErrRootRequired
	}

	return &LocalStore{root: root}, nil
}

// Save 使用内容寻址路径保存文件；相同内容会复用物理文件。
func (s *LocalStore) Save(_ context.Context, _ string, content []byte) (*StoredFile, error) {
	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	storageKey := path.Join(sha[:2], sha)
	fullPath := filepath.Join(s.root, filepath.FromSlash(storageKey))

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(fullPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(fullPath, content, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	return &StoredFile{
		SHA256:     sha,
		SizeBytes:  int64(len(content)),
		StorageKey: storageKey,
	}, nil
}

// Open 打开一个已保存的原文件。
func (s *LocalStore) Open(_ context.Context, storageKey string) (io.ReadCloser, error) {
	cleaned := path.Clean(strings.TrimSpace(storageKey))
	if cleaned == "." || cleaned == "" || cleaned == ".." || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) {
		return nil, os.ErrNotExist
	}

	return os.Open(filepath.Join(s.root, filepath.FromSlash(cleaned)))
}
