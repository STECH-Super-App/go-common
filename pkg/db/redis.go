package db

import (
	"context"
	"crypto/tls"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisConfig holds configuration for the Redis connection. Zero timeouts keep
// go-redis's own defaults (dial 5 s, read and write 3 s).
type RedisConfig struct {
	Addr     string
	Password string
	DB       int

	// TLS turns on TLS 1.2+ with the system roots. Off by default.
	TLS          bool
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

func redisOptions(config RedisConfig) *redis.Options {
	o := &redis.Options{
		Addr:         config.Addr,
		Password:     config.Password,
		DB:           config.DB,
		DialTimeout:  config.DialTimeout,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
	}
	if config.TLS {
		o.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return o
}

// NewRedis creates a new Redis client and pings it. A client that fails its
// ping is closed before the error is returned.
func NewRedis(ctx context.Context, config RedisConfig) (*redis.Client, error) {
	rdb := redis.NewClient(redisOptions(config))

	if err := pingOrClose(func() error { return rdb.Ping(ctx).Err() }, func() { _ = rdb.Close() }); err != nil {
		return nil, dbError(ReasonUnavailable, "failed to ping redis", err)
	}

	return rdb, nil
}
