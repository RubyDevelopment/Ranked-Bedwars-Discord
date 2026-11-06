// blob_to_local downloads every game log and ticket archive from the Vercel Blob store and saves each one on this computer,
// brotli-compressed exactly like the bot's storage package does (best compression), under the new R2 layout
// (games/<guild>/<game id>/<kind>-<suffix>.json.br; tickets keep their path). Nothing is uploaded anywhere.
//
//	go run ./scripts/blob_to_local                       (preview: lists the store and its size, downloads nothing)
//	go run ./scripts/blob_to_local --yes                 (downloads and compresses into --out)
//	go run ./scripts/blob_to_local --yes --max-mb 300    (stop after about 300 MB downloaded, run again later)
//
// It can be stopped and run again: a file already on disk is never downloaded twice. manifest.json in --out maps every
// old Blob URL to its new key, for the later upload to Cloudflare. Upload each .br file with Content-Encoding: br.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/joho/godotenv"
)

type blob struct {
	URL         string `json:"url"`
	DownloadURL string `json:"downloadUrl"`
	Pathname    string `json:"pathname"`
	Size        int64  `json:"size"`
}

// gameLog is games/<guild>/<id>-<kind>[-<suffix>].json, which now lives in a folder per game.
var gameLog = regexp.MustCompile(`^games/([^/]+)/(\d+)-(discord|game|bot)(-[0-9a-zA-Z]+)?\.json$`)

func newKey(pathname string) string {
	if m := gameLog.FindStringSubmatch(pathname); m != nil {
		return fmt.Sprintf("games/%s/%s/%s%s.json", m[1], m[2], m[3], m[4])
	}
	return pathname
}

func listBlobs(client *http.Client, apiURL, token string) ([]blob, error) {
	var all []blob
	cursor := ""
	for {
		q := url.Values{"limit": {"1000"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		req, _ := http.NewRequest(http.MethodGet, strings.TrimRight(apiURL, "/")+"/?"+q.Encode(), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("listing the store: %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}
		var page struct {
			Blobs   []blob `json:"blobs"`
			Cursor  string `json:"cursor"`
			HasMore bool   `json:"hasMore"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Blobs...)
		if !page.HasMore || page.Cursor == "" {
			return all, nil
		}
		cursor = page.Cursor
	}
}

func download(client *http.Client, b blob, token string) ([]byte, error) {
	src := b.DownloadURL
	if src == "" {
		src = b.URL
	}
	req, _ := http.NewRequest(http.MethodGet, src, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return io.ReadAll(resp.Body)
}

// brotliBytes is the same compression as storage.brotliBytes.
func brotliBytes(raw []byte) []byte {
	var out bytes.Buffer
	zw := brotli.NewWriterLevel(&out, brotli.BestCompression)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	return out.Bytes()
}

func main() {
	yes := flag.Bool("yes", false, "download and compress (without it nothing is downloaded)")
	maxMB := flag.Int64("max-mb", 0, "stop after about this many megabytes downloaded (0 = no limit)")
	out := flag.String("out", "blob-export", "folder the compressed files are written to")
	workers := flag.Int("workers", 4, "parallel downloads")
	flag.Parse()
	_ = godotenv.Load()

	token := strings.TrimSpace(os.Getenv("BLOB_READ_WRITE_TOKEN"))
	apiURL := strings.TrimSpace(os.Getenv("VERCEL_BLOB_API_URL"))
	if apiURL == "" {
		apiURL = "https://blob.vercel-storage.com"
	}
	if token == "" {
		fmt.Println("BLOB_READ_WRITE_TOKEN is not set in .env")
		os.Exit(1)
	}
	client := &http.Client{Timeout: 2 * time.Minute}

	blobs, err := listBlobs(client, apiURL, token)
	if err != nil {
		fmt.Println("Could not read the Vercel store:", err)
		os.Exit(1)
	}
	var total int64
	for _, b := range blobs {
		total += b.Size
	}
	fmt.Printf("The store holds %d files, %.1f MB.\n", len(blobs), float64(total)/1e6)

	target := func(b blob) string { return filepath.Join(*out, filepath.FromSlash(newKey(b.Pathname))+".br") }
	var todo []blob
	for _, b := range blobs {
		if _, err := os.Stat(target(b)); err != nil {
			todo = append(todo, b)
		}
	}
	fmt.Printf("%d already on disk, %d to go.\n", len(blobs)-len(todo), len(todo))
	if !*yes {
		for i, b := range todo {
			if i == 5 {
				fmt.Println("  ...")
				break
			}
			fmt.Printf("  %s  ->  %s\n", b.Pathname, target(b))
		}
		fmt.Println("Preview only. Run again with --yes to download.")
		return
	}

	var (
		mu         sync.Mutex
		downloaded int64
		stored     int64
		count      int
		failed     int
		wg         sync.WaitGroup
		jobs       = make(chan blob)
	)
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range jobs {
				data, err := download(client, b, token)
				if err == nil && !json.Valid(data) {
					err = fmt.Errorf("not valid JSON")
				}
				var packed []byte
				if err == nil {
					packed = brotliBytes(data)
					path := target(b)
					if err = os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
						if err = os.WriteFile(path+".part", packed, 0o644); err == nil {
							err = os.Rename(path+".part", path) // a half-written file is never taken for a finished one
						}
					}
				}
				mu.Lock()
				if err != nil {
					failed++
					fmt.Printf("FAILED %s: %v\n", b.Pathname, err)
				} else {
					downloaded += int64(len(data))
					stored += int64(len(packed))
					count++
					if count%100 == 0 {
						fmt.Printf("  %d done (%.0f MB downloaded, %.0f MB stored)\n", count, float64(downloaded)/1e6, float64(stored)/1e6)
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, b := range todo {
		mu.Lock()
		stop := *maxMB > 0 && downloaded >= *maxMB*1_000_000
		mu.Unlock()
		if stop {
			fmt.Println("Reached --max-mb, stopping. Run again to continue.")
			break
		}
		jobs <- b
	}
	close(jobs)
	wg.Wait()

	// Old Blob URL -> new key, for every file on disk, for the later upload.
	manifest := map[string]string{}
	for _, b := range blobs {
		if _, err := os.Stat(target(b)); err == nil {
			manifest[b.URL] = newKey(b.Pathname)
		}
	}
	raw, _ := json.MarshalIndent(manifest, "", "  ")
	_ = os.WriteFile(filepath.Join(*out, "manifest.json"), raw, 0o644)
	fmt.Printf("Done: %d downloaded this run (%.0f MB -> %.0f MB compressed), %d failed, %d of %d files on disk. Manifest: %s\n",
		count, float64(downloaded)/1e6, float64(stored)/1e6, failed, len(manifest), len(blobs), filepath.Join(*out, "manifest.json"))
	if failed > 0 {
		fmt.Println("Run again to retry the failed ones.")
	}
}
