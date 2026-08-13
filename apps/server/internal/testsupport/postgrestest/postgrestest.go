package postgrestest

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var schemaCounter atomic.Uint64

const (
	allowDBTestsEnv              = "ALLOW_DB_TESTS"
	testDatabaseURLEnv           = "TEST_DATABASE_URL"
	testDatabaseHostAllowlistEnv = "TEST_DATABASE_HOST_ALLOWLIST"
)

// PoolFactory 负责按隔离后的数据库地址创建连接池。
type PoolFactory func(ctx context.Context, databaseURL string) (*pgxpool.Pool, error)

// MigrationRunner 负责在隔离 schema 内执行迁移。
type MigrationRunner func(ctx context.Context, pool *pgxpool.Pool) error

// NewIsolatedPool 为单个测试创建独立 schema 的连接池，并自动执行迁移。
func NewIsolatedPool(
	t testing.TB,
	ctx context.Context,
	namespace string,
	openPool PoolFactory,
	runMigrations MigrationRunner,
) *pgxpool.Pool {
	t.Helper()
	return newIsolatedPool(t, ctx, namespace, openPool, runMigrations)
}

// NewRawIsolatedPool 为需要自行控制迁移时机的测试创建独立 schema 连接池。
func NewRawIsolatedPool(
	t testing.TB,
	ctx context.Context,
	namespace string,
	openPool PoolFactory,
) *pgxpool.Pool {
	t.Helper()
	return newIsolatedPool(t, ctx, namespace, openPool, nil)
}

// newIsolatedPool 创建IsolatedPool，并补齐当前链路需要的默认依赖和缺省行为。
func newIsolatedPool(
	t testing.TB,
	ctx context.Context,
	namespace string,
	openPool PoolFactory,
	runMigrations MigrationRunner,
) *pgxpool.Pool {
	t.Helper()
	if strings.TrimSpace(os.Getenv(testDatabaseURLEnv)) == "" {
		t.Skip("database tests require explicit TEST_DATABASE_URL")
	}

	adminPool, databaseURL, err := openValidatedPool(ctx, os.LookupEnv, pgxpool.New)
	if err != nil {
		t.Fatalf("unsafe database test configuration: %v", err)
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

// 处理失败： openValidatedPool is the single gate for every PostgreSQL test connection.
// Validation completes 之前 the 注入的 connection factory can 运行. It intentionally
// reads only the supplied process-environment 查询 和 never invokes application config
// 或 dotenv loading.
func openValidatedPool(
	ctx context.Context,
	lookup func(string) (string, bool),
	openPool PoolFactory,
) (*pgxpool.Pool, string, error) {
	databaseURL, err := validateDatabaseTestEnvironment(lookup)
	if err != nil {
		return nil, "", err
	}
	if openPool == nil {
		return nil, "", fmt.Errorf("数据库 connection factory 不能为空")
	}

	pool, err := openPool(ctx, databaseURL)
	if err != nil {
		return nil, "", fmt.Errorf("打开 test 数据库 pool：%w", err)
	}
	return pool, databaseURL, nil
}

// validateDatabaseTestEnvironment validates every safety predicate 不包含 opening a network connection.
func validateDatabaseTestEnvironment(lookup func(string) (string, bool)) (string, error) {
	if value, _ := lookup(allowDBTestsEnv); strings.TrimSpace(value) != "1" {
		return "", fmt.Errorf("处理失败：%s 必须为 set 用于 1", allowDBTestsEnv)
	}

	databaseURL, _ := lookup(testDatabaseURLEnv)
	databaseURL = strings.TrimSpace(databaseURL)
	if databaseURL == "" {
		return "", fmt.Errorf("处理失败：%s 必须为 set", testDatabaseURLEnv)
	}

	allowedHostsValue, _ := lookup(testDatabaseHostAllowlistEnv)
	allowedHosts := parseHostAllowlist(allowedHostsValue)
	if len(allowedHosts) == 0 {
		return "", fmt.Errorf("处理失败：%s 必须包含至少一个 host", testDatabaseHostAllowlistEnv)
	}

	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Hostname() == "" {
		return "", fmt.Errorf("%s 必须为一个 PostgreSQL URL 包含一个明确的 host", testDatabaseURLEnv)
	}
	for _, key := range []string{"host", "hostaddr", "port", "dbname", "database", "user", "password", "service", "servicefile", "passfile"} {
		if parsed.Query().Has(key) {
			return "", fmt.Errorf("处理失败：%s 必须 not 包含 connection-routing override parameters", testDatabaseURLEnv)
		}
	}

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return "", fmt.Errorf("处理失败：%s 必须为一个有效的 PostgreSQL connection URL", testDatabaseURLEnv)
	}

	databaseName := poolConfig.ConnConfig.Database
	if databaseName == "" || strings.Contains(databaseName, "/") || !strings.HasSuffix(databaseName, "_test") {
		return "", fmt.Errorf("%s 数据库 name 必须 end 位于 _test", testDatabaseURLEnv)
	}

	if _, ok := allowedHosts[normalizeHost(poolConfig.ConnConfig.Host)]; !ok {
		return "", fmt.Errorf("处理失败：%s host 为 not present 位于 %s", testDatabaseURLEnv, testDatabaseHostAllowlistEnv)
	}
	for _, fallback := range poolConfig.ConnConfig.Fallbacks {
		if _, ok := allowedHosts[normalizeHost(fallback.Host)]; !ok {
			return "", fmt.Errorf("处理失败：%s fallback host 为 not present 位于 %s", testDatabaseURLEnv, testDatabaseHostAllowlistEnv)
		}
	}

	return databaseURL, nil
}

// parseHostAllowlist 解析输入并返回类型化结果。
func parseHostAllowlist(value string) map[string]struct{} {
	hosts := make(map[string]struct{})
	for _, host := range strings.Split(value, ",") {
		host = normalizeHost(host)
		if host != "" {
			hosts[host] = struct{}{}
		}
	}
	return hosts
}

// normalizeHost 执行该函数负责的核心处理逻辑。
func normalizeHost(host string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
}

// newSchemaName 创建SchemaName，并补齐当前链路需要的默认依赖和缺省行为。
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

// sanitizeSchemaFragment 把测试 schema 名片段清洗成 PostgreSQL 可接受的安全标识符。
func sanitizeSchemaFragment(namespace string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(namespace) {
		// 根据当前状态或类型选择对应的处理分支。
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

// withSearchPath 返回一个选项函数，用于为当前注入 `搜索路径` 相关依赖。
func withSearchPath(databaseURL string, searchPath ...string) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", fmt.Errorf("parse 数据库 url：%w", err)
	}

	query := parsed.Query()
	query.Set("search_path", strings.Join(uniqueSearchPath(searchPath), ","))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// uniqueSearchPath 生成带唯一 schema 的 search_path，避免并发测试互相污染。
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

// ensureExtensionSchema 确保测试库中的扩展 schema 存在，便于隔离 schema 复用 pgcrypto 等扩展。
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

// dropSchema 删除测试用 schema 及其对象，确保用例结束后清理干净。
func dropSchema(ctx context.Context, adminPool *pgxpool.Pool, schemaName string) error {
	_, err := adminPool.Exec(ctx, `DROP SCHEMA IF EXISTS `+quoteIdentifier(schemaName)+` CASCADE`)
	return err
}

// quoteIdentifier 按 PostgreSQL 标识符规则转义名称，避免动态 SQL 因特殊字符破坏语法。
func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
