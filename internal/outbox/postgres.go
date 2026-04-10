package outbox

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/lo"
	"time"
)

const (
	createOutboxTable = `
CREATE TABLE IF NOT EXISTS _stats_outbox (
    id serial PRIMARY KEY,
    key         VARCHAR(255) UNIQUE NOT NULL,
    value       BYTEA NOT NULL,
    expires_at  TIMESTAMPTZ
);`

	listOutboxItems = `
SELECT key, value FROM _stats_outbox
WHERE id > $1 AND (expires_at IS NULL OR expires_at > now()) ORDER BY id ASC LIMIT $2;`

	storeOutboxItem = `
INSERT INTO _stats_outbox (key, value, expires_at)
VALUES ($1, $2, $3)
ON CONFLICT (key) DO NOTHING;`

	removeOutboxItems = `
DELETE FROM _stats_outbox
WHERE key = $1 OR (expires_at IS NOT NULL AND expires_at <= NOW());`
)

type Pooler interface {
	Ping(ctx context.Context) error
	Acquire(ctx context.Context) (*pgxpool.Conn, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type postgresClient struct {
	pool Pooler
}

func newPostgresClient(pool Pooler) (*postgresClient, error) {
	pc := &postgresClient{
		pool: pool,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := pc.pool.Ping(ctx)
	if err != nil {
		return nil, fmt.Errorf("couldn't connect to postgres: %w", err)
	}

	return pc, pc.rollupSchema(ctx)
}

func (p *postgresClient) rollupSchema(ctx context.Context) error {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	if _, err = tx.Exec(ctx, createOutboxTable); err != nil {
		return fmt.Errorf("couldn't create outbox table: %w", err)
	}

	return tx.Commit(ctx)
}

func (p *postgresClient) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	var expiresAt *time.Time
	if ttl > 0 {
		expiresAt = lo.ToPtr(time.Now().UTC().Add(ttl))
	}

	_, err := p.pool.Exec(ctx, storeOutboxItem, key, value, expiresAt)
	return err
}

func (p *postgresClient) Delete(ctx context.Context, key string) error {
	_, err := p.pool.Exec(ctx, removeOutboxItems, key)
	return err
}

func (p *postgresClient) List(ctx context.Context, cursor, limit int64) ([]Item, error) {
	rows, err := p.pool.Query(ctx, listOutboxItems, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]Item, 0, limit)

	for rows.Next() {
		var (
			key   string
			value []byte
		)
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		result = append(result, Item{key, value})
	}
	return result, rows.Err()
}
