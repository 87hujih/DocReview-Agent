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

// NewPool 创建 pgx 连接池，并在每个新连接上注册 pgvector 类型。
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}

	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return registerVectorTypes(ctx, conn)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}

// RunMigrations 按字典序执行内嵌 SQL 迁移，并在建表完成后重新注册 pgvector 类型。
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	var migrationNames []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		migrationNames = append(migrationNames, entry.Name())
	}

	sort.Strings(migrationNames)

	for _, migrationName := range migrationNames {
		statement, err := migrationsFS.ReadFile("migrations/" + migrationName)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", migrationName, err)
		}

		if _, err := pool.Exec(ctx, string(statement)); err != nil {
			return fmt.Errorf("run migration %s: %w", migrationName, err)
		}
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for vector registration: %w", err)
	}
	defer conn.Release()

	if err := pgxvector.RegisterTypes(ctx, conn.Conn()); err != nil {
		return fmt.Errorf("register vector types after migrations: %w", err)
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
