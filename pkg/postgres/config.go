// Package postgres wraps a bounded pgx connection pool scoped to one service schema.
package postgres

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
)

var schemaPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// Config holds the POSTGRES_* variables. Each service connects with its own role
// (POSTGRES_USER=svc_<service>) and only sees its own schema (POSTGRES_SCHEMA).
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
	Schema   string // search_path; wajib agar query tidak pernah jatuh ke schema lain
	MaxConns int
}

// Validate fails fast on missing or unsafe values.
func (c Config) Validate() error {
	var errs []error
	if c.Host == "" {
		errs = append(errs, errors.New("postgres: POSTGRES_HOST wajib diisi"))
	}
	if c.Port <= 0 || c.Port > 65535 {
		errs = append(errs, fmt.Errorf("postgres: POSTGRES_PORT %d di luar rentang", c.Port))
	}
	if c.User == "" {
		errs = append(errs, errors.New("postgres: POSTGRES_USER wajib diisi"))
	}
	if c.Database == "" {
		errs = append(errs, errors.New("postgres: POSTGRES_DATABASE wajib diisi"))
	}
	if !schemaPattern.MatchString(c.Schema) {
		errs = append(errs, fmt.Errorf("postgres: POSTGRES_SCHEMA %q tidak valid", c.Schema))
	}
	switch c.SSLMode {
	case "disable", "require", "verify-ca", "verify-full":
	default:
		errs = append(errs, fmt.Errorf("postgres: POSTGRES_SSLMODE %q tidak dikenal", c.SSLMode))
	}
	if c.MaxConns < 1 || c.MaxConns > 200 {
		errs = append(errs, fmt.Errorf("postgres: POSTGRES_MAX_CONNS %d di luar rentang 1–200", c.MaxConns))
	}
	return errors.Join(errs...)
}

// DSN builds the connection URL. The password is URL-escaped and never logged.
func (c Config) DSN() string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, c.Password),
		Host:   c.Host + ":" + strconv.Itoa(c.Port),
		Path:   "/" + c.Database,
	}
	q := url.Values{}
	q.Set("sslmode", c.SSLMode)
	q.Set("search_path", c.Schema)
	q.Set("application_name", c.User)
	u.RawQuery = q.Encode()
	return u.String()
}

// Redacted returns the DSN without the password, for logs.
func (c Config) Redacted() string {
	cp := c
	cp.Password = "xxxxx"
	return cp.DSN()
}
