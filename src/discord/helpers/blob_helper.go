package helpers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ============================================================================
// Vercel Blob
// ============================================================================

// BlobStore uploads files to a Vercel Blob store (the same store the dashboard
// reads game logs from). It is switched on by BLOB_READ_WRITE_TOKEN; without
// the token every upload is skipped, so the bot runs fine without logs.
//
// Blob URLs are private and only ever read through the dashboard's
// guild-checked API.
type BlobStore struct {
	Token  string
	APIURL string // https://vercel.com/api/blob unless VERCEL_BLOB_API_URL says otherwise
	HTTP   *http.Client
}

// Blobs is the process-wide store, configured from the environment.
var Blobs = NewBlobStoreFromEnv()

const (
	blobDefaultAPI = "https://vercel.com/api/blob"
	blobAPIVersion = "13"
)

// NewBlobStoreFromEnv reads BLOB_READ_WRITE_TOKEN (and VERCEL_BLOB_API_URL).
func NewBlobStoreFromEnv() *BlobStore {
	api := strings.TrimRight(strings.TrimSpace(os.Getenv("VERCEL_BLOB_API_URL")), "/")
	if api == "" {
		api = blobDefaultAPI
	}
	return &BlobStore{Token: strings.TrimSpace(os.Getenv("BLOB_READ_WRITE_TOKEN")), APIURL: api, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

// Enabled reports whether uploads will be attempted.
func (b *BlobStore) Enabled() bool { return b != nil && b.Token != "" }

// PutJSON stores v as JSON at pathname and returns its private URL.
func (b *BlobStore) PutJSON(ctx context.Context, pathname string, v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return b.Put(ctx, pathname, "application/json", raw)
}

// Put uploads data at pathname (a random suffix is added) and returns its URL.
func (b *BlobStore) Put(ctx context.Context, pathname, contentType string, data []byte) (string, error) {
	if !b.Enabled() {
		return "", fmt.Errorf("blob storage is not configured (BLOB_READ_WRITE_TOKEN)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, b.APIURL+"/?pathname="+url.QueryEscape(pathname), bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("authorization", "Bearer "+b.Token)
	req.Header.Set("x-api-version", blobAPIVersion)
	req.Header.Set("x-content-type", contentType)
	req.Header.Set("x-vercel-blob-access", "private")
	req.Header.Set("x-add-random-suffix", "1")
	req.Header.Set("x-cache-control-max-age", "31536000")

	resp, err := b.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("blob upload %s: %s: %s", pathname, resp.Status, strings.TrimSpace(string(body)))
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.URL == "" {
		return "", fmt.Errorf("blob upload %s: unreadable response", pathname)
	}
	return out.URL, nil
}
