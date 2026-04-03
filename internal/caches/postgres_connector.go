package caches

import (
	"context"
	"errors"
	"fmt"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/storages"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

const (
	createTable = `
CREATE TABLE IF NOT EXISTS %s (
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL,
    expires_at  TIMESTAMPTZ
);`

	createIndex = `
CREATE INDEX IF NOT EXISTS idx_cache_items_expires_at
ON %s (expires_at)
WHERE expires_at IS NOT NULL;`

	getItem = `
SELECT value FROM %s
WHERE key=$1 AND (expires_at IS NULL OR expires_at > now());`

	storeItem = `
INSERT INTO %s (key, value, expires_at)
VALUES ($1, $2, $3)
ON CONFLICT (key) DO NOTHING;`

	removeItems = `
DELETE FROM %s
WHERE expires_at IS NOT NULL AND expires_at <= NOW();`
)

type PostgresConnector struct {
	id                    string
	pool                  *pgxpool.Pool
	table                 string
	queryTimeout          time.Duration
	expiredRemoveInterval time.Duration
}

var _ CacheConnector = (*PostgresConnector)(nil)

func NewPostgresConnector(id string, postgresCfg *config.PostgresCacheConnectorConfig, storageRegistry *storages.StorageRegistry) (*PostgresConnector, error) {
	storage, ok := storageRegistry.Get(postgresCfg.StorageName)
	if !ok {
		return nil, fmt.Errorf("postgres storage with name %s not found", postgresCfg.StorageName)
	}
	postgresStorage, ok := storage.(*storages.PostgresStorage)
	if !ok {
		return nil, fmt.Errorf("postgres storage with name %s is not a postgres storage", postgresCfg.StorageName)
	}

	return &PostgresConnector{
		id:                    id,
		pool:                  postgresStorage.Postgres,
		table:                 postgresCfg.CacheTable,
		queryTimeout:          *postgresCfg.QueryTimeout,
		expiredRemoveInterval: postgresCfg.ExpiredRemoveInterval,
	}, nil
}

func (p *PostgresConnector) Id() string {
	return p.id
}

func (p *PostgresConnector) Store(ctx context.Context, key string, object string, ttl time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, p.queryTimeout)
	defer cancel()

	var expiresAt *time.Time
	if ttl > 0 {
		expiresAt = lo.ToPtr(time.Now().UTC().Add(ttl))
	}

	_, err := p.pool.Exec(ctx, fmt.Sprintf(storeItem, p.table), key, object, expiresAt)
	return err
}

func (p *PostgresConnector) Receive(ctx context.Context, key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, p.queryTimeout)
	defer cancel()

	var value string
	err := p.pool.QueryRow(ctx, fmt.Sprintf(getItem, p.table), key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCacheNotFound
	}

	return []byte(value), err
}

func (p *PostgresConnector) Initialize() error {
	ctx, cancel := context.WithTimeout(context.Background(), p.queryTimeout)
	defer cancel()

	err := p.pool.Ping(ctx)
	if err != nil {
		return fmt.Errorf("couldn't connect to postgres: %w", err)
	}

	err = p.ensureSchema()
	if err != nil {
		return fmt.Errorf("couldn't ensure schema: %w", err)
	}

	go p.removeExpired()

	return nil
}

const (
	createOutboxTable = `
CREATE TABLE IF NOT EXISTS _outbox (
    id serial PRIMARY KEY,
    key         VARCHAR(255) UNIQUE NOT NULL,
    value       BYTEA NOT NULL,
    expires_at  TIMESTAMPTZ
);`

	listOutboxItems = `
SELECT key, value FROM _outbox
WHERE id > $1 AND (expires_at IS NULL OR expires_at > now()) ORDER BY id ASC LIMIT $2;`

	storeOutboxItem = `
INSERT INTO _outbox (key, value, expires_at)
VALUES ($1, $2, $3)
ON CONFLICT (key) DO NOTHING;`

	removeOutboxItems = `
DELETE FROM _outbox
WHERE key = $1 OR (expires_at IS NOT NULL AND expires_at <= NOW());`
)

func (p *PostgresConnector) OutboxStore(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	var expiresAt *time.Time
	if ttl > 0 {
		expiresAt = lo.ToPtr(time.Now().UTC().Add(ttl))
	}

	_, err := p.pool.Exec(ctx, storeOutboxItem, key, value, expiresAt)
	return err
}

func (p *PostgresConnector) OutboxRemove(ctx context.Context, key string) error {
	_, err := p.pool.Exec(ctx, removeOutboxItems, key)
	return err
}

func (p *PostgresConnector) OutboxList(
	ctx context.Context,
	cursor, limit int64,
) ([]outboxItem, error) {
	rows, err := p.pool.Query(ctx, listOutboxItems, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]outboxItem, 0, limit)

	for rows.Next() {
		var (
			key   string
			value []byte
		)
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		result = append(result, outboxItem{key: value})
	}
	return result, rows.Err()
}

func (p *PostgresConnector) ensureSchema() error {
	ctx, cancel := context.WithTimeout(context.Background(), p.queryTimeout)
	defer cancel()

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

	if _, err = tx.Exec(ctx, fmt.Sprintf(createTable, p.table)); err != nil {
		return fmt.Errorf("couldn't create table: %w", err)
	}
	if _, err = tx.Exec(ctx, fmt.Sprintf(createIndex, p.table)); err != nil {
		return fmt.Errorf("couldn't create index: %w", err)
	}
	if _, err = tx.Exec(ctx, createOutboxTable); err != nil {
		return fmt.Errorf("couldn't create outbox table: %w", err)
	}

	return tx.Commit(ctx)
}

func (p *PostgresConnector) removeExpired() {
	for {
		<-time.After(p.expiredRemoveInterval)

		err := p.removeItems()
		if err != nil {
			log.Error().Err(err).Msg("couldn't remove expired items")
		}
	}
}

func (p *PostgresConnector) removeItems() error {
	ctx, cancel := context.WithTimeout(context.Background(), p.queryTimeout)
	defer cancel()

	result, err := p.pool.Exec(ctx, fmt.Sprintf(removeItems, p.table))
	if err != nil {
		return err
	}

	if rows := result.RowsAffected(); rows > 0 {
		log.Debug().Msgf("removed %d expired items from %s", rows, p.table)
	}

	result, err = p.pool.Exec(ctx, removeOutboxItems)
	if err != nil {
		return err
	}
	if rows := result.RowsAffected(); rows > 0 {
		log.Debug().Msgf("removed %d expired items from _outbox", rows)
	}
	return nil
}
