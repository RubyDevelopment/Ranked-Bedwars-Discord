package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The calls below talk to the database API directly (the SDK has no client for server links yet).

// apiCall sends one JSON request to the database API and decodes the answer into out (when it is not nil).
func apiCall(method, path string, in, out any) error {
	if apiConn.baseURL == "" {
		return errors.New("database API is not configured")
	}
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, apiConn.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiConn.apiKey)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := (&http.Client{Timeout: max(apiConn.timeout, 10*time.Second)}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 300 {
		return &APIStatusError{Status: res.StatusCode, Message: apiMessage(raw)}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// APIStatusError is an answer of the database API that was not a success; Message is its own explanation when it gave one.
type APIStatusError struct {
	Status  int
	Message string
}

func (e *APIStatusError) Error() string { return fmt.Sprintf("database API answered %d: %s", e.Status, e.Message) }

func apiMessage(raw []byte) string {
	var e struct {
		Error   any    `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil {
		if e.Message != "" {
			return e.Message
		}
		switch v := e.Error.(type) {
		case string:
			return v
		case map[string]any:
			if m, ok := v["message"].(string); ok {
				return m
			}
		}
	}
	return strings.TrimSpace(string(raw))
}

// Link is a claimed link: a secondary server and the main guild it belongs to (Discord snowflakes).
type Link struct {
	VenueGuildID string `json:"venue_guild_id"`
	HomeGuildID  string `json:"home_guild_id"`
}

// Links lists every claimed link.
func Links() ([]Link, error) {
	var out []Link
	err := apiCall(http.MethodGet, "/v1/guild-links", nil, &out)
	return out, err
}

// ClaimLink links the secondary server to the guild that made the code.
func ClaimLink(code, venueGuildID, venueName string) (*Link, error) {
	var out Link
	err := apiCall(http.MethodPost, "/v1/guild-links/claim", map[string]string{"code": code, "venue_guild_id": venueGuildID, "venue_name": venueName}, &out)
	return &out, err
}

// LinkRow is one link of a main guild as the database stores it (pending while it still has a code).
type LinkRow struct {
	ID            uint       `json:"id"`
	VenueGuildID  *string    `json:"venue_guild_id"`
	VenueName     string     `json:"venue_name"`
	Code          *string    `json:"code"`
	CodeExpiresAt *time.Time `json:"code_expires_at"`
	CreatedBy     string     `json:"created_by"`
}

func linksPath(guildID string) string { return "/v1/guilds/" + url.PathEscape(guildID) + "/links" }

// GuildLinks lists the links (claimed and waiting for a claim) of a main guild.
func GuildLinks(guildID string) ([]LinkRow, error) {
	var out []LinkRow
	err := apiCall(http.MethodGet, linksPath(guildID), nil, &out)
	return out, err
}

// CreateGuildLink makes a one-time code a secondary server can claim within 30 minutes.
func CreateGuildLink(guildID, code, createdBy string) (*LinkRow, error) {
	var out LinkRow
	expires := time.Now().Add(30 * time.Minute).UTC()
	err := apiCall(http.MethodPost, linksPath(guildID), map[string]any{"code": code, "code_expires_at": expires, "created_by": createdBy}, &out)
	return &out, err
}

// DeleteGuildLink removes a link (or an unclaimed code) of a main guild.
func DeleteGuildLink(guildID string, id uint) error {
	return apiCall(http.MethodDelete, fmt.Sprintf("%s/%d", linksPath(guildID), id), nil, nil)
}

// GuildHasData says whether a guild has any registered player or game. A guild the bot created on joining a server has neither.
func GuildHasData(guildID string) (bool, error) {
	for _, kind := range []string{"players", "games"} {
		var rows []json.RawMessage
		if err := apiCall(http.MethodGet, "/v1/guilds/"+url.PathEscape(guildID)+"/"+kind+"?limit=1", nil, &rows); err != nil {
			return true, err // when unsure, treat it as a real guild
		}
		if len(rows) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// DeleteGuild removes a guild record (it can be restored; the bot restores one when the server is registered again).
func DeleteGuild(guildID string) error {
	return apiCall(http.MethodDelete, "/v1/guilds/"+url.PathEscape(guildID)+"/", nil, nil)
}
