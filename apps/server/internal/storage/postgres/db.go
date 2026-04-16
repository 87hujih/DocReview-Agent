package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

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

// RunMigrations 按字典序执行内嵌 SQL 迁移，并在建表完成后重新注册 pgvector 类型。
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("读取 migrations 目录失败：%w", err)
	}

	var migrationNames []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		migrationNames = append(migrationNames, entry.Name())
	}

	sort.Strings(migrationNames)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("获取迁移连接失败：%w", err)
	}
	defer conn.Release()

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

	for _, migrationName := range migrationNames {
		statement, err := migrationsFS.ReadFile("migrations/" + migrationName)
		if err != nil {
			return fmt.Errorf("读取迁移 %s 失败：%w", migrationName, err)
		}

		if _, err := tx.Exec(ctx, string(statement)); err != nil {
			return fmt.Errorf("执行迁移 %s 失败：%w", migrationName, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交迁移失败：%w", err)
	}

	if err := pgxvector.RegisterTypes(ctx, conn.Conn()); err != nil {
		return fmt.Errorf("迁移后注册 vector 类型失败：%w", err)
	}

	return nil
}

// registerVectorTypes 允许在 vector 扩展尚未创建前启动，迁移完成后会再次注册。
func registerVectorTypes(ctx context.Context, conn *pgx.Conn) error {
	err := pgxvector.RegisterTypes(ctx, conn)
	if err != nil && strings.Contains(err.Error(), "vector type not found in the database") {
		return nil
	}

	return err
}
