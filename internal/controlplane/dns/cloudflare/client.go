package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	Token      string
	BaseURL    string
	HTTPClient *http.Client
}

type Record struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

type responseEnvelope struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Errors  []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (c *Client) ListRecords(ctx context.Context, zoneID string, name string) ([]Record, error) {
	if c.Token == "" {
		return nil, fmt.Errorf("CLOUDFLARE_API_TOKEN is required")
	}
	if zoneID == "" {
		return nil, fmt.Errorf("Cloudflare zone id is required")
	}
	path := c.base() + "/zones/" + zoneID + "/dns_records"
	if name != "" {
		path += "?name=" + url.QueryEscape(name)
	}
	var records []Record
	if err := c.do(ctx, http.MethodGet, path, nil, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (c *Client) UpsertRecord(ctx context.Context, zoneID string, r Record) error {
	existing, err := c.ListRecords(ctx, zoneID, r.Name)
	if err != nil {
		return err
	}
	for _, current := range existing {
		if current.Type == r.Type && current.Name == r.Name {
			r.ID = current.ID
			if current.Content == r.Content && current.Proxied == r.Proxied {
				return nil
			}
			return c.UpdateRecord(ctx, zoneID, r)
		}
	}
	return c.CreateRecord(ctx, zoneID, r)
}

func (c *Client) CreateRecord(ctx context.Context, zoneID string, r Record) error {
	if c.Token == "" {
		return fmt.Errorf("CLOUDFLARE_API_TOKEN is required")
	}
	if zoneID == "" {
		return fmt.Errorf("Cloudflare zone id is required")
	}
	return c.do(ctx, http.MethodPost, c.base()+"/zones/"+zoneID+"/dns_records", r, nil)
}

func (c *Client) UpdateRecord(ctx context.Context, zoneID string, r Record) error {
	if r.ID == "" {
		return fmt.Errorf("record id is required for update")
	}
	return c.do(ctx, http.MethodPut, c.base()+"/zones/"+zoneID+"/dns_records/"+r.ID, r, nil)
}

func (c *Client) DeleteRecord(ctx context.Context, zoneID string, recordID string) error {
	if recordID == "" {
		return fmt.Errorf("record id is required for delete")
	}
	return c.do(ctx, http.MethodDelete, c.base()+"/zones/"+zoneID+"/dns_records/"+recordID, nil, nil)
}

func (c *Client) LookupZone(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("zone name is required")
	}
	var zones []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, c.base()+"/zones?name="+url.QueryEscape(name), nil, &zones); err != nil {
		return "", err
	}
	if len(zones) == 0 {
		return "", fmt.Errorf("cloudflare zone %s not found", name)
	}
	return zones[0].ID, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env responseEnvelope
	respBody, _ := io.ReadAll(resp.Body)
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &env)
	}
	if resp.StatusCode >= 300 || (len(respBody) > 0 && !env.Success) {
		if len(env.Errors) > 0 {
			return fmt.Errorf("cloudflare returned status %d: %s", resp.StatusCode, env.Errors[0].Message)
		}
		return fmt.Errorf("cloudflare returned status %d", resp.StatusCode)
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return "https://api.cloudflare.com/client/v4"
}

func (c *Client) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}
