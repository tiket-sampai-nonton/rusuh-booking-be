// Package dotenv loads KEY=VALUE files into the environment and provides typed getters.
package dotenv

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"
)

// Load reads a .env file. Variables already present in the environment are not overridden,
// so real environment (Docker, CI) always wins. A missing file is not an error.
func Load(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("dotenv: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return fmt.Errorf("dotenv: %s:%d: missing '='", path, line)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("dotenv: set %s: %w", key, err)
		}
	}
	return sc.Err()
}

// String returns the variable or def when unset or empty.
func String(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// Int returns the variable parsed as int, or def when unset.
func Int(key string, def int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("dotenv: %s=%q is not an int: %w", key, v, err)
	}
	return n, nil
}

// Duration returns the variable parsed with time.ParseDuration, or def when unset.
func Duration(key string, def time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("dotenv: %s=%q is not a duration: %w", key, v, err)
	}
	return d, nil
}
