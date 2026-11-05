package match

import (
	"context"
	"encoding/json"
	"fmt"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/discord/helpers/storage"
	"ranked-bedwars/src/types"
	"slices"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"
)

// ============================================================================

// ============================================================================
// Game logs
// ============================================================================

// When a game is released, its two chats are written to object storage (Cloudflare R2) and their
// URLs are recorded in the game's runtime metadata ("logs"), where the
// dashboard's game results page picks them up:
//
//	discord: the messages of the game's text channel or thread (read just
//	         before the channels are deleted)
//	game:    the Minecraft chat the engine's bot saw (sent as a "game.chat" frame)
//	bot:     the engine's own record of the bot(s) of the game (chat, events, notes, reports), asked for with logs.read

const (
	gameLogVersion     = 1
	discordLogPages    = 20 // 100 messages each
	gameChatLineLimit  = 20000
	uploadDeadline = 90 * time.Second
)

// GameChatLine is defined in package types (games.go).
type GameChatLine = types.GameChatLine

// DiscordLogAuthor is defined in package types (games.go).
type DiscordLogAuthor = types.DiscordLogAuthor

// DiscordLogEmbed and its field and attachment types are defined in package types.
type DiscordLogEmbed = types.DiscordLogEmbed
type DiscordLogEmbedField = types.DiscordLogEmbedField
type DiscordLogAttachment = types.DiscordLogAttachment

// DiscordLogMessage is defined in package types (games.go).
type DiscordLogMessage = types.DiscordLogMessage

// GameLogRefs is defined in package types (games.go).
type GameLogRefs = types.GameLogRefs

// gameLogFile is the JSON document written to the storage bucket.
type gameLogFile struct {
	Version    int    `json:"version"`
	Kind       string `json:"kind"` // "discord" | "game" | "bot"
	GameID     uint   `json:"game_id"`
	GameNumber int    `json:"game_number"`
	GuildID    string `json:"guild_id"`
	CreatedAt  string `json:"created_at"`
	Messages   any    `json:"messages"`
}

// StashChat keeps the engine's Minecraft chat for a game until it is archived.
func (m *GameManager) StashChat(id uint, lines []GameChatLine) {
	if len(lines) == 0 {
		return
	}
	m.chatMu.Lock()
	defer m.chatMu.Unlock()
	if m.chat == nil {
		m.chat = map[uint][]GameChatLine{}
	}
	m.chat[id] = append(m.chat[id], lines...)
	if over := len(m.chat[id]) - gameChatLineLimit; over > 0 {
		m.chat[id] = m.chat[id][over:]
	}
}

func (m *GameManager) takeChat(id uint) []GameChatLine {
	m.chatMu.Lock()
	defer m.chatMu.Unlock()
	lines := m.chat[id]
	delete(m.chat, id)
	return lines
}

// archiveLogs writes both logs of a finished game to the storage bucket and records
// where they are. It runs before the game's channels are deleted, and does
// nothing when object storage is not configured.
func (m *GameManager) archiveLogs(g Game) {
	chat := m.takeChat(g.ID)
	if !storage.Files.Enabled() {
		// Game logs are part of a finished game's record on the dashboard: say loudly that this one is lost.
		m.logf("game %d: its chat logs were NOT saved. Game logs need object storage: set STORAGE_BUCKET, STORAGE_ACCESS_KEY and STORAGE_SECRET_KEY", g.ID)
		return
	}
	var discord []DiscordLogMessage
	if collect := m.fx().CollectChat; collect != nil && g.Channels.Text != "" {
		discord = collect(g.Channels)
	}
	botLog := m.collectBotLog(g)
	if len(discord) == 0 && len(chat) == 0 && len(botLog) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), uploadDeadline)
	defer cancel()
	put := func(kind string, messages any) string {
		file := gameLogFile{Version: gameLogVersion, Kind: kind, GameID: g.ID, GameNumber: g.Number, GuildID: g.GuildID, CreatedAt: time.Now().UTC().Format(time.RFC3339), Messages: messages}
		url, err := storage.Files.PutJSON(ctx, fmt.Sprintf("games/%s/%d-%s.json", g.GuildID, g.ID, kind), file)
		if err != nil {
			m.logf("archive %s log of game %d: %v", kind, g.ID, err)
		}
		return url
	}
	var refs GameLogRefs
	if len(discord) > 0 {
		refs.Discord = put("discord", discord)
	}
	if len(chat) > 0 {
		refs.Game = put("game", chat)
	}
	if len(botLog) > 0 {
		refs.Bot = put("bot", botLog)
	}
	if refs == (GameLogRefs{}) {
		return
	}
	m.recordLogs(g, refs)
}

// collectBotLog asks the engine for everything its bot(s) did in this game. Nothing when the engine is away or kept nothing.
func (m *GameManager) collectBotLog(g Game) []json.RawMessage {
	params, _ := json.Marshal(map[string]any{"game": g.ID, "span": true, "limit": 50000})
	result := HostEngine.Control(g.GuildID, "", "logs.read", params)
	if !result.OK {
		return nil
	}
	var data struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if json.Unmarshal(result.Data, &data) != nil {
		return nil
	}
	return data.Entries
}

// recordLogs merges the log URLs into the stored game's runtime metadata.
func (m *GameManager) recordLogs(g Game, refs GameLogRefs) {
	rec, err := m.api().GetGame(g.GuildID, g.ID)
	if err != nil {
		m.logf("record logs of game %d: %v", g.ID, err)
		return
	}
	meta := map[string]any{}
	if !rec.RuntimeMetadata.IsEmpty() {
		_ = rec.RuntimeMetadata.Unmarshal(&meta)
	}
	meta["logs"] = refs
	raw, err := json.Marshal(meta)
	if err != nil {
		return
	}
	if _, err := m.api().UpdateGame(sdk.SystemActor, g.GuildID, g.ID, map[string]any{"runtime_metadata": json.RawMessage(raw)}); err != nil {
		m.logf("record logs of game %d: %v", g.ID, err)
	}
}

// ============================================================================
// Reading the Discord chat
// ============================================================================

// NewGameChatCollector returns the CollectChat effect: it reads the game's text
// channel (or thread), oldest message first. session returns the live session.
func NewGameChatCollector(session func() *discordgo.Session) func(ch GameChannels) []DiscordLogMessage {
	return func(ch GameChannels) []DiscordLogMessage {
		s := session()
		if s == nil || ch.Text == "" {
			return nil
		}
		var out []DiscordLogMessage
		before := ""
		for range discordLogPages {
			page, err := s.ChannelMessages(ch.Text, 100, before, "", "")
			if err != nil || len(page) == 0 {
				break
			}
			for _, msg := range page { // newest first
				out = append(out, DiscordLogMessageOf(msg))
			}
			before = page[len(page)-1].ID
			if len(page) < 100 {
				break
			}
		}
		slices.Reverse(out)
		return out
	}
}

func DiscordLogMessageOf(msg *discordgo.Message) DiscordLogMessage {
	out := DiscordLogMessage{ID: msg.ID, At: msg.Timestamp}
	if msg.EditedTimestamp != nil {
		out.EditedAt = msg.EditedTimestamp
	}
	out.Pinned = msg.Pinned
	if a := msg.Author; a != nil {
		out.Author = DiscordLogAuthor{ID: a.ID, Name: base.FirstNonEmpty(a.GlobalName, a.Username), Username: a.Username, Avatar: a.AvatarURL("64"), Bot: a.Bot}
	}
	if msg.Member != nil && msg.Member.Nick != "" {
		out.Author.Name = msg.Member.Nick
	}

	out.Content = strings.TrimSpace(msg.Content)
	for _, e := range msg.Embeds {
		embed := DiscordLogEmbed{Title: e.Title, Description: e.Description, URL: e.URL, Color: e.Color, Timestamp: e.Timestamp}
		if e.Author != nil {
			embed.Author = e.Author.Name
		}
		if e.Footer != nil {
			embed.Footer = e.Footer.Text
		}
		if e.Image != nil {
			embed.ImageURL = e.Image.URL
		}
		if e.Thumbnail != nil {
			embed.ThumbnailURL = e.Thumbnail.URL
		}
		for _, f := range e.Fields {
			if f != nil {
				embed.Fields = append(embed.Fields, DiscordLogEmbedField{Name: f.Name, Value: f.Value, Inline: f.Inline})
			}
		}
		out.Embeds = append(out.Embeds, embed)
	}
	out.Components = helpers.ComponentText(msg.Components)
	for _, a := range msg.Attachments {
		out.Attachments = append(out.Attachments, DiscordLogAttachment{Name: a.Filename, URL: a.URL, ContentType: a.ContentType, Size: a.Size})
	}
	for _, reaction := range msg.Reactions {
		if reaction == nil || reaction.Emoji == nil {
			continue
		}
		out.Reactions = append(out.Reactions, types.DiscordLogReaction{
			Name: reaction.Emoji.Name, ID: reaction.Emoji.ID, Count: reaction.Count, Me: reaction.Me,
		})
	}
	if len(msg.Mentions) > 0 {
		out.Mentions = make(map[string]types.DiscordLogMention, len(msg.Mentions))
		for _, u := range msg.Mentions {
			out.Mentions[u.ID] = types.DiscordLogMention{
				ID:       u.ID,
				Name:     base.FirstNonEmpty(u.GlobalName, u.Username),
				Username: u.Username,
			}
		}
	}
	return out
}

// ============================================================================
// Types
