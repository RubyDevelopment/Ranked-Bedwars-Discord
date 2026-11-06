// local_to_r2 uploads the folder made by blob_to_local to the R2 bucket, exactly the way the bot's storage package stores a file:
// the brotli bytes as they are on disk, at key <endpoint>/<bucket>/<key> (no random suffix), Content-Type application/json and
// Content-Encoding br. Nothing is re-compressed. An object that already exists in the bucket is skipped, so it can be run again.
//
//	go run ./scripts/local_to_r2 --dir C:/Users/me/Downloads/blob-export                 (preview: counts, uploads nothing)
//	go run ./scripts/local_to_r2 --dir C:/Users/me/Downloads/blob-export --yes           (uploads, verifies, writes blob_to_r2.sql)
//
// Settings come from the bot's .env (STORAGE_*). The SQL file points the database at the new addresses; it is for you to read and run.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/joho/godotenv"

	"ranked-bedwars/src/discord/helpers/storage"
)

func escapeKey(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", "''") + "'"
}

func main() {
	dir := flag.String("dir", "blob-export", "folder made by blob_to_local (with manifest.json)")
	yes := flag.Bool("yes", false, "upload (without it nothing is uploaded)")
	workers := flag.Int("workers", 8, "parallel uploads")
	sqlPath := flag.String("sql", "blob_to_r2.sql", "where the database update is written")
	flag.Parse()
	_ = godotenv.Load()

	s := storage.Files
	if !s.Enabled() {
		fmt.Println("The R2 bucket is not configured (STORAGE_ENDPOINT, STORAGE_BUCKET, STORAGE_ACCESS_KEY, STORAGE_SECRET_KEY)")
		os.Exit(1)
	}
	raw, err := os.ReadFile(filepath.Join(*dir, "manifest.json"))
	if err != nil {
		fmt.Println("Cannot read manifest.json:", err)
		os.Exit(1)
	}
	manifest := map[string]string{} // old Blob URL -> key
	if err := json.Unmarshal(raw, &manifest); err != nil {
		fmt.Println("Bad manifest.json:", err)
		os.Exit(1)
	}
	oldURLs := make([]string, 0, len(manifest))
	for old := range manifest {
		oldURLs = append(oldURLs, old)
	}
	sort.Strings(oldURLs)

	target := func(key string) string { return s.Endpoint + "/" + s.Bucket + "/" + escapeKey(key) }
	local := func(key string) string { return filepath.Join(*dir, filepath.FromSlash(key)+".br") }
	client := &http.Client{Timeout: 2 * time.Minute}

	do := func(method, key string, body []byte) (*http.Response, error) {
		req, err := http.NewRequestWithContext(context.Background(), method, target(key), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		if method == http.MethodPut {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Content-Encoding", "br")
		}
		sum := sha256.Sum256(body)
		payload := hex.EncodeToString(sum[:])
		req.Header.Set("x-amz-content-sha256", payload)
		storage.Sign(req, payload, s.Region, "s3", s.AccessKey, s.SecretKey, time.Now())
		return client.Do(req)
	}

	var missing []string
	for _, key := range manifest {
		if _, err := os.Stat(local(key)); err != nil {
			missing = append(missing, key)
		}
	}
	fmt.Printf("%d files in the manifest, %d missing on disk.\n", len(manifest), len(missing))
	if len(missing) > 0 {
		fmt.Println("Missing, e.g.:", missing[0], "- run blob_to_local again first.")
		os.Exit(1)
	}
	if !*yes {
		fmt.Printf("Preview only. Would upload to %s/%s, e.g. %s\nRun again with --yes.\n", s.Endpoint, s.Bucket, target(manifest[oldURLs[0]]))
		return
	}

	var (
		mu                        sync.Mutex
		uploaded, skipped, failed int
		sentBytes                 int64
		wg                        sync.WaitGroup
		jobs                      = make(chan string)
	)
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range jobs {
				data, err := os.ReadFile(local(key))
				var outcome string
				if err == nil {
					var resp *http.Response
					if resp, err = do(http.MethodHead, key, nil); err == nil {
						resp.Body.Close()
						if resp.StatusCode == http.StatusOK {
							outcome = "skipped"
						}
					}
				}
				if err == nil && outcome == "" {
					var resp *http.Response
					if resp, err = do(http.MethodPut, key, data); err == nil {
						body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
						resp.Body.Close()
						if resp.StatusCode/100 != 2 {
							err = fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
						} else {
							outcome = "uploaded"
						}
					}
				}
				mu.Lock()
				switch {
				case err != nil:
					failed++
					fmt.Printf("FAILED %s: %v\n", key, err)
				case outcome == "skipped":
					skipped++
				default:
					uploaded++
					sentBytes += int64(len(data))
					if uploaded%100 == 0 {
						fmt.Printf("  %d uploaded (%.1f MB)\n", uploaded, float64(sentBytes)/1e6)
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, old := range oldURLs {
		jobs <- manifest[old]
	}
	close(jobs)
	wg.Wait()
	fmt.Printf("Upload: %d uploaded, %d already there, %d failed.\n", uploaded, skipped, failed)
	if failed > 0 {
		fmt.Println("Run again to retry the failed ones. No SQL was written.")
		os.Exit(1)
	}

	// Verify every object against its local file.
	var bad int
	var vwg sync.WaitGroup
	vjobs := make(chan string)
	for i := 0; i < *workers; i++ {
		vwg.Add(1)
		go func() {
			defer vwg.Done()
			for key := range vjobs {
				want, _ := os.ReadFile(local(key))
				plain, _ := io.ReadAll(brotli.NewReader(bytes.NewReader(want)))
				ok := false
				// The stored object must be the br file (HEAD: encoding and size), and reading it back must give the original JSON.
				if head, err := do(http.MethodHead, key, nil); err == nil {
					head.Body.Close()
					if head.StatusCode == http.StatusOK && head.Header.Get("Content-Encoding") == "br" && head.ContentLength == int64(len(want)) {
						if resp, err := do(http.MethodGet, key, nil); err == nil {
							got, _ := io.ReadAll(resp.Body) // R2 hands the bytes back unpacked, unless the client asks for br
							resp.Body.Close()
							ok = resp.StatusCode == http.StatusOK && len(plain) > 0 && json.Valid(plain) && (bytes.Equal(got, plain) || bytes.Equal(got, want))
						}
					}
				}
				if !ok {
					mu.Lock()
					bad++
					fmt.Printf("VERIFY FAILED %s\n", key)
					mu.Unlock()
				}
			}
		}()
	}
	for _, old := range oldURLs {
		vjobs <- manifest[old]
	}
	close(vjobs)
	vwg.Wait()
	if bad > 0 {
		fmt.Printf("%d objects did not verify. No SQL was written.\n", bad)
		os.Exit(1)
	}
	fmt.Printf("Verified %d objects byte for byte.\n", len(manifest))

	var sql strings.Builder
	sql.WriteString("-- Points the database at the files in R2. Read it, then run it against the database.\nSTART TRANSACTION;\n")
	for _, old := range oldURLs {
		n := target(manifest[old])
		fmt.Fprintf(&sql, "UPDATE games SET runtime_metadata = REPLACE(runtime_metadata, %s, %s) WHERE runtime_metadata LIKE %s;\n", sqlQuote(old), sqlQuote(n), sqlQuote("%"+old+"%"))
		fmt.Fprintf(&sql, "UPDATE guild_tickets SET archive_url = %s WHERE archive_url = %s;\n", sqlQuote(n), sqlQuote(old))
	}
	sql.WriteString("COMMIT;\n")
	if err := os.WriteFile(*sqlPath, []byte(sql.String()), 0o600); err != nil {
		fmt.Println("Could not write the SQL file:", err)
		os.Exit(1)
	}
	fmt.Printf("SQL for %d files written to %s (not run).\n", len(manifest), *sqlPath)
}
