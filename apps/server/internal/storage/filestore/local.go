package filestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	absPath, err := s.safePath(storageKey)
	if err != nil {
		return nil, err
	}
	return os.Open(absPath)
}

// Stat 返回已保存原文件的元信息，用于设置 Content-Length 等响应头。
func (s *LocalStore) Stat(_ context.Context, storageKey string) (os.FileInfo, error) {
	absPath, err := s.safePath(storageKey)
	if err != nil {
		return nil, err
	}
	return os.Stat(absPath)
}

// safePath 校验 storageKey 合法性并返回 root 内的绝对路径。
// 拒绝空 key、绝对路径、包含反斜杠的 key，以及通过路径规范化后逃逸 root 的 key。
func (s *LocalStore) safePath(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || key == "." || key == ".." {
		return "", fmt.Errorf("文件 key 非法: %q", key)
	}
	if filepath.IsAbs(key) {
		return "", fmt.Errorf("文件 key 不能是绝对路径: %q", key)
	}
	// 拒绝包含反斜杠，防止 Windows 路径风格逃逸（如 ..\secret.txt）
	if strings.ContainsRune(key, '\\') {
		return "", fmt.Errorf("文件 key 包含非法字符: %q", key)
	}

	fullPath := filepath.Join(s.root, key)

	absRoot, err := filepath.Abs(s.root)
	if err != nil {
		return "", fmt.Errorf("解析存储根目录失败: %w", err)
	}
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("解析文件路径失败: %w", err)
	}

	// 最终 containment 校验：确保解析后的路径在 root 目录内
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("文件 key 超出存储目录: %q", key)
	}

	return absPath, nil
}
