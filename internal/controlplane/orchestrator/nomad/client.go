package nomad

import (
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

type Job struct {
	ID     string `json:"ID"`
	Status string `json:"Status,omitempty"`
}

type Node struct {
	ID     string `json:"ID"`
	Name   string `json:"Name"`
	Status string `json:"Status"`
}

func (c *Client) Jobs(ctx context.Context) ([]Job, error) {
	var jobs []Job
	if err := c.get(ctx, "/v1/jobs", &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (c *Client) Job(ctx context.Context, job string) (Job, error) {
	var out Job
	if err := c.get(ctx, "/v1/job/"+job, &out); err != nil {
		return Job{}, err
	}
	return out, nil
}

func (c *Client) JobStatus(ctx context.Context, job string) error {
	_, err := c.Job(ctx, job)
	return err
}

func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	var nodes []Node
	if err := c.get(ctx, "/v1/nodes", &nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (c *Client) RefuseTemplateDeployment() error {
	return fmt.Errorf("nomad job deployment templates are absent; refusing to submit jobs")
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	if c.Addr == "" {
		return fmt.Errorf("NOMAD_ADDR is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Addr+path, nil)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("X-Nomad-Token", c.Token)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("nomad returned status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (c *Client) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}
