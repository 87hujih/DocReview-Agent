package postgres

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestLoadMigrationsSortsAndUsesCanonicalChecksums 验证对应场景下的正常路径与失败路径。
func TestLoadMigrationsSortsAndUsesCanonicalChecksums(t *testing.T) {
	loaded, err := loadMigrations(fstest.MapFS{
		"migrations/002_second.sql": {Data: []byte("SELECT 2;\r\n")},
		"migrations/001_first.sql":  {Data: []byte("\xEF\xBB\xBFSELECT 1;\r\n")},
		"migrations/README.md":      {Data: []byte("ignored")},
	}, "migrations")
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 migrations, got %d", len(loaded))
	}
	if loaded[0].Name != "001_first.sql" || loaded[1].Name != "002_second.sql" {
		t.Fatalf("expected sorted migrations, got %#v", loaded)
	}
	if loaded[0].SQL != "SELECT 1;\n" {
		t.Fatalf("expected BOM and CRLF normalization, got %q", loaded[0].SQL)
	}

	lfOnly, err := loadMigrations(fstest.MapFS{
		"migrations/001_first.sql": {Data: []byte("SELECT 1;\n")},
	}, "migrations")
	if err != nil {
		t.Fatalf("load LF migration: %v", err)
	}
	if loaded[0].Checksum != lfOnly[0].Checksum {
		t.Fatal("expected checksum to be stable across line endings")
	}
}

// TestPlanMigrationsReturnsOnlyPendingInOrder 验证对应场景下的正常路径与失败路径。
func TestPlanMigrationsReturnsOnlyPendingInOrder(t *testing.T) {
	available := []migration{
		{Name: "001_first.sql", Checksum: strings.Repeat("a", 64)},
		{Name: "002_second.sql", Checksum: strings.Repeat("b", 64)},
	}

	pending, err := planMigrations(available, map[string]string{
		"001_first.sql": strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatalf("plan migrations: %v", err)
	}
	if len(pending) != 1 || pending[0].Name != "002_second.sql" {
		t.Fatalf("expected only second migration pending, got %#v", pending)
	}
}

// TestPlanMigrationsRejectsChecksumDrift 验证对应场景下的正常路径与失败路径。
func TestPlanMigrationsRejectsChecksumDrift(t *testing.T) {
	available := []migration{{Name: "001_first.sql", Checksum: strings.Repeat("a", 64)}}

	_, err := planMigrations(available, map[string]string{
		"001_first.sql": strings.Repeat("b", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

// TestPlanMigrationsRejectsMissingAppliedFile 验证对应场景下的正常路径与失败路径。
func TestPlanMigrationsRejectsMissingAppliedFile(t *testing.T) {
	_, err := planMigrations(nil, map[string]string{
		"001_removed.sql": strings.Repeat("a", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "文件缺失") {
		t.Fatalf("expected missing applied migration error, got %v", err)
	}
}

// TestPlanMigrationsRejectsDuplicateNames 验证对应场景下的正常路径与失败路径。
func TestPlanMigrationsRejectsDuplicateNames(t *testing.T) {
	available := []migration{
		{Name: "001_same.sql", Checksum: strings.Repeat("a", 64)},
		{Name: "001_same.sql", Checksum: strings.Repeat("a", 64)},
	}

	_, err := planMigrations(available, nil)
	if err == nil || !strings.Contains(err.Error(), "名称重复") {
		t.Fatalf("expected duplicate migration error, got %v", err)
	}
}
