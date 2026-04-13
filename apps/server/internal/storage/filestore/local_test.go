package filestore

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

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

func TestNewLocalStoreRejectsEmptyRoot(t *testing.T) {
	if _, err := NewLocalStore(" "); err == nil {
		t.Fatal("expected empty root to fail")
	}
}
