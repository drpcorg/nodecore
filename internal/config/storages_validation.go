package config

import (
	"errors"
	"fmt"
)

func (a *AppStorageConfig) validate() (string, error) {
	if a.Name == "" {
		return "", errors.New("app storage name cannot be empty")
	}
	if a.Redis != nil {
		if err := a.Redis.validate(); err != nil {
			return "", fmt.Errorf("error during redis storage config validation, cause: %s", err.Error())
		}
		return "redis", nil
	}
	if a.Postgres != nil {
		// Postgres validation is minimal - just check URL exists
		if a.Postgres.Url == "" {
			return "", errors.New("postgres url cannot be empty")
		}
		return "postgres", nil
	}
	return "", errors.New("storage must have either redis or postgres configuration")
}

func (r *RedisStorageConfig) validate() error {
	if r.FullUrl == "" && r.Address == "" {
		return errors.New("either 'address' or 'full_url' must be specified")
	}
	if r.Timeouts != nil {
		if r.Timeouts.ReadTimeout != nil && *r.Timeouts.ReadTimeout < 0 {
			return errors.New("read timeout cannot be negative")
		}
		if r.Timeouts.WriteTimeout != nil && *r.Timeouts.WriteTimeout < 0 {
			return errors.New("write timeout cannot be negative")
		}
		if r.Timeouts.ConnectTimeout != nil && *r.Timeouts.ConnectTimeout < 0 {
			return errors.New("connect timeout cannot be negative")
		}
	}
	if r.Pool != nil {
		if r.Pool.Size < 0 {
			return errors.New("pool size cannot be negative")
		}
		if r.Pool.PoolTimeout != nil && *r.Pool.PoolTimeout < 0 {
			return errors.New("pool timeout cannot be negative")
		}
		if r.Pool.MinIdleConns < 0 {
			return errors.New("pool min idle connections cannot be negative")
		}
		if r.Pool.MaxIdleConns < 0 {
			return errors.New("pool max idle connections cannot be negative")
		}
		if r.Pool.MinIdleConns > r.Pool.MaxIdleConns {
			return errors.New("pool min idle connections cannot be greater than pool max idle connections")
		}
		if r.Pool.MaxActiveConns < 0 {
			return errors.New("pool max connections cannot be negative")
		}
		if r.Pool.ConnMaxLifeTime != nil && *r.Pool.ConnMaxLifeTime < 0 {
			return errors.New("pool conn max life time cannot be negative")
		}
		if r.Pool.ConnMaxIdleTime != nil && *r.Pool.ConnMaxIdleTime < 0 {
			return errors.New("pool conn max idle time cannot be negative")
		}
	}

	return nil
}
