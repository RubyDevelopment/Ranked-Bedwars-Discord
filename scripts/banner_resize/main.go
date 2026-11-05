// banner_resize lists the banners every server has saved and backs up each server's banner document; with --apply it writes a converted document back.
//
//	go run ./scripts/banner_resize                      (list every server's banners and write a backup of each document)
//	go run ./scripts/banner_resize --backup-dir <dir>   (where the backups go; default: ../banner-variants/backups)
//	go run ./scripts/banner_resize --apply <converted.json> --guild <discord id> --expect <backup.json> [--yes]
//
// DATABASE_API_URL and DATABASE_API_KEY are read from the bot's .env.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/joho/godotenv"
)

type banner struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Command string `json:"command"`
	Mode    string `json:"mode"`
	Enabled bool   `json:"enabled"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	HTML    string `json:"html"`
	CSS     string `json:"css"`
	Layers  []any  `json:"layers"`
}

type document struct {
	Version int                        `json:"version"`
	Banners []banner                   `json:"banners"`
	Assets  map[string]json.RawMessage `json:"assets"`
}

func main() {
	backupDir := flag.String("backup-dir", "../banner-variants/backups", "where the backups go")
	applyFile := flag.String("apply", "", "a converted banner document to write to --guild")
	guild := flag.String("guild", "", "the server (its Discord ID) to write to")
	expect := flag.String("expect", "", "the backup the server must still match before --apply writes")
	yes := flag.Bool("yes", false, "really write (without it --apply only previews)")
	flag.Parse()
	_ = godotenv.Load()

	svc := sdk.NewServicesWithOptions(sdk.Options{
		BaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("DATABASE_API_URL")), "/"),
		APIKey:  strings.TrimSpace(os.Getenv("DATABASE_API_KEY")),
		Timeout: 60 * time.Second,
	})
	if *applyFile != "" {
		if *guild == "" || *expect == "" {
			fmt.Fprintln(os.Stderr, "--apply needs --guild and --expect")
			os.Exit(2)
		}
		if err := apply(svc, *guild, *applyFile, *expect, *yes); err != nil {
			fmt.Fprintln(os.Stderr, "stopped:", err)
			os.Exit(1)
		}
		return
	}
	guilds, err := svc.Guild.ListAll("", false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not list the servers:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(*backupDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stamp := time.Now().Format("2006-01-02T15-04")
	fmt.Printf("%d servers\n\n", len(guilds))
	for _, g := range guilds {
		raw, err := svc.GuildConfig.Field(g.SnowflakeID, "banner_layouts")
		if err != nil || len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var doc document
		if json.Unmarshal(raw, &doc) != nil || len(doc.Banners) == 0 {
			continue
		}
		fmt.Printf("== %s  (snowflake %s, id %d)  %d banners, %d uploaded pictures, %d KB\n", g.Name, g.SnowflakeID, g.ID, len(doc.Banners), len(doc.Assets), len(raw)/1024)
		for _, b := range doc.Banners {
			fmt.Printf("   %-28s %-18s %-7s %4dx%-4d on=%-5v html %5d  css %5d  layers %d\n", trunc(b.Name, 28), b.Command, b.Mode, b.Width, b.Height, b.Enabled, len(b.HTML), len(b.CSS), len(b.Layers))
		}
		file := filepath.Join(*backupDir, fmt.Sprintf("%s-%s-%s.json", safe(g.Name), g.SnowflakeID, stamp))
		if err := os.WriteFile(file, raw, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "backup failed:", err)
			os.Exit(1)
		}
		fmt.Printf("   backup: %s\n\n", file)
	}
}

func trunc(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func safe(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// apply writes a converted banner document to one server, after checking that the server's banners are still exactly the ones that
// were backed up (so nobody's edit made in between is overwritten). Without --yes it only says what it would do.
func apply(svc *sdk.Services, guild, convertedFile, backupFile string, yes bool) error {
	converted, err := os.ReadFile(convertedFile)
	if err != nil {
		return err
	}
	backup, err := os.ReadFile(backupFile)
	if err != nil {
		return err
	}
	current, err := svc.GuildConfig.GetFresh(guild)
	if err != nil {
		return fmt.Errorf("reading the server's config: %w", err)
	}
	if !sameJSON([]byte(current.BannerLayouts), backup) {
		return fmt.Errorf("the server's banners changed since the backup %s was taken: nothing written", backupFile)
	}
	var doc document
	if err := json.Unmarshal(converted, &doc); err != nil || doc.Version != 2 || len(doc.Banners) == 0 {
		return fmt.Errorf("%s is not a banner document", convertedFile)
	}
	for _, b := range doc.Banners {
		fmt.Printf("   %-28s %-18s now %dx%d\n", trunc(b.Name, 28), b.Command, b.Width, b.Height)
	}
	if !yes {
		fmt.Println("   (preview only: add --yes to write it)")
		return nil
	}
	if err := svc.GuildConfig.SaveField(sdk.SystemActor, guild, "banner_layouts", json.RawMessage(converted)); err != nil {
		return fmt.Errorf("writing: %w", err)
	}
	after, err := svc.GuildConfig.GetFresh(guild)
	if err != nil {
		return fmt.Errorf("reading back: %w", err)
	}
	if !sameJSON([]byte(after.BannerLayouts), converted) {
		return fmt.Errorf("the saved document does not match what was written (restore with %s)", backupFile)
	}
	fmt.Println("   written and verified")
	return nil
}

// sameJSON reports whether two JSON documents hold the same data (key order and spacing do not matter).
func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return string(xa) == string(ya)
}
