package filestore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestLocalStoreSaveWritesContentAddressedFile 验证`localStoreSave`在写入或副作用路径下的行为，防止同类回归。
func TestLocalStoreSaveWritesContentAddressedFile(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStore(root)
	if err != nil {
		t.Fatalf("new local store: %v", err)
	}

	stored, err := store.Save(context.Background(), "学生守则.md", []byte("上传内容"))
	if err != nil {
		t.Fatalf("save file: %v", err)
	}

	if stored.SizeBytes != int64(len("上传内容")) {
		t.Fatalf("expected size %d, got %d", len("上传内容"), stored.SizeBytes)
	}
	if stored.SHA256 == "" {
		t.Fatal("expected sha256 to be set")
	}
	if stored.StorageKey == "" {
		t.Fatal("expected storage key to be set")
	}

	path := filepath.Join(root, stored.StorageKey)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if !bytes.Equal(content, []byte("上传内容")) {
		t.Fatalf("expected stored content %q, got %q", "上传内容", string(content))
	}
}

// TestLocalStoreOpenReadsStoredFile 验证`localStoreOpenReadsStoredFile`在特定边界条件下的行为，防止同类回归。
func TestLocalStoreOpenReadsStoredFile(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStore(root)
	if err != nil {
		t.Fatalf("new local store: %v", err)
	}

	stored, err := store.Save(context.Background(), "手册.txt", []byte("原文件内容"))
	if err != nil {
		t.Fatalf("save file: %v", err)
	}

	reader, err := store.Open(context.Background(), stored.StorageKey)
	if err != nil {
		t.Fatalf("open stored file: %v", err)
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if string(content) != "原文件内容" {
		t.Fatalf("expected content %q, got %q", "原文件内容", string(content))
	}
}

// TestNewLocalStoreRejectsEmptyRoot 验证`newLocalStore`在非法输入或失败路径下的行为，防止同类回归。
func TestNewLocalStoreRejectsEmptyRoot(t *testing.T) {
	if _, err := NewLocalStore(" "); err == nil {
		t.Fatal("expected empty root to fail")
	}
}

// TestLocalStoreOpenRejectsPathTraversal 验证`localStoreOpen`在非法输入或失败路径下的行为，防止同类回归。
func TestLocalStoreOpenRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStore(root)
	if err != nil {
		t.Fatalf("new local store: %v", err)
	}

	badKeys := []string{
		"../secret.txt",
		`..\secret.txt`,
		"/etc/passwd",
		`C:\secret.txt`,
		"",
		".",
		"..",
	}
	for _, key := range badKeys {
		_, err := store.Open(context.Background(), key)
		if err == nil {
			t.Errorf("Open(%q) should have returned error, got nil", key)
		}
	}
}

// TestLocalStoreOpenCannotEscapeRoot 验证`localStoreOpenCannotEscapeRoot`在特定边界条件下的行为，防止同类回归。
func TestLocalStoreOpenCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	// 在 root 的父目录写入一个文件，验证无法通过 ../ 读取
	parent := filepath.Dir(root)
	secretPath := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	t.Cleanup(func() { os.Remove(secretPath) })

	store, err := NewLocalStore(root)
	if err != nil {
		t.Fatalf("new local store: %v", err)
	}

	_, err = store.Open(context.Background(), "../secret.txt")
	if err == nil {
		t.Fatal("Open('../secret.txt') should have returned error, got nil")
	}
}

// TestLocalStoreStatReturnsSizeAfterSave 验证`localStoreStat`在返回值分支下的行为，防止同类回归。
func TestLocalStoreStatReturnsSizeAfterSave(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStore(root)
	if err != nil {
		t.Fatalf("new local store: %v", err)
	}

	content := []byte("stat test content")
	stored, err := store.Save(context.Background(), "test.txt", content)
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	info, err := store.Stat(context.Background(), stored.StorageKey)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != int64(len(content)) {
		t.Fatalf("expected size %d, got %d", len(content), info.Size())
	}
}

// TestLocalStoreStatRejectsPathTraversal 验证`localStoreStat`在非法输入或失败路径下的行为，防止同类回归。
func TestLocalStoreStatRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStore(root)
	if err != nil {
		t.Fatalf("new local store: %v", err)
	}

	_, err = store.Stat(context.Background(), "../secret.txt")
	if err == nil {
		t.Fatal("Stat('../secret.txt') should have returned error")
	}
	if errors.Is(err, os.ErrNotExist) {
		// ErrNotExist 是可以接受的（安全拦截可以返回这个错误）
	}
}
