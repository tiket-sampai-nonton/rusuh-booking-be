// Package valkey wraps valkey-go for the roles Valkey has in RBT: antrean, rate limit, cache, dan OTP sementara.
// Valkey tidak pernah menyimpan stok, hold, kuota, NIK, sesi login, atau status pembayaran (ADR-008).
package valkey

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Node is one entry of VALKEY_SERVER.
type Node struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// Config holds the VALKEY_* variables.
type Config struct {
	Server    string // VALKEY_SERVER: JSON array, mis. [{"host":"localhost","port":6379}]
	Username  string
	Password  string
	TLS       bool
	DB        int
	PrefixEnv string // VALKEY_PREFIX_ENV: namespace key bila satu cluster dipakai banyak lingkungan
}

// Nodes parses VALKEY_SERVER.
func (c Config) Nodes() ([]Node, error) {
	var nodes []Node
	if err := json.Unmarshal([]byte(c.Server), &nodes); err != nil {
		return nil, fmt.Errorf("valkey: VALKEY_SERVER bukan JSON array node: %w", err)
	}
	if len(nodes) == 0 {
		return nil, errors.New("valkey: VALKEY_SERVER kosong")
	}
	for i, n := range nodes {
		if strings.TrimSpace(n.Host) == "" || n.Port <= 0 || n.Port > 65535 {
			return nil, fmt.Errorf("valkey: VALKEY_SERVER[%d] tidak valid: %+v", i, n)
		}
	}
	return nodes, nil
}

// Addresses returns host:port for every node.
func (c Config) Addresses() ([]string, error) {
	nodes, err := c.Nodes()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = net.JoinHostPort(n.Host, strconv.Itoa(n.Port))
	}
	return out, nil
}

// Validate fails fast on malformed configuration.
func (c Config) Validate() error {
	var errs []error
	if _, err := c.Nodes(); err != nil {
		errs = append(errs, err)
	}
	if c.DB < 0 || c.DB > 15 {
		errs = append(errs, fmt.Errorf("valkey: VALKEY_DB %d di luar rentang 0–15", c.DB))
	}
	if strings.ContainsAny(c.PrefixEnv, " {}:") {
		errs = append(errs, fmt.Errorf("valkey: VALKEY_PREFIX_ENV %q tidak boleh berisi spasi, kurung kurawal, atau titik dua", c.PrefixEnv))
	}
	return errors.Join(errs...)
}

// Key prefixes k with the environment namespace (mis. "preprod:q:{e:123}:seq").
// Hash tag {..} tetap utuh sehingga semua key satu event berada di slot yang sama.
func (c Config) Key(k string) string {
	if c.PrefixEnv == "" {
		return k
	}
	return c.PrefixEnv + ":" + k
}
