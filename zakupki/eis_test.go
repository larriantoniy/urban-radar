package zakupki

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEISFetchJSONFixtureAndBoundedLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "7" {
			t.Fatalf("limit = %q", r.URL.Query().Get("limit"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"1","object":"Строительство школы"},{"id":"2","object":"Ремонт дороги"}]`))
	}))
	defer server.Close()
	client := EISClient{Endpoint: server.URL, Client: server.Client()}
	raw, err := client.Fetch(context.Background(), Query{Limit: 7})
	if err != nil || len(raw) != 2 {
		t.Fatalf("raw=%d err=%v", len(raw), err)
	}
	if raw[0].DocumentType != "json" {
		t.Fatalf("document type = %q", raw[0].DocumentType)
	}
}

func TestEISFetchStatusErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests, http.StatusBadGateway} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		client := EISClient{Endpoint: server.URL, Client: server.Client()}
		if _, err := client.Fetch(context.Background(), Query{Limit: 1}); err == nil {
			t.Errorf("status %d: expected error", status)
		}
		server.Close()
	}
}
