package storages

import (
	"context"
	"fmt"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStorage struct {
	Postgres *pgxpool.Pool
	name     string
}

func (p *PostgresStorage) storage() string {
	return "postgres"
}

func NewPostgresStorage(name string, postgresConfig *config.PostgresStorageConfig) (*PostgresStorage, error) {
	cfg, err := pgxpool.ParseConfig(postgresConfig.Url)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse postgres url %s of storage '%s': %w", postgresConfig.Url, name, err)
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("couldn't create a postgres pool: %w", err)
	}
	return &PostgresStorage{
		Postgres: pool,
		name:     name,
	}, nil
}
