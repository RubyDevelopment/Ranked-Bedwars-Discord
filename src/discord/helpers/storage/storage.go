// Package storage keeps the files the bot saves for the dashboard (the chat logs of finished games and ticket archives) in an object-storage
// bucket that speaks the S3 API: Cloudflare R2 by default in this project, and equally Backblaze B2, AWS S3 or Google Cloud Storage (through
// its S3-compatible endpoint). Only the endpoint and the keys differ between them.
package storage

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// Store uploads files to the bucket. It is switched on by STORAGE_BUCKET with its keys; without them every upload is skipped, so the bot
// runs fine without logs.
//
//	STORAGE_ENDPOINT    the S3 address, for R2 https://<account id>.r2.cloudflarestorage.com
//	STORAGE_BUCKET      the bucket's name
//	STORAGE_ACCESS_KEY, STORAGE_SECRET_KEY   the bucket's S3 key
//	STORAGE_REGION      "auto" unless the provider needs another
//
// The objects are private. The dashboard reads them with its own copy of the keys, through its guild-checked API.
type Store struct {
	Endpoint  string
	Bucket    string
	Region    string
	AccessKey string
	SecretKey string
	HTTP      *http.Client
}

// Files is the process-wide store, configured from the environment.
var Files = NewFromEnv()

const (
	defaultEndpoint = "https://storage.googleapis.com" // used only when STORAGE_ENDPOINT is not set
	defaultRegion   = "auto"
)

func env(name string) string { return strings.TrimSpace(os.Getenv(name)) }

// NewFromEnv reads the STORAGE_* settings.
func NewFromEnv() *Store {
	s := &Store{HTTP: &http.Client{Timeout: 60 * time.Second}}
	s.load()
	return s
}

func (s *Store) load() {
	s.Endpoint = strings.TrimRight(env("STORAGE_ENDPOINT"), "/")
	if s.Endpoint == "" {
		s.Endpoint = defaultEndpoint
	}
	s.Region = env("STORAGE_REGION")
	if s.Region == "" {
		s.Region = defaultRegion
	}
	s.Bucket, s.AccessKey, s.SecretKey = env("STORAGE_BUCKET"), env("STORAGE_ACCESS_KEY"), env("STORAGE_SECRET_KEY")
}

// Enabled reports whether uploads will be attempted.
func (s *Store) Enabled() bool {
	if s == nil {
		return false
	}
	if s.Bucket == "" { // Files is built before main loads .env, so look again
		s.load()
	}
	return s.Bucket != "" && s.AccessKey != "" && s.SecretKey != ""
}

// PutJSON stores v as JSON at key and returns the object's URL.
func (s *Store) PutJSON(ctx context.Context, key string, v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	var zipped bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&zipped, gzip.BestCompression)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	return s.put(ctx, key, "application/json", zipped.Bytes(), "gzip")
}

// Put uploads data at key (a random suffix is added before the extension, so an address cannot be guessed) and returns its URL.
func (s *Store) Put(ctx context.Context, key, contentType string, data []byte) (string, error) {
	return s.put(ctx, key, contentType, data, "")
}

// put is Put with an optional Content-Encoding. The bucket serves it back as stored, so readers (fetch) unpack gzip by themselves.
func (s *Store) put(ctx context.Context, key, contentType string, data []byte, encoding string) (string, error) {
	if !s.Enabled() {
		return "", fmt.Errorf("object storage is not configured (STORAGE_BUCKET, STORAGE_ACCESS_KEY, STORAGE_SECRET_KEY)")
	}
	key = withSuffix(strings.TrimLeft(key, "/"))
	target := s.Endpoint + "/" + s.Bucket + "/" + escapeKey(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", contentType)
	if encoding != "" {
		req.Header.Set("Content-Encoding", encoding)
	}
	sum := sha256.Sum256(data)
	payload := hex.EncodeToString(sum[:])
	req.Header.Set("x-amz-content-sha256", payload)
	Sign(req, payload, s.Region, "s3", s.AccessKey, s.SecretKey, time.Now())

	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("storage upload %s: %s: %s", key, resp.Status, strings.TrimSpace(string(body)))
	}
	return target, nil
}

// withSuffix puts a random string before the key's extension: games/1/2-discord.json becomes games/1/2-discord-3f9a1c7e2b40.json.
func withSuffix(key string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	ext := path.Ext(key)
	return strings.TrimSuffix(key, ext) + "-" + hex.EncodeToString(b[:]) + ext
}

// escapeKey percent-encodes each part of a key but keeps the slashes.
func escapeKey(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// ============================================================================
// AWS Signature Version 4
// ============================================================================

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// Sign adds the Authorization and X-Amz-Date headers of AWS Signature Version 4 to req. payloadHash is the hex SHA-256 of the body. Every
// header already on the request, plus Host, is signed.
func Sign(req *http.Request, payloadHash, region, service, accessKey, secretKey string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)

	// Canonical headers: lower-case names, sorted, trimmed values.
	headers := map[string]string{"host": req.URL.Host}
	for name, values := range req.Header {
		headers[strings.ToLower(name)] = strings.Join(strings.Fields(strings.Join(values, ",")), " ")
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name + ":" + headers[name] + "\n")
	}
	signed := strings.Join(names, ";")

	canonicalPath := req.URL.EscapedPath()
	if canonicalPath == "" {
		canonicalPath = "/"
	}
	query := req.URL.Query()
	pairs := make([]string, 0, len(query))
	for k, vs := range query {
		for _, v := range vs {
			pairs = append(pairs, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	sort.Strings(pairs)

	canonicalRequest := strings.Join([]string{req.Method, canonicalPath, strings.Join(pairs, "&"), canonicalHeaders.String(), signed, payloadHash}, "\n")
	scope := day + "/" + region + "/" + service + "/aws4_request"
	hash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(hash[:])

	key := hmacSHA256([]byte("AWS4"+secretKey), day)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, service)
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))

	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+signature)
}
