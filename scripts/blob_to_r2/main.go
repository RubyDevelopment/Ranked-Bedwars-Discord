// blob_to_r2 moves the game logs and ticket archives out of the (suspended) Vercel Blob store into the R2 bucket, compressed.
//
// It lists the store, downloads every object, checks it is valid JSON, uploads it brotli-compressed to the bucket under the new layout
// (games/<guild>/<game id>/<kind>-<suffix>.json; tickets keep their path) and writes a SQL file that points the database at the new addresses.
//
//	go run ./scripts/blob_to_r2                                   (preview: lists what would move, downloads nothing)
//	go run ./scripts/blob_to_r2 --yes                             (downloads, uploads, writes blob_to_r2.sql)
//	go run ./scripts/blob_to_r2 --yes --max-mb 300                (stop after about 300 MB downloaded, run again later)
//
// Settings come from the bot's .env: BLOB_READ_WRITE_TOKEN (the Vercel store) and STORAGE_* (the bucket).
//
// It can be stopped and run again: what is done is remembered in blob_to_r2.state.json, and a finished object is never downloaded twice
// (the Vercel allowance is the scarce part). Nothing is ever deleted from Vercel or from the database; the SQL is for you to read and run.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"

	"ranked-bedwars/src/discord/helpers/storage"
)

type blob struct {
	URL         string `json:"url"`
	DownloadURL string `json:"downloadUrl"`
	Pathname    string `json:"pathname"`
	Size        int64  `json:"size"`
	UploadedAt  string `json:"uploadedAt"`
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
	req.Header.Set("Authorization", "Bearer "+token) // a private store needs it; a public one ignores it
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

func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", "''") + "'"
}

func main() {
	yes := flag.Bool("yes", false, "download and upload (without it nothing is downloaded)")
	maxMB := flag.Int64("max-mb", 0, "stop after about this many megabytes downloaded (0 = no limit)")
	statePath := flag.String("state", "blob_to_r2.state.json", "where progress is remembered")
	sqlPath := flag.String("sql", "blob_to_r2.sql", "where the database update is written")
	workers := flag.Int("workers", 4, "parallel transfers")
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
	if !storage.Files.Enabled() {
		fmt.Println("The R2 bucket is not configured (STORAGE_ENDPOINT, STORAGE_BUCKET, STORAGE_ACCESS_KEY, STORAGE_SECRET_KEY)")
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

	done := map[string]string{} // old URL -> new URL
	if raw, err := os.ReadFile(*statePath); err == nil {
		_ = json.Unmarshal(raw, &done)
	}
	var todo []blob
	for _, b := range blobs {
		if _, ok := done[b.URL]; !ok {
			todo = append(todo, b)
		}
	}
	fmt.Printf("%d already moved, %d to go.\n", len(blobs)-len(todo), len(todo))
	if !*yes {
		for i, b := range todo {
			if i == 5 {
				fmt.Println("  ...")
				break
			}
			fmt.Printf("  %s  ->  %s\n", b.Pathname, newKey(b.Pathname))
		}
		fmt.Println("Preview only. Run again with --yes to move them.")
		return
	}

	var (
		mu         sync.Mutex
		downloaded int64
		failed     int
		wg         sync.WaitGroup
		jobs       = make(chan blob)
		save       = func() { raw, _ := json.MarshalIndent(done, "", "  "); _ = os.WriteFile(*statePath, raw, 0o600) }
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
				var newURL string
				if err == nil {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					newURL, err = storage.Files.PutJSONExact(ctx, newKey(b.Pathname), data)
					cancel()
				}
				mu.Lock()
				if err != nil {
					failed++
					fmt.Printf("FAILED %s: %v\n", b.Pathname, err)
				} else {
					downloaded += int64(len(data))
					done[b.URL] = newURL
					if len(done)%25 == 0 {
						save()
						fmt.Printf("  %d moved (%.0f MB)\n", len(done), float64(downloaded)/1e6)
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
	save()

	// The database update: one pair of statements per moved file, in a transaction.
	olds := make([]string, 0, len(done))
	for old := range done {
		olds = append(olds, old)
	}
	sort.Strings(olds)
	var sql strings.Builder
	sql.WriteString("-- Points the database at the files moved to R2. Read it, then run it against the database.\nSTART TRANSACTION;\n")
	for _, old := range olds {
		fmt.Fprintf(&sql, "UPDATE games SET runtime_metadata = REPLACE(runtime_metadata, %s, %s) WHERE runtime_metadata LIKE %s;\n", sqlQuote(old), sqlQuote(done[old]), sqlQuote("%"+old+"%"))
		fmt.Fprintf(&sql, "UPDATE guild_tickets SET archive_url = %s WHERE archive_url = %s;\n", sqlQuote(done[old]), sqlQuote(old))
	}
	sql.WriteString("COMMIT;\n")
	if err := os.WriteFile(*sqlPath, []byte(sql.String()), 0o600); err != nil {
		fmt.Println("Could not write the SQL file:", err)
	}
	fmt.Printf("Done: %d moved in total, %d failed this run, %.0f MB downloaded. SQL for %d files written to %s.\n", len(done), failed, float64(downloaded)/1e6, len(done), *sqlPath)
	if failed > 0 {
		fmt.Println("Run again to retry the failed ones.")
	}
}
