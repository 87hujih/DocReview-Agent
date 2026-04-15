package postgrestest

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var schemaCounter atomic.Uint64

// PoolFactory 负责按隔离后的数据库地址创建连接池。
type PoolFactory func(ctx context.Context, databaseURL string) (*pgxpool.Pool, error)

// MigrationRunner 负责在隔离 schema 内执行迁移。
type MigrationRunner func(ctx context.Context, pool *pgxpool.Pool) error

// NewIsolatedPool 为单个测试创建独立 schema 的连接池，并自动执行迁移。
func NewIsolatedPool(
	t testing.TB,
	ctx context.Context,
	databaseURL string,
	namespace string,
	openPool PoolFactory,
	runMigrations MigrationRunner,
) *pgxpool.Pool {
	t.Helper()
	return newIsolatedPool(t, ctx, databaseURL, namespace, openPool, runMigrations)
}

// NewRawIsolatedPool 为需要自行控制迁移时机的测试创建独立 schema 连接池。
func NewRawIsolatedPool(
	t testing.TB,
	ctx context.Context,
	databaseURL string,
	namespace string,
	openPool PoolFactory,
) *pgxpool.Pool {
	t.Helper()
	return newIsolatedPool(t, ctx, databaseURL, namespace, openPool, nil)
}

func newIsolatedPool(
	t testing.TB,
	ctx context.Context,
	databaseURL string,
	namespace string,
	openPool PoolFactory,
	runMigrations MigrationRunner,
) *pgxpool.Pool {
	t.Helper()

	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("new admin pool: %v", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Fatalf("ping admin pool: %v", err)
	}

	schemaName := newSchemaName(namespace)
	if _, err := adminPool.Exec(ctx, `CREATE SCHEMA `+quoteIdentifier(schemaName)); err != nil {
		adminPool.Close()
		t.Fatalf("create isolated schema %q: %v", schemaName, err)
	}

	vectorSchema, err := ensureExtensionSchema(ctx, adminPool, "vector", "public")
	if err != nil {
		dropSchema(context.Background(), adminPool, schemaName)
		adminPool.Close()
		t.Fatalf("ensure vector extension schema: %v", err)
	}

	trigramSchema, err := ensureExtensionSchema(ctx, adminPool, "pg_trgm", "public")
	if err != nil {
		dropSchema(context.Background(), adminPool, schemaName)
		adminPool.Close()
		t.Fatalf("ensure pg_trgm extension schema: %v", err)
	}

	isolatedURL, err := withSearchPath(databaseURL, schemaName, vectorSchema, trigramSchema, "public")
	if err != nil {
		dropSchema(context.Background(), adminPool, schemaName)
		adminPool.Close()
		t.Fatalf("build isolated database url: %v", err)
	}

	if openPool == nil {
		dropSchema(context.Background(), adminPool, schemaName)
		adminPool.Close()
		t.Fatal("open pool factory is required")
	}

	pool, err := openPool(ctx, isolatedURL)
	if err != nil {
		dropSchema(context.Background(), adminPool, schemaName)
		adminPool.Close()
		t.Fatalf("new isolated pool: %v", err)
	}

	if runMigrations != nil {
		if err := runMigrations(ctx, pool); err != nil {
			pool.Close()
			dropSchema(context.Background(), adminPool, schemaName)
			adminPool.Close()
			t.Fatalf("run migrations in schema %q: %v", schemaName, err)
		}
	}

	t.Cleanup(func() {
		pool.Close()

		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := dropSchema(cleanupCtx, adminPool, schemaName); err != nil {
			t.Errorf("drop isolated schema %q: %v", schemaName, err)
		}

		adminPool.Close()
	})

	return pool
}

func newSchemaName(namespace string) string {
	sanitizedNamespace := sanitizeSchemaFragment(namespace)
	if sanitizedNamespace == "" {
		sanitizedNamespace = "pkg"
	}

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36) + strconv.FormatUint(schemaCounter.Add(1), 36)
	maxNamespaceLen := 63 - len("test__") - len(suffix)
	if maxNamespaceLen < 1 {
		maxNamespaceLen = 1
	}
	if len(sanitizedNamespace) > maxNamespaceLen {
		sanitizedNamespace = sanitizedNamespace[:maxNamespaceLen]
	}

	return "test_" + sanitizedNamespace + "_" + suffix
}

func sanitizeSchemaFragment(namespace string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(namespace) {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		default:
			if builder.Len() == 0 || strings.HasSuffix(builder.String(), "_") {
				continue
			}
			builder.WriteByte('_')
		}
	}

	return strings.Trim(builder.String(), "_")
}

func withSearchPath(databaseURL string, searchPath ...string) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", fmt.Errorf("parse database url: %w", err)
	}

	query := parsed.Query()
	query.Set("search_path", strings.Join(uniqueSearchPath(searchPath), ","))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func uniqueSearchPath(searchPath []string) []string {
	result := make([]string, 0, len(searchPath))
	seen := make(map[string]struct{}, len(searchPath))
	for _, schemaName := range searchPath {
		schemaName = strings.TrimSpace(schemaName)
		if schemaName == "" {
			continue
		}
		if _, ok := seen[schemaName]; ok {
			continue
		}
		seen[schemaName] = struct{}{}
		result = append(result, schemaName)
	}

	return result
}

func ensureExtensionSchema(
	ctx context.Context,
	adminPool *pgxpool.Pool,
	extensionName string,
	defaultSchema string,
) (string, error) {
	var schemaName string
	err := adminPool.QueryRow(ctx, `
		SELECT namespace.nspname
		FROM pg_extension extension
		JOIN pg_namespace namespace ON namespace.oid = extension.extnamespace
		WHERE extension.extname = $1
	`, extensionName).Scan(&schemaName)
	if err == nil {
		return schemaName, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}

	if _, err := adminPool.Exec(
		ctx,
		`CREATE EXTENSION IF NOT EXISTS `+quoteIdentifier(extensionName)+` WITH SCHEMA `+quoteIdentifier(defaultSchema),
	); err != nil {
		return "", err
	}

	return defaultSchema, nil
}

func dropSchema(ctx context.Context, adminPool *pgxpool.Pool, schemaName string) error {
	_, err := adminPool.Exec(ctx, `DROP SCHEMA IF EXISTS `+quoteIdentifier(schemaName)+` CASCADE`)
	return err
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
