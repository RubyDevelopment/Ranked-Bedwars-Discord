package helpers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// HTML banners (the Banner Builder's HTML and CSS mode) are drawn by the dashboard, which has the layout engine for them. The bot sends the
// guild, the banner and the values of its variables to BANNER_RENDER_URL (the dashboard's address) with BANNER_RENDER_SECRET, and gets the
// picture back. The dashboard reads the banner from the guild's saved layouts, so the bot never carries the HTML.

const (
	remoteTimeout = 25 * time.Second
	maxRemotePNG  = 8 << 20
)

var remoteClient = &http.Client{Timeout: remoteTimeout}

// renderRemote has the dashboard draw an html banner. An error means there is no banner (the caller falls back to its embed).
func renderRemote(b *Banner, vars Vars) ([]byte, error) {
	base := strings.TrimRight(os.Getenv("BANNER_RENDER_URL"), "/")
	secret := os.Getenv("BANNER_RENDER_SECRET")
	if base == "" || secret == "" {
		return nil, errors.New("HTML banners need BANNER_RENDER_URL and BANNER_RENDER_SECRET set on the bot")
	}
	if b.guildID == "" {
		return nil, errors.New("html banner without a guild")
	}
	body, err := json.Marshal(map[string]any{"guild_id": b.guildID, "banner_id": b.ID, "vars": map[string]string(vars)})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/banners/render", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	res, err := remoteClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("html banner: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxRemotePNG+1))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		if failure.Error == "" {
			failure.Error = res.Status
		}
		return nil, fmt.Errorf("html banner: %s", failure.Error)
	}
	if len(data) > maxRemotePNG || !strings.HasPrefix(res.Header.Get("Content-Type"), "image/png") {
		return nil, errors.New("html banner: the dashboard did not answer with a picture")
	}
	return data, nil
}
