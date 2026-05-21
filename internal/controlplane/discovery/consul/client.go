package consul

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	Addr       string
	Token      string
	HTTPClient *http.Client
}

type RouteMetadata struct {
	Domain  string `json:"domain"`
	Service string `json:"service"`
	Port    int    `json:"port"`
}

func (c *Client) Health(ctx context.Context) error {
	if c.Addr == "" {
		return fmt.Errorf("CONSUL_HTTP_ADDR is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Addr+"/v1/status/leader", nil)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("X-Consul-Token", c.Token)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("consul returned status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) PutKV(ctx context.Context, key string, value []byte) error {
	if c.Addr == "" {
		return fmt.Errorf("CONSUL_HTTP_ADDR is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.Addr+"/v1/kv/"+key, bytes.NewReader(value))
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("X-Consul-Token", c.Token)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("consul returned status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) GetKV(ctx context.Context, key string) ([]byte, error) {
	if c.Addr == "" {
		return nil, fmt.Errorf("CONSUL_HTTP_ADDR is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Addr+"/v1/kv/"+key+"?raw", nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("X-Consul-Token", c.Token)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("consul key %s not found", key)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("consul returned status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (c *Client) DeleteKV(ctx context.Context, key string) error {
	if c.Addr == "" {
		return fmt.Errorf("CONSUL_HTTP_ADDR is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.Addr+"/v1/kv/"+key, nil)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("X-Consul-Token", c.Token)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("consul returned status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) WriteRoute(ctx context.Context, domain string, meta RouteMetadata) error {
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return c.PutKV(ctx, "stackforge/routes/"+domain, b)
}

func (c *Client) ReadRoute(ctx context.Context, domain string) (RouteMetadata, error) {
	b, err := c.GetKV(ctx, "stackforge/routes/"+domain)
	if err != nil {
		return RouteMetadata{}, err
	}
	var meta RouteMetadata
	return meta, json.Unmarshal(b, &meta)
}

func (c *Client) DeleteRoute(ctx context.Context, domain string) error {
	return c.DeleteKV(ctx, "stackforge/routes/"+domain)
}

func (c *Client) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}
