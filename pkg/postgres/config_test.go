package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
)

func validConfig() Config {
	return Config{Host: "localhost", Port: 5432, User: "svc_booking", Password: "p@ss/word",
		Database: "rbt", SSLMode: "disable", Schema: "booking", MaxConns: 10}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"valid", func(*Config) {}, ""},
		{"missing host", func(c *Config) { c.Host = "" }, "POSTGRES_HOST"},
		{"bad port", func(c *Config) { c.Port = 0 }, "POSTGRES_PORT"},
		{"missing user", func(c *Config) { c.User = "" }, "POSTGRES_USER"},
		{"missing db", func(c *Config) { c.Database = "" }, "POSTGRES_DATABASE"},
		{"injected schema", func(c *Config) { c.Schema = "booking;drop" }, "POSTGRES_SCHEMA"},
		{"bad sslmode", func(c *Config) { c.SSLMode = "maybe" }, "POSTGRES_SSLMODE"},
		{"too many conns", func(c *Config) { c.MaxConns = 500 }, "POSTGRES_MAX_CONNS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			tt.mutate(&c)
			err := c.Validate()
			if tt.want == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestDSN_EscapesPasswordAndSetsSearchPath(t *testing.T) {
	dsn := validConfig().DSN()
	assert.Contains(t, dsn, "p%40ss%2Fword")
	assert.Contains(t, dsn, "search_path=booking")
	assert.Contains(t, dsn, "sslmode=disable")
}

func TestRedacted_HidesPassword(t *testing.T) {
	r := validConfig().Redacted()
	assert.NotContains(t, r, "p%40ss")
	assert.Contains(t, r, "xxxxx")
}

func TestIsCodeAndConstraintName(t *testing.T) {
	err := &pgconn.PgError{Code: CodeUniqueViolation, ConstraintName: "uq_x"}
	wrapped := errors.Join(errors.New("ctx"), err)
	assert.True(t, IsCode(wrapped, CodeUniqueViolation))
	assert.False(t, IsCode(wrapped, CodeCheckViolation))
	assert.Equal(t, "uq_x", ConstraintName(wrapped))
	assert.Empty(t, ConstraintName(errors.New("plain")))
	assert.False(t, IsCode(errors.New("plain"), CodeUniqueViolation))
}
