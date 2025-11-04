package caches

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/storages"
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

	return tx.Commit(ctx)
}

func (p *PostgresConnector) removeExpired() {
	for {
		<-time.After(p.expiredRemoveInterval)

		err := p.removeItems()
		if err != nil {
			log.Warn().Err(err).Msg("couldn't remove expired items")
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
		log.Debug().Msgf("removed %d expired items from postgres", rows)
	}

	return nil
}

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

var _ CacheConnector = (*PostgresConnector)(nil)
