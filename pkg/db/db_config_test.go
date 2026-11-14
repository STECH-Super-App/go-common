package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperr "github.com/STECH-Super-App/go-common/pkg/errors"
)

// SL-22: NewPostgres overwrote pgx's parsed values with the struct's
// unconditionally, so an unset MaxConns (0) made pool creation fail, an unset
// MaxConnLifetime (0) made connections expire on release, and a value given in
// the URL (pool_max_conns=...) was silently replaced by the zero field.
func TestPoolConfig_ZeroFieldsKeepTheURLAndPgxDefaults(t *testing.T) {
	cfg, err := poolConfig(PostgresConfig{
		URL: "postgres://u:p@localhost:5432/db?pool_max_conns=7&pool_max_conn_lifetime=2h&pool_min_conns=2",
	})
	require.NoError(t, err)

	assert.EqualValues(t, 7, cfg.MaxConns, "an unset MaxConns must not overwrite pool_max_conns")
	assert.EqualValues(t, 2, cfg.MinConns)
	assert.Equal(t, 2*time.Hour, cfg.MaxConnLifetime)
}

func TestPoolConfig_ZeroConfigIsUsable(t *testing.T) {
	cfg, err := poolConfig(PostgresConfig{URL: "postgres://u:p@localhost:5432/db"})
	require.NoError(t, err)

	assert.GreaterOrEqual(t, cfg.MaxConns, int32(1), "pgx's own default, not 0")
	assert.Greater(t, cfg.MaxConnLifetime, time.Duration(0), "0 would expire every connection on release")
}

func TestPoolConfig_ExplicitFieldsWin(t *testing.T) {
	cfg, err := poolConfig(PostgresConfig{
		URL:      "postgres://u:p@localhost:5432/db?pool_max_conns=7",
		MaxConns: 3, MinConns: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute,
	})
	require.NoError(t, err)

	assert.EqualValues(t, 3, cfg.MaxConns)
	assert.EqualValues(t, 1, cfg.MinConns)
	assert.Equal(t, time.Hour, cfg.MaxConnLifetime)
	assert.Equal(t, time.Minute, cfg.MaxConnIdleTime)
}

func TestNewPostgres_UnsetMaxConnsFailsAtPingNotAtPoolCreation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := NewPostgres(ctx, PostgresConfig{URL: "postgres://u:p@127.0.0.1:1/db?connect_timeout=1"})

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "create postgres pool", "a zero MaxConns must not reject the config")
	assert.Contains(t, err.Error(), "ping postgres")
}

func TestPingOrClose_ClosesOnFailureOnly(t *testing.T) {
	closed := 0
	closeFn := func() { closed++ }

	require.NoError(t, pingOrClose(func() error { return nil }, closeFn))
	assert.Zero(t, closed)

	boom := errors.New("down")
	err := pingOrClose(func() error { return boom }, closeFn)
	require.ErrorIs(t, err, boom)
	assert.Equal(t, 1, closed, "a failed ping must not leak the pool or client")
}

func TestRedisOptions(t *testing.T) {
	o := redisOptions(RedisConfig{Addr: "r:6379", Password: "pw", DB: 2})
	assert.Equal(t, "r:6379", o.Addr)
	assert.Equal(t, "pw", o.Password)
	assert.Equal(t, 2, o.DB)
	assert.Nil(t, o.TLSConfig)

	o = redisOptions(RedisConfig{Addr: "r:6379", TLS: true,
		DialTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 3 * time.Second})
	require.NotNil(t, o.TLSConfig)
	assert.GreaterOrEqual(t, o.TLSConfig.MinVersion, uint16(0x0303), "TLS 1.2 or newer")
	assert.Equal(t, time.Second, o.DialTimeout)
	assert.Equal(t, 2*time.Second, o.ReadTimeout)
	assert.Equal(t, 3*time.Second, o.WriteTimeout)
}

// SL-26: the factories returned raw fmt.Errorf errors (Critical Rule 7).
func TestFactoriesReturnTypedErrors(t *testing.T) {
	_, err := poolConfig(PostgresConfig{URL: "not a url ::"})
	require.Error(t, err)
	var ae *apperr.AppError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, ReasonConfigInvalid, ae.Reason)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = NewPostgres(ctx, PostgresConfig{URL: "postgres://u:p@127.0.0.1:1/db?connect_timeout=1"})
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, ReasonUnavailable, ae.Reason)
	assert.NotNil(t, ae.Unwrap())

	_, err = NewRedis(ctx, RedisConfig{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond})
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, ReasonUnavailable, ae.Reason)
}
