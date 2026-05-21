package cloudflare

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpsertRecordUpdatesExistingRecord(t *testing.T) {
	var updated bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"rec1","type":"CNAME","name":"app.example.com","content":"old.example.com","proxied":false}]}`))
		case r.Method == http.MethodPut && r.URL.Path == "/zones/zone1/dns_records/rec1":
			updated = true
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"rec1"}}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	c := &Client{Token: "token", BaseURL: srv.URL, HTTPClient: srv.Client()}
	if err := c.UpsertRecord(context.Background(), "zone1", Record{Type: "CNAME", Name: "app.example.com", Content: "new.example.com"}); err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("expected update")
	}
}
