package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvector "github.com/pgvector/pgvector-go/pgx"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const (
	migrationLockNamespace int32 = 20260408
	migrationLockKey       int32 = 1
)

type migration struct {
	Name     string
	SQL      string
	Checksum string
}

// NewPool 创建 pgx 连接池，并在每个新连接上注册 pgvector 类型。
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("解析数据库配置失败：%w", err)
	}

	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return registerVectorTypes(ctx, conn)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("创建连接池失败：%w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接数据库失败：%w", err)
	}

	return pool, nil
}

// RunMigrations applies only 待处理的 embedded migrations 和 records their canonical checksums.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	migrations, err := loadMigrations(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	return runMigrations(ctx, pool, migrations)
}

// loadMigrations 按作用域读取并返回所需数据。
func loadMigrations(source fs.FS, directory string) ([]migration, error) {
	entries, err := fs.ReadDir(source, directory)
	if err != nil {
		return nil, fmt.Errorf("读取 migrations 目录失败：%w", err)
	}

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		statement, err := fs.ReadFile(source, directory+"/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("读取迁移 %s 失败：%w", entry.Name(), err)
		}
		canonicalSQL := canonicalMigrationSQL(string(statement))
		digest := sha256.Sum256([]byte(canonicalSQL))
		migrations = append(migrations, migration{
			Name:     entry.Name(),
			SQL:      canonicalSQL,
			Checksum: hex.EncodeToString(digest[:]),
		})
	}

	sort.Slice(migrations, func(i int, j int) bool {
		return migrations[i].Name < migrations[j].Name
	})
	return migrations, nil
}

// canonicalMigrationSQL 执行该函数负责的核心处理逻辑。
func canonicalMigrationSQL(statement string) string {
	statement = strings.TrimPrefix(statement, "\uFEFF")
	statement = strings.ReplaceAll(statement, "\r\n", "\n")
	return strings.ReplaceAll(statement, "\r", "\n")
}

// planMigrations 执行该函数负责的核心处理逻辑。
func planMigrations(available []migration, applied map[string]string) ([]migration, error) {
	known := make(map[string]migration, len(available))
	for _, candidate := range available {
		if candidate.Name == "" || candidate.Checksum == "" {
			return nil, fmt.Errorf("迁移元数据不完整")
		}
		if _, exists := known[candidate.Name]; exists {
			return nil, fmt.Errorf("迁移名称重复：%s", candidate.Name)
		}
		known[candidate.Name] = candidate
	}

	for name, recordedChecksum := range applied {
		candidate, exists := known[name]
		if !exists {
			return nil, fmt.Errorf("已应用迁移文件缺失：%s", name)
		}
		if candidate.Checksum != recordedChecksum {
			return nil, fmt.Errorf("迁移 checksum 不匹配：%s", name)
		}
	}

	pending := make([]migration, 0, len(available))
	for _, candidate := range available {
		if _, exists := applied[candidate.Name]; !exists {
			pending = append(pending, candidate)
		}
	}
	return pending, nil
}

// runMigrations 执行该函数负责的核心处理逻辑。
func runMigrations(ctx context.Context, pool *pgxpool.Pool, available []migration) error {

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("获取迁移连接失败：%w", err)
	}
	defer conn.Release()

	// 开启事务，确保后续状态变更以原子方式提交。
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("开启迁移事务失败：%w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// 迁移会创建数据库级 extension，必须串行化，避免多个进程并发执行时冲撞系统表唯一索引。
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, migrationLockNamespace, migrationLockKey); err != nil {
		return fmt.Errorf("获取迁移 advisory lock 失败：%w", err)
	}

	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name TEXT PRIMARY KEY,
			checksum CHAR(64) NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			execution_ms BIGINT NOT NULL CHECK (execution_ms >= 0)
		)
	`); err != nil {
		return fmt.Errorf("创建 migration ledger 失败：%w", err)
	}

	rows, err := tx.Query(ctx, `SELECT name, checksum FROM schema_migrations ORDER BY name`)
	if err != nil {
		return fmt.Errorf("读取 migration ledger 失败：%w", err)
	}
	applied := make(map[string]string)
	for rows.Next() {
		var name string
		var checksum string
		if err := rows.Scan(&name, &checksum); err != nil {
			rows.Close()
			return fmt.Errorf("解析 migration ledger 失败：%w", err)
		}
		applied[name] = strings.TrimSpace(checksum)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("遍历 migration ledger 失败：%w", err)
	}
	rows.Close()

	pending, err := planMigrations(available, applied)
	if err != nil {
		return err
	}

	for _, candidate := range pending {
		startedAt := time.Now()
		if _, err := tx.Exec(ctx, candidate.SQL); err != nil {
			return fmt.Errorf("执行迁移 %s 失败：%w", candidate.Name, err)
		}

		executionMS := time.Since(startedAt).Milliseconds()
		if _, err := tx.Exec(ctx, `
			INSERT INTO schema_migrations (name, checksum, execution_ms)
			VALUES ($1, $2, $3)
		`, candidate.Name, candidate.Checksum, executionMS); err != nil {
			return fmt.Errorf("记录迁移 %s 失败：%w", candidate.Name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交迁移失败：%w", err)
	}

	// Connections opened 之前 创建 EXTENSION could not 注册 向量 OIDs.
	// Reset 之后 commit so every subsequent connection runs AfterConnect against
	// the committed schema; 没有 post-commit setup 错误 can masquerade as 一个 失败 migration.
	pool.Reset()

	return nil
}

// registerVectorTypes 允许在 向量 扩展尚未创建前启动，迁移完成后会再次注册。
func registerVectorTypes(ctx context.Context, conn *pgx.Conn) error {
	err := pgxvector.RegisterTypes(ctx, conn)
	if err != nil && strings.Contains(err.Error(), "vector type not found in the database") {
		return nil
	}

	return err
}
