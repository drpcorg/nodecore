package outbox

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/redis/go-redis/v9"
)

func TestPostgresClient_Set_WithoutTTL(t *testing.T) {
	mockPool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create pgx mock: %v", err)
	}
	defer mockPool.Close()

	client := &postgresClient{pool: mockPool}

	mockPool.ExpectExec(regexp.QuoteMeta(storeOutboxItem)).
		WithArgs("key-1", []byte("value-1"), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = client.Set(context.Background(), "key-1", []byte("value-1"), 0)
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	if err := mockPool.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresClient_Set_WithTTL(t *testing.T) {
	mockPool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create pgx mock: %v", err)
	}
	defer mockPool.Close()

	client := &postgresClient{pool: mockPool}

	mockPool.ExpectExec(regexp.QuoteMeta(storeOutboxItem)).
		WithArgs("key-1", []byte("value-1"), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = client.Set(context.Background(), "key-1", []byte("value-1"), time.Minute)
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	if err := mockPool.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresClient_Delete(t *testing.T) {
	mockPool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create pgx mock: %v", err)
	}
	defer mockPool.Close()

	client := &postgresClient{pool: mockPool}

	mockPool.ExpectExec(regexp.QuoteMeta(removeOutboxItems)).
		WithArgs("key-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	err = client.Delete(context.Background(), "key-1")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if err := mockPool.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresClient_List_Empty(t *testing.T) {
	mockPool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create pgx mock: %v", err)
	}
	defer mockPool.Close()

	client := &postgresClient{pool: mockPool}

	rows := pgxmock.NewRows([]string{"key", "value"})
	mockPool.ExpectQuery(regexp.QuoteMeta(listOutboxItems)).
		WithArgs(int64(0), int64(10)).
		WillReturnRows(rows)

	items, err := client.List(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}

	if err := mockPool.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresClient_List_Items(t *testing.T) {
	mockPool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create pgx mock: %v", err)
	}
	defer mockPool.Close()

	client := &postgresClient{pool: mockPool}

	rows := pgxmock.NewRows([]string{"key", "value"}).
		AddRow("key-1", []byte("value-1")).
		AddRow("key-2", []byte("value-2"))

	mockPool.ExpectQuery(regexp.QuoteMeta(listOutboxItems)).
		WithArgs(int64(100), int64(2)).
		WillReturnRows(rows)

	items, err := client.List(context.Background(), 100, 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if string(items[0].Key) != "key-1" {
		t.Fatalf("unexpected first item: %q", string(items[0].Key))
	}
	if string(items[1].Key) != "key-2" {
		t.Fatalf("unexpected second item: %q", string(items[1].Key))
	}

	if err := mockPool.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRedisClient_Set_WithoutTTL(t *testing.T) {
	redisClientMock, redisMock := redismock.NewClientMock()
	client := &redisClient{client: redisClientMock}

	redisMock.ExpectTxPipeline()
	redisMock.ExpectHSet(outboxKeyDataPrefix, "key-1", []byte("value-1")).SetVal(1)

	redisMock.CustomMatch(func(expected []interface{}, actual []interface{}) error {
		if len(actual) != 4 {
			return errors.New("unexpected zadd arg count")
		}
		if actual[0] != "zadd" {
			return errors.New("unexpected command")
		}
		if actual[1] != outboxKeyIndexPrefix {
			return errors.New("unexpected index key")
		}
		if actual[3] != "key-1" {
			return errors.New("unexpected member")
		}
		return nil
	}).ExpectZAdd(outboxKeyIndexPrefix, redis.Z{
		Score:  0,
		Member: "key-1",
	}).SetVal(1)

	redisMock.ExpectZRem(outboxKeyTTLPrefix, "key-1").SetVal(1)
	redisMock.ExpectTxPipelineExec()

	err := client.Set(context.Background(), "key-1", []byte("value-1"), 0)
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	if err := redisMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRedisClient_Set_WithTTL(t *testing.T) {
	redisClientMock, redisMock := redismock.NewClientMock()
	client := &redisClient{client: redisClientMock}

	redisMock.ExpectTxPipeline()
	redisMock.ExpectHSet(outboxKeyDataPrefix, "key-1", []byte("value-1")).SetVal(1)

	redisMock.CustomMatch(func(expected []interface{}, actual []interface{}) error {
		if len(actual) != 4 {
			return errors.New("unexpected index zadd arg count")
		}
		if actual[0] != "zadd" {
			return errors.New("unexpected command")
		}
		if actual[1] != outboxKeyIndexPrefix {
			return errors.New("unexpected index key")
		}
		if actual[3] != "key-1" {
			return errors.New("unexpected member")
		}
		return nil
	}).ExpectZAdd(outboxKeyIndexPrefix, redis.Z{
		Score:  0,
		Member: "key-1",
	}).SetVal(1)

	redisMock.CustomMatch(func(expected []interface{}, actual []interface{}) error {
		if len(actual) != 4 {
			return errors.New("unexpected ttl zadd arg count")
		}
		if actual[0] != "zadd" {
			return errors.New("unexpected command")
		}
		if actual[1] != outboxKeyTTLPrefix {
			return errors.New("unexpected ttl key")
		}
		if actual[3] != "key-1" {
			return errors.New("unexpected member")
		}
		return nil
	}).ExpectZAdd(outboxKeyTTLPrefix, redis.Z{
		Score:  0,
		Member: "key-1",
	}).SetVal(1)

	redisMock.ExpectTxPipelineExec()

	err := client.Set(context.Background(), "key-1", []byte("value-1"), time.Minute)
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	if err := redisMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRedisClient_Delete(t *testing.T) {
	redisClientMock, redisMock := redismock.NewClientMock()
	client := &redisClient{client: redisClientMock}

	redisMock.ExpectTxPipeline()
	redisMock.ExpectHDel(outboxKeyDataPrefix, "key-1").SetVal(1)
	redisMock.ExpectZRem(outboxKeyIndexPrefix, "key-1").SetVal(1)
	redisMock.ExpectZRem(outboxKeyTTLPrefix, "key-1").SetVal(1)
	redisMock.ExpectTxPipelineExec()

	err := client.Delete(context.Background(), "key-1")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if err := redisMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRedisClient_List_ZeroLimit(t *testing.T) {
	redisClientMock, redisMock := redismock.NewClientMock()
	client := &redisClient{client: redisClientMock}

	items, err := client.List(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}

	if err := redisMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

type fakeMemorizer struct {
	zrangeByScoreFunc func(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.StringSliceCmd
	hmgetFunc         func(ctx context.Context, key string, fields ...string) *redis.SliceCmd
}

func (fake *fakeMemorizer) Ping(ctx context.Context) *redis.StatusCmd {
	return redis.NewStatusCmd(ctx)
}

func (fake *fakeMemorizer) TxPipeline() redis.Pipeliner {
	panic("TxPipeline should not be called in List tests")
}

func (fake *fakeMemorizer) ZRangeByScore(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.StringSliceCmd {
	return fake.zrangeByScoreFunc(ctx, key, opt)
}

func (fake *fakeMemorizer) HMGet(ctx context.Context, key string, fields ...string) *redis.SliceCmd {
	return fake.hmgetFunc(ctx, key, fields...)
}

func TestRedisClient_List_Empty(t *testing.T) {
	client := &redisClient{
		client: &fakeMemorizer{
			zrangeByScoreFunc: func(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.StringSliceCmd {
				cmd := redis.NewStringSliceCmd(ctx)

				if key == outboxKeyTTLPrefix {
					if opt.Min != "-inf" {
						cmd.SetErr(errors.New("unexpected cleanup min"))
						return cmd
					}
					if opt.Count != expiredCleanupBatchSize {
						cmd.SetErr(errors.New("unexpected cleanup count"))
						return cmd
					}
					cmd.SetVal([]string{})
					return cmd
				}

				if key == outboxKeyIndexPrefix {
					if opt.Min != "1" {
						cmd.SetErr(errors.New("unexpected index min"))
						return cmd
					}
					if opt.Max != "+inf" {
						cmd.SetErr(errors.New("unexpected index max"))
						return cmd
					}
					if opt.Count != 10 {
						cmd.SetErr(errors.New("unexpected index count"))
						return cmd
					}
					cmd.SetVal([]string{})
					return cmd
				}

				cmd.SetErr(errors.New("unexpected key"))
				return cmd
			},
			hmgetFunc: func(ctx context.Context, key string, fields ...string) *redis.SliceCmd {
				cmd := redis.NewSliceCmd(ctx)
				cmd.SetErr(errors.New("HMGet should not be called"))
				return cmd
			},
		},
	}

	items, err := client.List(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}
}

func TestRedisClient_List_Items(t *testing.T) {
	client := &redisClient{
		client: &fakeMemorizer{
			zrangeByScoreFunc: func(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.StringSliceCmd {
				cmd := redis.NewStringSliceCmd(ctx)

				if key == outboxKeyTTLPrefix {
					if opt.Min != "-inf" {
						cmd.SetErr(errors.New("unexpected cleanup min"))
						return cmd
					}
					if opt.Count != expiredCleanupBatchSize {
						cmd.SetErr(errors.New("unexpected cleanup count"))
						return cmd
					}
					cmd.SetVal([]string{})
					return cmd
				}

				if key == outboxKeyIndexPrefix {
					if opt.Min != "101" {
						cmd.SetErr(errors.New("unexpected index min"))
						return cmd
					}
					if opt.Max != "+inf" {
						cmd.SetErr(errors.New("unexpected index max"))
						return cmd
					}
					if opt.Count != 2 {
						cmd.SetErr(errors.New("unexpected index count"))
						return cmd
					}
					cmd.SetVal([]string{"key-1", "key-2"})
					return cmd
				}

				cmd.SetErr(errors.New("unexpected key"))
				return cmd
			},
			hmgetFunc: func(ctx context.Context, key string, fields ...string) *redis.SliceCmd {
				cmd := redis.NewSliceCmd(ctx)

				if key != outboxKeyDataPrefix {
					cmd.SetErr(errors.New("unexpected hmget key"))
					return cmd
				}
				if len(fields) != 2 || fields[0] != "key-1" || fields[1] != "key-2" {
					cmd.SetErr(errors.New("unexpected hmget fields"))
					return cmd
				}

				cmd.SetVal([]interface{}{[]byte("value-1"), []byte("value-2")})
				return cmd
			},
		},
	}

	items, err := client.List(context.Background(), 100, 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if string(items[0].Key) != "key-1" {
		t.Fatalf("unexpected first item: %q", string(items[0].Key))
	}
	if string(items[1].Key) != "key-2" {
		t.Fatalf("unexpected second item: %q", string(items[1].Key))
	}
}
