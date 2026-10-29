// Package redis — быстрые данные: статусы задач, кэш статистики, rate limit.
//
// Redis здесь не источник истины (им остаётся Postgres), а ускоритель:
// клиент опрашивает статус каждую секунду, и эти запросы не должны
// бить по основной БД.
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/config"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/domain"
)

// ErrCacheMiss — ключ отсутствует.
var ErrCacheMiss = errors.New("cache miss")

// Client — обёртка над go-redis с типизированными методами.
type Client struct {
	rdb *goredis.Client
}

// New подключается к Redis и проверяет соединение.
func New(ctx context.Context, cfg config.Redis) (*Client, error) {
	rdb := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

// Close закрывает соединение.
func (c *Client) Close() error { return c.rdb.Close() }

// Ping для health-check.
func (c *Client) Ping(ctx context.Context) error { return c.rdb.Ping(ctx).Err() }

// --- Статусы анализов ---

const statusTTL = 24 * time.Hour

func statusKey(id string) string { return "analysis:" + id + ":status" }

// SetStatus сохраняет статус анализа с TTL.
func (c *Client) SetStatus(ctx context.Context, id string, status domain.AnalysisStatus) error {
	return c.rdb.Set(ctx, statusKey(id), string(status), statusTTL).Err()
}

// GetStatus возвращает статус или ErrCacheMiss.
func (c *Client) GetStatus(ctx context.Context, id string) (domain.AnalysisStatus, error) {
	v, err := c.rdb.Get(ctx, statusKey(id)).Result()
	if errors.Is(err, goredis.Nil) {
		return "", ErrCacheMiss
	}
	if err != nil {
		return "", err
	}
	return domain.AnalysisStatus(v), nil
}

// DeleteStatus удаляет статус (при удалении анализа).
func (c *Client) DeleteStatus(ctx context.Context, id string) error {
	return c.rdb.Del(ctx, statusKey(id)).Err()
}

// --- Кэш произвольных JSON-значений (статистика) ---

// SetJSON сериализует значение и кладёт с TTL.
func (c *Client) SetJSON(ctx context.Context, key string, v any, ttl time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, key, b, ttl).Err()
}

// GetJSON читает и десериализует значение; ErrCacheMiss, если нет.
func (c *Client) GetJSON(ctx context.Context, key string, dst any) error {
	b, err := c.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, goredis.Nil) {
		return ErrCacheMiss
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// Delete удаляет ключи.
func (c *Client) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return c.rdb.Del(ctx, keys...).Err()
}

// --- Rate limiting (fixed window) ---

// Allow увеличивает счётчик для key в окне window и сообщает,
// не превышен ли limit. Возвращает также текущее значение счётчика.
func (c *Client) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int64, error) {
	pipe := c.rdb.TxPipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, window)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, 0, err
	}
	n := incr.Val()
	return n <= int64(limit), n, nil
}
