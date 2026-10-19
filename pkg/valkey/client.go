package valkey

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/valkey-io/valkey-go"
)

// Client owns the valkey-go client.
type Client struct {
	vk  valkey.Client
	cfg Config
}

// New connects (standalone or cluster, decided by valkey-go from the nodes) and pings once.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	addrs, err := cfg.Addresses()
	if err != nil {
		return nil, err
	}
	opt := valkey.ClientOption{
		InitAddress:  addrs,
		Username:     cfg.Username,
		Password:     cfg.Password,
		SelectDB:     cfg.DB,
		DisableCache: true, // client-side caching tidak dipakai; konsistensi antrean lebih penting
	}
	if cfg.TLS {
		opt.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	vk, err := valkey.NewClient(opt)
	if err != nil {
		return nil, fmt.Errorf("valkey: connect %v: %w", addrs, err)
	}
	c := &Client{vk: vk, cfg: cfg}
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx); err != nil {
		vk.Close()
		return nil, fmt.Errorf("valkey: ping %v: %w", addrs, err)
	}
	return c, nil
}

// VK exposes the raw client to adapters (Lua scripts, pipelines).
func (c *Client) VK() valkey.Client { return c.vk }

// Key applies the environment namespace.
func (c *Client) Key(k string) string { return c.cfg.Key(k) }

// Name implements the health Pinger contract.
func (c *Client) Name() string { return "valkey" }

// Ping checks connectivity.
func (c *Client) Ping(ctx context.Context) error {
	return c.vk.Do(ctx, c.vk.B().Ping().Build()).Error()
}

// Close releases connections.
func (c *Client) Close() { c.vk.Close() }
