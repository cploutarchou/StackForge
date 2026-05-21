package consul

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteAndReadRoute(t *testing.T) {
	store := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path[len("/v1/kv/"):]
		switch r.Method {
		case http.MethodPut:
			var meta RouteMetadata
			if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(meta)
			store[key] = b
			_, _ = w.Write([]byte("true"))
		case http.MethodGet:
			_, _ = w.Write(store[key])
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()
	c := &Client{Addr: srv.URL, HTTPClient: srv.Client()}
	if err := c.WriteRoute(context.Background(), "app.example.com", RouteMetadata{Domain: "app.example.com", Service: "web", Port: 8080}); err != nil {
		t.Fatal(err)
	}
	meta, err := c.ReadRoute(context.Background(), "app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Service != "web" || meta.Port != 8080 {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
}
