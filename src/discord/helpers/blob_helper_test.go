package helpers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBlobStorePutUsesPrivateAccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %q, want PUT", r.Method)
		}
		if got := r.Header.Get("x-vercel-blob-access"); got != "private" {
			t.Errorf("blob access = %q, want private", got)
		}
		if got := r.Header.Get("x-api-version"); got != blobAPIVersion {
			t.Errorf("API version = %q, want %q", got, blobAPIVersion)
		}
		if got := r.Header.Get("authorization"); got != "Bearer test-token" {
			t.Errorf("authorization = %q, want test token", got)
		}
		if got := r.URL.Query().Get("pathname"); got != "games/123/4-discord.json" {
			t.Errorf("pathname = %q, want games/123/4-discord.json", got)
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"url": "https://store.private.blob.vercel-storage.com/games/123/4-discord.json"})
	}))
	defer server.Close()

	store := &BlobStore{Token: "test-token", APIURL: server.URL, HTTP: server.Client()}
	url, err := store.PutJSON(context.Background(), "games/123/4-discord.json", map[string]string{"kind": "discord"})
	if err != nil {
		t.Fatalf("PutJSON returned error: %v", err)
	}
	if url != "https://store.private.blob.vercel-storage.com/games/123/4-discord.json" {
		t.Fatalf("PutJSON URL = %q, want private blob URL", url)
	}
}
