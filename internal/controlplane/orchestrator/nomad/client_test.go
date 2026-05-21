package nomad

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJobsAndNodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/jobs":
			_, _ = w.Write([]byte(`[{"ID":"web","Status":"running"}]`))
		case "/v1/nodes":
			_, _ = w.Write([]byte(`[{"ID":"node1","Name":"node1","Status":"ready"}]`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := &Client{Addr: srv.URL, HTTPClient: srv.Client()}
	jobs, err := c.Jobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := c.Nodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if jobs[0].ID != "web" || nodes[0].Status != "ready" {
		t.Fatalf("unexpected results: %+v %+v", jobs, nodes)
	}
	if err := c.RefuseTemplateDeployment(); err == nil {
		t.Fatal("expected template deployment refusal")
	}
}
