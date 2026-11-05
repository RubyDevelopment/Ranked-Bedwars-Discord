package storage

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The signing is checked against Amazon's published test case "get-vanilla" (a GET of / with only Host and X-Amz-Date signed): the same
// signature has to come out, whoever implements it.
func TestSignMatchesAmazonsTestCase(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	now := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	const emptyBody = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	Sign(req, emptyBody, "us-east-1", "service", "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", now)
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("authorization\n got  %s\n want %s", got, want)
	}
}

// An upload goes to <endpoint>/<bucket>/<key with a random suffix>, signed, with the body as it was.
func TestPutUploadsASignedObject(t *testing.T) {
	var gotPath, gotAuth, gotType, gotBody, gotEnc string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotType, gotEnc = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.Header.Get("Content-Encoding")
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("body is not gzip: %v", err)
			return
		}
		b, _ := io.ReadAll(zr)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s := &Store{Endpoint: srv.URL, Bucket: "rbw-storage", Region: "auto", AccessKey: "KEYID", SecretKey: "secret", HTTP: srv.Client()}
	if !s.Enabled() {
		t.Fatal("a configured store says it is off")
	}
	url, err := s.PutJSON(context.Background(), "games/123/45-discord.json", map[string]string{"hello": "world"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gotPath, "/rbw-storage/games/123/45-discord-") || !strings.HasSuffix(gotPath, ".json") || gotPath == "/rbw-storage/games/123/45-discord.json" {
		t.Errorf("object path %q has no random suffix", gotPath)
	}
	if url != srv.URL+gotPath {
		t.Errorf("returned %q, uploaded to %q", url, srv.URL+gotPath)
	}
	if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 Credential=KEYID/") || !strings.Contains(gotAuth, "/auto/s3/aws4_request") || !strings.Contains(gotAuth, "x-amz-content-sha256") {
		t.Errorf("authorization %q", gotAuth)
	}
	if gotEnc != "gzip" || gotType != "application/json" || gotBody != `{"hello":"world"}` {
		t.Errorf("type %q body %q", gotType, gotBody)
	}
}

func TestPutReportsRefusals(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("SignatureDoesNotMatch"))
	}))
	defer srv.Close()
	s := &Store{Endpoint: srv.URL, Bucket: "b", Region: "auto", AccessKey: "k", SecretKey: "s", HTTP: srv.Client()}
	if _, err := s.Put(context.Background(), "x.json", "application/json", []byte("{}")); err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Fatalf("error %v", err)
	}
}

func TestDisabledWithoutKeys(t *testing.T) {
	t.Setenv("STORAGE_BUCKET", "")
	s := &Store{HTTP: http.DefaultClient}
	if s.Enabled() {
		t.Fatal("a store without a bucket is on")
	}
	if _, err := s.Put(context.Background(), "x", "text/plain", nil); err == nil {
		t.Fatal("uploading without a store worked")
	}
}
