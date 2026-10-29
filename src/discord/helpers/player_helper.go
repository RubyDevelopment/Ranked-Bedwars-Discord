package helpers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
	appLog "ranked-bedwars/src/utility"
)

type discordImageFetchError struct {
	statusCode int
}

func (err *discordImageFetchError) Error() string {
	return fmt.Sprintf("failed to fetch image: HTTP %d", err.statusCode)
}

func isMissingDiscordImage(err error) bool {
	var fetchErr *discordImageFetchError
	return errors.As(err, &fetchErr) && fetchErr.statusCode == http.StatusNotFound
}

// ============================================================================
// Account age
// ============================================================================

// A guild can require Discord accounts to be a minimum age before they queue. A younger account is queue-banned
// automatically, for a set number of days or for good, unless it is on the whitelist. The settings are the dashboard's
// Account Age page: `account_age` inside the guild's registration_verification, plus the account_age_whitelist list.

func appLogf(format string, a ...any) {
	appLog.EngineLogger.Error("[AccountAge] " + fmt.Sprintf(format, a...))
}

// AccountAgeReason starts the reason of every automatic ban, so these bans can be told from staff ones.
const AccountAgeReason = "Discord account too new"

// AccountAgeConfig is a guild's account age settings.
type AccountAgeConfig struct {
	Enabled bool `json:"enabled"`
	MinDays int  `json:"minDays"` // the youngest account allowed, in days
	BanDays int  `json:"banDays"` // how long the queue ban lasts, in days; 0 = permanent

	Whitelist []string `json:"-"` // Discord user ids that are never checked
}

var accountAgeCache = newTTLCache[AccountAgeConfig](eloConfigCacheTTL)

// LoadAccountAgeConfig reads the guild's settings (cached briefly). A failed read counts as "off".
func LoadAccountAgeConfig(guildID string) AccountAgeConfig {
	cfg, _ := accountAgeCache.get(guildID, func() (AccountAgeConfig, error) {
		reg, err := database.Must().GuildConfig.Field(guildID, "registration_verification")
		if err != nil && !sdk.IsNotFound(err) {
			return AccountAgeConfig{}, err
		}
		list, err := database.Must().GuildConfig.Field(guildID, "account_age_whitelist")
		if err != nil && !sdk.IsNotFound(err) {
			return AccountAgeConfig{}, err
		}
		return parseAccountAge(reg, list), nil
	})
	return cfg
}

func parseAccountAge(registration, whitelist json.RawMessage) AccountAgeConfig {
	var holder struct {
		AccountAge AccountAgeConfig `json:"account_age"`
	}
	_ = json.Unmarshal(registration, &holder)
	cfg := holder.AccountAge
	cfg.MinDays = max(0, cfg.MinDays)
	cfg.BanDays = max(0, cfg.BanDays)
	_ = json.Unmarshal(whitelist, &cfg.Whitelist)
	return cfg
}

// AccountCreatedAt is when a Discord account was made: a snowflake carries its creation time.
func AccountCreatedAt(userID string) time.Time {
	id, err := strconv.ParseUint(userID, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(int64(id>>22) + 1420070400000)
}

// tooYoung reports whether the account fails the age rule.
func (c AccountAgeConfig) tooYoung(userID string, now time.Time) bool {
	if !c.Enabled || c.MinDays <= 0 || slices.Contains(c.Whitelist, userID) {
		return false
	}
	created := AccountCreatedAt(userID)
	return !created.IsZero() && now.Sub(created) < time.Duration(c.MinDays)*24*time.Hour
}

// EnforceAccountAge checks the user against the guild's rule and, when the account is too new, queue-bans it
// (once: an account whose ban has run out is not banned again). It returns a message for the member when they are
// turned away, or "" when they may queue.
func EnforceAccountAge(s *discordgo.Session, guildID string, user *discordgo.User) string {
	cfg := LoadAccountAgeConfig(guildID)
	if user == nil || user.Bot || !cfg.tooYoung(user.ID, time.Now()) {
		return ""
	}

	playerID, _, err := EnsureModerationTarget(actorSystem, guildID, user)
	if err != nil {
		appLogf("account age: load player %s in guild %s: %v", user.ID, guildID, err)
		return ""
	}
	pid := sdk.UintToString(playerID)

	history, err := database.Must().Punishments.ByPlayer(guildID, pid, false, "ban", sdk.Page{})
	if err != nil {
		appLogf("account age: punishments of %s in guild %s: %v", user.ID, guildID, err)
		return ""
	}
	for _, row := range history.Data {
		if strings.HasPrefix(row.Reason, AccountAgeReason) {
			return denialMessage(cfg, user.ID, row.ExpiresAt) // already banned for this (or served it)
		}
	}

	reason := fmt.Sprintf("%s: younger than %d days (automatic)", AccountAgeReason, cfg.MinDays)
	expires := ""
	var expiresAt *time.Time
	if cfg.BanDays > 0 {
		t := time.Now().Add(time.Duration(cfg.BanDays) * 24 * time.Hour).UTC()
		expires, expiresAt = t.Format(time.RFC3339), &t
	}
	if _, err := database.Must().Punishments.Create(actorSystem, guildID, pid, sdk.NewPunishment{
		StaffID: "000000000000000000", Reason: reason, Type: "ban", ExpiresAt: expires,
	}); err != nil {
		appLogf("account age: ban %s in guild %s: %v", user.ID, guildID, err)
		return ""
	}
	TrackPunishmentExpiry(guildID, expires)
	return denialMessage(cfg, user.ID, expiresAt)
}

func denialMessage(cfg AccountAgeConfig, userID string, expires *time.Time) string {
	msg := fmt.Sprintf("Your Discord account is too new: accounts must be at least **%d days** old to queue.", cfg.MinDays)
	switch {
	case expires == nil:
		msg += " You are banned from queueing."
	case expires.After(time.Now()):
		msg += fmt.Sprintf(" You are banned from queueing until <t:%d:F>.", expires.Unix())
	default:
		msg += " Your ban has ended."
	}
	return msg
}

const AccountAgeEventName = "Account Age Check"

func HandleAccountAge(s *discordgo.Session, m *discordgo.GuildMemberAdd) {
	if m == nil || m.Member == nil || m.User == nil || !EventEnabled(m.GuildID, AccountAgeEventName) {
		return
	}
	msg := EnforceAccountAge(s, m.GuildID, m.User)
	if msg == "" {
		return
	}
	// Tell them why, if their DMs are open.
	if dm, err := s.UserChannelCreate(m.User.ID); err == nil {
		denied := queueDeniedEmbed(msg)
		denied.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: ThumbPlayers}
		_, _ = s.ChannelMessageSendEmbed(dm.ID, denied)
	}
}

// ============================================================================
// Types & Constructor
// ============================================================================

// DiscordRestAPI handles direct REST API calls to Discord for features not fully supported by Discordgo
type DiscordRestAPI struct {
	httpClient *http.Client
	baseURL    string
	botToken   string
}

// GuildMember is defined in package types (players.go).
type GuildMember = types.GuildMember

// NewDiscordRestAPI creates a new Discord REST API client
func NewDiscordRestAPI(botToken string) *DiscordRestAPI {
	return &DiscordRestAPI{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL:  "https://discord.com/api/v10",
		botToken: botToken,
	}
}

// SetBotToken updates the bot token for the API client
func (api *DiscordRestAPI) SetBotToken(token string) {
	api.botToken = token
}

// ============================================================================
// Guild Member API Methods
// ============================================================================

// UpdateGuildMemberProfile changes the bot's own profile in one server (nick, avatar, banner, bio) with a single request
// that holds only the fields in `fields`: a field that is not in the map is left exactly as it is, and a nil value clears it.
// Never send these one at a time as a whole GuildMember: every field of it that is not set would be sent as null, and Discord
// clears whatever it receives as null.
//
// If Discord refuses the combined request (one picture is rejected, say), each field is tried on its own, so the others still
// land. It returns the fields that were changed and, for the ones that were not, why.
func (api *DiscordRestAPI) UpdateGuildMemberProfile(guildID string, fields map[string]any) (done []string, failures map[string]error) {
	endpoint := fmt.Sprintf("%s/guilds/%s/members/@me", api.baseURL, guildID)
	err := api.makeRequest("PATCH", endpoint, fields)
	if err == nil {
		for key := range fields {
			done = append(done, key)
		}
		return done, nil
	}
	if len(fields) == 1 {
		for key := range fields {
			return nil, map[string]error{key: err}
		}
	}
	failures = map[string]error{}
	for key, value := range fields {
		if one := api.makeRequest("PATCH", endpoint, map[string]any{key: value}); one != nil {
			failures[key] = one
		} else {
			done = append(done, key)
		}
	}
	return done, failures
}

// discordProfileText is a nick or bio as Discord takes it: empty clears it (null), and it is cut to `limit` characters.
func discordProfileText(text string, limit int) any {
	if text == "" {
		return nil
	}
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit])
	}
	return text
}

// GetGuildMember retrieves the current guild member info via REST API
func (api *DiscordRestAPI) GetGuildMember(guildID string) (*GuildMember, error) {
	endpoint := fmt.Sprintf("%s/guilds/%s/members/@me", api.baseURL, guildID)

	member := &GuildMember{}
	err := api.makeRequest("GET", endpoint, nil, member)
	if err != nil {
		return nil, err
	}

	return member, nil
}

// ============================================================================
// Internal Helpers
// ============================================================================

// makeRequest makes an HTTP request to Discord's API
func (api *DiscordRestAPI) makeRequest(method, endpoint string, body any, responseTarget ...any) error {
	var bodyReader io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequest(method, endpoint, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bot "+api.botToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "RankedBedwarsBot (https://rubydevelopment.com, 1.0)")

	resp, err := api.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Handle rate limiting
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("rate limited by Discord API")
	}

	// Handle other errors
	if resp.StatusCode >= 400 {
		errorBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Discord API error (HTTP %d): %s", resp.StatusCode, string(errorBody))
	}

	// Parse response if target provided
	if len(responseTarget) > 0 && responseTarget[0] != nil {
		responseData, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response body: %w", err)
		}

		if err := json.Unmarshal(responseData, responseTarget[0]); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}

// fetchImageAsBase64 downloads an image and converts it to base64 data URI
func (api *DiscordRestAPI) fetchImageAsBase64(imageURL string) (string, error) {
	if imageURL == "" {
		return "", fmt.Errorf("empty image URL")
	}

	// If already a data URI, return as-is
	if strings.HasPrefix(imageURL, "data:image") {
		return imageURL, nil
	}

	resp, err := api.httpClient.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", &discordImageFetchError{statusCode: resp.StatusCode}
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		return "", fmt.Errorf("invalid content type: %s", contentType)
	}

	// Read image data
	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read image data: %w", err)
	}

	// Convert to base64 data URI
	base64Data := base64.StdEncoding.EncodeToString(imageData)

	// Determine appropriate MIME type
	mimeType := "image/png"
	if strings.Contains(contentType, "jpeg") || strings.Contains(contentType, "jpg") {
		mimeType = "image/jpeg"
	} else if strings.Contains(contentType, "webp") {
		mimeType = "image/webp"
	} else if strings.Contains(contentType, "gif") {
		mimeType = "image/gif"
	}

	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data), nil
}

const (
	HistoryPerPage = 5
)

// HistoryPageFooter builds the shared pagination footer text.
func HistoryPageFooter(page, totalPages, totalFound int) string {
	return fmt.Sprintf("Ranked Bedwars • Page %d/%d • Total: %d", page+1, totalPages, totalFound)
}

// HistoryTotalPages returns the page count for a history list.
func HistoryTotalPages(total int) int {
	if total <= 0 {
		return 1
	}
	pages := (total + HistoryPerPage - 1) / HistoryPerPage
	if pages < 1 {
		return 1
	}
	return pages
}

const (
	EmojiRedDot    = "<:red_dot:1490569600805372085>"
	EmojiYellowDot = "<:yellow_dot:1490569602076512410>"
)

// FilterStrikeHistoryEntries filters strike entries by time duration
func FilterStrikeHistoryEntries(entries []models.PlayerStrikesModel, window time.Duration) []models.PlayerStrikesModel {
	if window <= 0 {
		return entries
	}
	cutoff := time.Now().Add(-window)
	filtered := make([]models.PlayerStrikesModel, 0, len(entries))
	for _, entry := range entries {
		if entry.CreatedAt.After(cutoff) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// FilterPunishmentHistoryEntries filters punishment entries by active status and time duration
func FilterPunishmentHistoryEntries(entries []models.PlayerPunishmentsModel, activeOnly bool, window time.Duration) []models.PlayerPunishmentsModel {
	filtered := make([]models.PlayerPunishmentsModel, 0, len(entries))
	cutoff := time.Now()
	if window > 0 {
		cutoff = time.Now().Add(-window)
	}

	for _, entry := range entries {
		// Filter by active status
		if activeOnly && (!entry.IsEnabled || entry.IsVoided) {
			continue
		}
		// Filter by time window
		if window > 0 && entry.CreatedAt.Before(cutoff) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// FilterDecayHistoryEntries filters decay entries by time duration
func FilterDecayHistoryEntries(entries []models.PlayerDecaysModel, window time.Duration) []models.PlayerDecaysModel {
	if window <= 0 {
		return entries
	}
	cutoff := time.Now().Add(-window)
	filtered := make([]models.PlayerDecaysModel, 0, len(entries))
	for _, entry := range entries {
		if entry.CreatedAt.After(cutoff) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// FilterGameHistoryEntries keeps a player's games from one season, optionally
// dropping voided matches and anything older than window, newest first.
func FilterGameHistoryEntries(participants []sdk.GameHistory, seasonID uint, window time.Duration, hideVoided bool) []sdk.GameHistory {
	cutoff := time.Now().Add(-window)
	entries := make([]sdk.GameHistory, 0, len(participants))
	for _, participant := range participants {
		game := participant.Game
		if game == nil || game.SeasonID != seasonID {
			continue
		}
		if hideVoided && game.Status == string(types.GameStatusVoided) {
			continue
		}
		if window > 0 && gameWhen(*game).Before(cutoff) {
			continue
		}
		entries = append(entries, participant)
	}
	sort.Slice(entries, func(a, b int) bool {
		return gameWhen(*entries[a].Game).After(gameWhen(*entries[b].Game))
	})
	return entries
}

func formatStaffDisplay(staffID string) string {
	if staffID == "" {
		return "Unknown"
	}
	return fmt.Sprintf("<@%s>", staffID)
}

// BuildStrikeHistoryDescriptionsDirect builds description blocks for one page of strike history.
func BuildStrikeHistoryDescriptionsDirect(entries []models.PlayerStrikesModel, page int) []string {
	start := page * HistoryPerPage
	if start >= len(entries) {
		return nil
	}
	end := start + HistoryPerPage
	if end > len(entries) {
		end = len(entries)
	}

	descriptions := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		strike := entries[i]
		emoji := DotGreen
		status := strikeHistoryStatus(strike, time.Now())
		switch {
		case strike.IsVoided:
			emoji = DotGray
		case status != "Active":
			emoji = DotRed
		}

		valueLines := []string{
			fmt.Sprintf("> **Staff:** %s", formatStaffDisplay(strike.StaffID)),
			fmt.Sprintf("> **Reason:** %s", strike.Reason),
			fmt.Sprintf("> **Issued:** %s", DiscordRelativeTimestamp(strike.CreatedAt)),
			"> **Expires:** Never",
		}
		if strike.ExpiresAt != nil {
			valueLines[len(valueLines)-1] = fmt.Sprintf("> **Expires:** %s", DiscordRelativeTimestamp(*strike.ExpiresAt))
		}

		descriptions = append(descriptions, fmt.Sprintf("%s **Strike #%d | %s**\n%s", emoji, strike.ID, status, strings.Join(valueLines, "\n")))
	}
	return descriptions
}

func strikeHistoryStatus(strike models.PlayerStrikesModel, now time.Time) string {
	switch {
	case strike.IsVoided:
		return "Voided"
	case strike.IsAppealed:
		return "Appealed"
	case strike.ExpiresAt != nil && !strike.ExpiresAt.After(now):
		return "Expired"
	case !strike.IsEnabled:
		return "Disabled"
	default:
		return "Active"
	}
}

// BuildDecayHistoryDescriptionsDirect builds description blocks for one page of ELO decay history.
func BuildDecayHistoryDescriptionsDirect(entries []models.PlayerDecaysModel, page int) []string {
	start := page * HistoryPerPage
	if start >= len(entries) {
		return nil
	}
	end := start + HistoryPerPage
	if end > len(entries) {
		end = len(entries)
	}

	descriptions := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		decay := entries[i]
		emoji := DotGreen
		if decay.IsVoided {
			emoji = DotGray
		}

		valueLines := []string{
			fmt.Sprintf("> **Amount:** `-%d ELO`", decay.AmountDeducted),
			fmt.Sprintf("> **ELO:** `%d` → `%d`", decay.EloBefore, decay.EloAfter),
			fmt.Sprintf("> **Inactive:** `%d` day(s)", decay.DaysInactive),
		}
		if strings.TrimSpace(decay.Reason) != "" {
			valueLines = append(valueLines, fmt.Sprintf("> **Reason:** %s", decay.Reason))
		}
		valueLines = append(valueLines, fmt.Sprintf("> **Applied:** %s", DiscordRelativeTimestamp(decay.CreatedAt)))
		if decay.IsVoided {
			valueLines = append(valueLines, "> **Status:** Voided")
		}

		descriptions = append(descriptions, fmt.Sprintf("%s **Decay #%d**\n%s", emoji, decay.ID, strings.Join(valueLines, "\n")))
	}
	return descriptions
}

// BuildPunishmentHistoryDescriptionsDirect builds description blocks for one page of punishment history.
func BuildPunishmentHistoryDescriptionsDirect(entries []models.PlayerPunishmentsModel, page int) []string {
	start := page * HistoryPerPage
	if start >= len(entries) {
		return nil
	}
	end := start + HistoryPerPage
	if end > len(entries) {
		end = len(entries)
	}

	descriptions := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		punishment := entries[i]
		emoji := DotGreen
		status := punishmentHistoryStatus(punishment, time.Now())
		switch {
		case punishment.IsVoided || status == "Revoked":
			emoji = DotGray
		case status != "Active":
			emoji = DotRed
		}

		valueLines := []string{
			fmt.Sprintf("> **Type:** `%s`", punishment.Type),
			fmt.Sprintf("> **Staff:** %s", formatStaffDisplay(punishment.StaffID)),
			fmt.Sprintf("> **Reason:** %s", punishment.Reason),
			fmt.Sprintf("> **Issued:** %s", DiscordRelativeTimestamp(punishment.CreatedAt)),
			"> **Expires:** Never",
		}
		if punishment.ExpiresAt != nil {
			expiryLabel := "Expires"
			if !punishment.ExpiresAt.After(time.Now()) {
				expiryLabel = "Expired"
			}
			valueLines[len(valueLines)-1] = fmt.Sprintf("> **%s:** %s", expiryLabel, DiscordRelativeTimestamp(*punishment.ExpiresAt))
		}

		descriptions = append(descriptions, fmt.Sprintf("%s **%s #%d | %s**\n%s", emoji, strings.Title(punishment.Type), punishment.ID, status, strings.Join(valueLines, "\n")))
	}
	return descriptions
}

func punishmentHistoryStatus(punishment models.PlayerPunishmentsModel, now time.Time) string {
	switch {
	case punishment.IsVoided:
		return "Voided"
	case punishment.IsAppealed:
		return "Appealed"
	case punishment.RevokedAt != nil || punishment.RevokedBy != nil:
		return "Revoked"
	case punishment.ExpiresAt != nil && !punishment.ExpiresAt.After(now):
		return "Expired"
	case !punishment.IsEnabled:
		return "Disabled"
	default:
		return "Active"
	}
}

// BuildGameHistoryDescriptionsDirect builds description blocks for one page of game history.
func BuildGameHistoryDescriptionsDirect(entries []sdk.GameHistory, page int) []string {
	return buildGameHistoryDescriptions(entries, page, nil)
}

type GameHistoryPlayer struct {
	Username string
	IsWinner bool
}

type gameHistoryParticipantView struct {
	models.GamePlayersModel
	Username    string `json:"username"`
	SnowflakeID string `json:"snowflake_id"`
}

// LoadGameHistoryPlayers fetches the names for the games displayed on one history page.
func LoadGameHistoryPlayers(svc *sdk.Services, guildID string, entries []sdk.GameHistory, page int) (map[uint][]GameHistoryPlayer, error) {
	start := page * HistoryPerPage
	if start >= len(entries) {
		return map[uint][]GameHistoryPlayer{}, nil
	}
	end := start + HistoryPerPage
	if end > len(entries) {
		end = len(entries)
	}

	gameIDs := make([]string, 0, end-start)
	for _, entry := range entries[start:end] {
		if entry.Game == nil {
			continue
		}
		gameIDs = append(gameIDs, strconv.FormatUint(uint64(entry.Game.ID), 10))
	}
	if len(gameIDs) == 0 {
		return map[uint][]GameHistoryPlayer{}, nil
	}

	var participants map[string][]gameHistoryParticipantView
	path := fmt.Sprintf("/v1/guilds/%s/games/participants?ids=%s", url.PathEscape(guildID), strings.Join(gameIDs, ","))
	if err := svc.Client().API.Do(context.Background(), http.MethodGet, path, nil, &participants); err != nil {
		return nil, fmt.Errorf("load game history participants: %w", err)
	}

	playersByGame := make(map[uint][]GameHistoryPlayer, len(participants))
	for gameID, players := range participants {
		id, err := strconv.ParseUint(gameID, 10, strconv.IntSize)
		if err != nil {
			return nil, fmt.Errorf("parse game id %q from participants response: %w", gameID, err)
		}
		for _, player := range players {
			name := strings.TrimSpace(player.Username)
			if name == "" {
				name = strings.TrimSpace(player.SnowflakeID)
			}
			if name == "" {
				name = "Unknown player"
			}
			playersByGame[uint(id)] = append(playersByGame[uint(id)], GameHistoryPlayer{
				Username: name,
				IsWinner: player.IsWinner,
			})
		}
	}
	return playersByGame, nil
}

func buildGameHistoryDescriptions(entries []sdk.GameHistory, page int, playersByGame map[uint][]GameHistoryPlayer) []string {
	start := page * HistoryPerPage
	if start >= len(entries) {
		return nil
	}
	end := start + HistoryPerPage
	if end > len(entries) {
		end = len(entries)
	}

	descriptions := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		entry := entries[i]
		game := entry.Game
		if game == nil { // the game itself was removed; the participation record survives
			continue
		}
		participant := entry.GamePlayersModel
		players := playersByGame[game.ID]
		tied := game.Status == string(types.GameStatusCompleted) &&
			len(players) > 0 &&
			!slices.ContainsFunc(players, func(player GameHistoryPlayer) bool { return player.IsWinner })

		resultEmoji := DotRed
		switch {
		case game.Status == string(types.GameStatusVoided) || game.Status == string(types.GameStatusCancelled):
			resultEmoji = DotGray
		case tied:
			resultEmoji = DotYellow
		case participant.IsWinner:
			resultEmoji = DotGreen
		}

		eloSummary := fmt.Sprintf("`%d` → `%d`", participant.EloBefore, participant.EloAfter)
		switch {
		case participant.EloChange > 0:
			eloSummary += fmt.Sprintf(" · gained +%d", participant.EloChange)
		case participant.EloChange < 0:
			eloSummary += fmt.Sprintf(" · lost %d", -participant.EloChange)
		default:
			eloSummary += " · no change"
		}

		valueLines := []string{
			fmt.Sprintf("> **Recap:** `%d` Kills, `%d` Deaths, `%d` Beds", participant.Kills, participant.Deaths, participant.BedsDestroyed),
			fmt.Sprintf("> **ELO:** %s", eloSummary),
		}
		if len(players) > 0 {
			winners, losers := make([]string, 0, len(players)), make([]string, 0, len(players))
			for _, player := range players {
				name := strings.ReplaceAll(player.Username, "`", "'")
				if player.IsWinner {
					winners = append(winners, "`"+name+"`")
				} else if !tied {
					losers = append(losers, "`"+name+"`")
				}
			}
			if tied {
				tiedPlayers := make([]string, 0, len(players))
				for _, player := range players {
					tiedPlayers = append(tiedPlayers, "`"+strings.ReplaceAll(player.Username, "`", "'")+"`")
				}
				valueLines = append(valueLines, "> **Tied:** "+strings.Join(tiedPlayers, ", "))
			} else if len(winners) > 0 {
				valueLines = append(valueLines, "> **Winners:** "+strings.Join(winners, ", "))
			}
			if len(losers) > 0 {
				valueLines = append(valueLines, "> **Losers:** "+strings.Join(losers, ", "))
			}
		}

		descriptions = append(descriptions, fmt.Sprintf(
			"%s **Game #%d | %s | %s | %s**\n%s",
			resultEmoji, game.GameNumber, game.MapName, game.ModeName, DiscordRelativeTimestamp(gameWhen(*game)), strings.Join(valueLines, "\n"),
		))
	}
	return descriptions
}

// gameWhen resolves the effective timestamp for a game history entry.
func gameWhen(game models.GamesModel) time.Time {
	if game.StartedAt != nil {
		return *game.StartedAt
	}
	return game.CreatedAt
}

// ============================================================================
// Username & Rank Helpers
// ============================================================================

var mcUsernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_]{3,16}$`)

// IsValidMcUsername reports whether a Minecraft username matches the allowed format.
func IsValidMcUsername(username string) bool {
	return mcUsernamePattern.MatchString(username)
}

// LowestRankDefaults returns the entry rank name and starting ELO from a rank list.
func LowestRankDefaults(ranks []types.Rank) (string, int) {
	sorted := SortedRanks(ranks)
	if len(sorted) == 0 {
		return "", 1000
	}
	return sorted[0].RankName, sorted[0].MinElo
}

// ============================================================================
// Registration Role Resolution
// ============================================================================

// RegisteredRoleID returns the configured "guild_role_registered" role snowflake.
func RegisteredRoleID(guildID string) string {
	roles, err := GuildStateRoles(guildID)
	if err != nil {
		return ""
	}
	role, ok := roles["guild_role_registered"]
	if !ok {
		return ""
	}
	return strings.TrimSpace(role.ID)
}

// ============================================================================
// Moderation Target Resolution
// ============================================================================

// placeholderUsername derives a valid Minecraft-shaped username from a Discord
// user, for registering someone who has never linked an account (see
// EnsureModerationTarget). Staff can correct it later with /forcerename.
func placeholderUsername(user *discordgo.User) string {
	var b strings.Builder
	for _, r := range user.Username {
		if b.Len() == 16 {
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	name := b.String()
	if len(name) < 3 {
		if len(user.ID) >= 6 {
			name = "Player" + user.ID[len(user.ID)-6:]
		} else {
			name = "Player" + user.ID
		}
	}
	return name
}

// EnsureModerationTarget resolves a Discord user to a player id (and their
// current config, so callers building a result embed don't need a second
// fetch for the username) in the guild, for moderation actions (strikes,
// punishments, decays) that must work even against someone who never linked a
// Minecraft account: a target who isn't registered yet is registered with a
// placeholder username first. actorID is the staff member performing the
// action that triggered this.
func EnsureModerationTarget(actorID, guildID string, user *discordgo.User) (uint, *models.PlayerConfigsModel, error) {
	view, err := database.Must().Player.Get(guildID, user.ID)
	if err == nil {
		return view.Player.ID, view.Config, nil
	}
	if !sdk.IsNotFound(err) {
		return 0, nil, err
	}
	username := placeholderUsername(user)
	reg, err := database.Must().Player.Register(actorID, guildID, user.ID, username, 0)
	if err != nil {
		return 0, nil, err
	}
	return reg.PlayerID, &models.PlayerConfigsModel{Username: username}, nil
}

// HasConfiguredRank reports whether the guild has at least one rank, which a
// new registration needs to receive a rank (and its role).
func HasConfiguredRank(guildID string) (bool, error) {
	ranks, err := GuildMetaRanks(guildID)
	if err != nil {
		return false, err
	}
	for _, r := range ranks {
		if strings.TrimSpace(r.RankName) != "" {
			return true, nil
		}
	}
	return false, nil
}

// ApplyStartingRank sets a freshly registered player's stats to the guild's
// configured entry rank and ELO (Register itself only sets the schema default,
// 1000). Only call this for a genuinely new registration (Registration.CreatedGuild) —
// a restored one already carries its prior stats and must be left alone.
func ApplyStartingRank(guildID, actorID string, playerID uint) error {
	ranks, err := GuildMetaRanks(guildID)
	if err != nil {
		return err
	}
	rankName, startingElo := LowestRankDefaults(ranks)
	if rankName == "" {
		return nil // no ranks configured yet
	}
	pid := sdk.UintToString(playerID)
	if startingElo > 0 {
		// The entry rating is the base of the game chain, not a staff modification on top of it.
		if _, err = database.Must().Stats.ApplyElo(actorID, guildID, pid, 0, sdk.EloEffect{Part: "game", Delta: startingElo}); err != nil {
			return err
		}
	}
	_, err = database.Must().Stats.Patch(actorID, guildID, pid, 0, map[string]any{"rank": rankName})
	return err
}

// ============================================================================
// Nickname & Profile Sync
// ============================================================================

// SetMemberNickname sets a member's guild nickname; an empty string resets it.
func SetMemberNickname(s *discordgo.Session, guildID, userID, nickname string) error {
	return s.GuildMemberNickname(guildID, userID, nickname)
}

// FormatPlayerDisplayName builds the registered Discord display nickname.
func FormatPlayerDisplayName(elo int, username string, nickname *string, showElo bool) string {
	prefix := ""
	if showElo {
		prefix = fmt.Sprintf("[%d] ", elo)
	}
	nick := ""
	if nickname != nil {
		nick = strings.TrimSpace(*nickname)
	}
	if nick != "" {
		return prefix + username + " | " + nick
	}
	return prefix + username
}

// SyncRegisteredPlayerProfile applies registered, rank, and nickname sync for a player.
func SyncRegisteredPlayerProfile(
	s *discordgo.Session,
	guildID string,
	playerID uint,
	discordUserID string,
) ([]string, error) {
	pid := sdk.UintToString(playerID)
	config, err := database.Must().Player.GetConfig(guildID, pid)
	if err != nil || config == nil {
		if sdk.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	member, err := FetchGuildMember(s, guildID, discordUserID)
	if err != nil || member == nil {
		return nil, err
	}

	_, seasonID, err := ActiveSeasonKey(guildID)
	if err != nil && !sdk.IsNotFound(err) {
		return nil, err // no active season is fine: elo stays 0
	}

	elo := 0
	if seasonID != 0 {
		row, statsErr := database.Must().Stats.Get(guildID, pid, seasonID)
		if statsErr == nil && row != nil {
			elo = row.Elo
		}
	}

	ranks, err := GuildMetaRanks(guildID)
	if err != nil {
		return nil, err
	}
	currentRank := RankForElo(ranks, elo)

	var actions []string

	if roleID := RegisteredRoleID(guildID); roleID != "" && !MemberHasRole(member, roleID) {
		if err := AddMemberRole(s, guildID, discordUserID, roleID); err != nil {
			return actions, fmt.Errorf("assign registered role: %w", err)
		}
		actions = append(actions, "Assigned standard server verified `Registered` profile identity card.")
		member, _ = FetchGuildMember(s, guildID, discordUserID)
	}

	if member != nil {
		for _, rank := range SortedRanks(ranks) {
			roleID := strings.TrimSpace(rank.RoleID)
			if roleID == "" {
				continue
			}
			shouldHave := currentRank != nil && rank.RankName == currentRank.RankName
			hasRole := MemberHasRole(member, roleID)
			if shouldHave && !hasRole {
				_ = AddMemberRole(s, guildID, discordUserID, roleID)
				actions = append(actions, fmt.Sprintf("Synchronized rank role to **%s**.", rank.RankName))
			} else if !shouldHave && hasRole {
				_ = RemoveMemberRole(s, guildID, discordUserID, roleID)
			}
		}
	}

	expectedNick := FormatPlayerDisplayName(elo, config.Username, config.Nickname, config.IsEloVisible)
	if member != nil && member.Nick != expectedNick {
		if err := SetMemberNickname(s, guildID, discordUserID, expectedNick); err == nil {
			actions = append(actions, fmt.Sprintf("Updated server nickname to **%s**.", expectedNick))
		}
	}

	return actions, nil
}

// ResetMemberRegistrationProfile removes registration and rank roles and clears the nickname.
func ResetMemberRegistrationProfile(s *discordgo.Session, guildID string, member *discordgo.Member) error {
	if member == nil || member.User == nil {
		return nil
	}
	userID := member.User.ID

	if roleID := RegisteredRoleID(guildID); roleID != "" && MemberHasRole(member, roleID) {
		_ = RemoveMemberRole(s, guildID, userID, roleID)
	}

	ranks, err := GuildMetaRanks(guildID)
	if err == nil {
		for _, rank := range ranks {
			if roleID := strings.TrimSpace(rank.RoleID); roleID != "" {
				_ = RemoveMemberRole(s, guildID, userID, roleID)
			}
		}
	}

	_ = SetMemberNickname(s, guildID, userID, "")
	return nil
}

// GetRankName returns the rank name or "Unranked" if nil/empty.
func GetRankName(rank *types.Rank) string {
	if rank == nil || rank.RankName == "" {
		return "Unranked"
	}
	return rank.RankName
}

// AbsInt returns the absolute value of an integer.
func AbsInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// ============================================================================
// Registration Verification Settings
// ============================================================================

// LoadRegistrationVerification returns the guild's registration verification settings.
// A missing configuration resolves to an empty (disabled) mode.
func LoadRegistrationVerification(guildID string) (*types.RegistrationVerification, error) {
	verification := &types.RegistrationVerification{}
	cfg, err := database.Must().GuildConfig.Get(guildID)
	if sdk.IsNotFound(err) {
		return verification, nil
	}
	if err != nil {
		return nil, err
	}
	if cfg != nil && !cfg.RegistrationVerification.IsEmpty() {
		if err := cfg.RegistrationVerification.Unmarshal(verification); err != nil {
			return nil, err
		}
	}
	return verification, nil
}

// RequiresRegistrationAPIVerification reports whether external username verification is required.
func RequiresRegistrationAPIVerification(cfg *types.RegistrationVerification) bool {
	if cfg == nil {
		return false
	}
	return cfg.Mode == "network_api" || cfg.Mode == "custom_api"
}

// VerifyMinecraftUsernameExists checks username existence via Mojang or custom API when API verification is enabled.
func VerifyMinecraftUsernameExists(username string) (bool, error) {
	return VerifyMinecraftUsernameExistsWithConfig(username, nil)
}

// VerifyMinecraftUsernameExistsWithConfig checks username existence using the provided verification config.
// If cfg is nil or mode is network_api, it uses Mojang API. If mode is custom_api, it uses the configured external API.
func VerifyMinecraftUsernameExistsWithConfig(username string, cfg *types.RegistrationVerification) (bool, error) {
	// Default to Mojang API if no config or network_api mode
	if cfg == nil || cfg.Mode == "network_api" {
		return verifyViaMojang(username)
	}

	// Use custom API if configured
	if cfg.Mode == "custom_api" && cfg.ExternalAPI != nil {
		return verifyViaCustomAPI(username, cfg.ExternalAPI)
	}

	// Fallback to Mojang for unknown modes
	return verifyViaMojang(username)
}

// verifyViaMojang checks username existence via Mojang API.
func verifyViaMojang(username string) (bool, error) {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	endpoint := "https://api.mojang.com/users/profiles/minecraft/" + url.PathEscape(username)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return false, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("minecraft profile lookup failed with status %d", resp.StatusCode)
	}
}

// verifyViaCustomAPI looks the player up in the configured external API. The lookup succeeds only when the
// response is HTTP 200 JSON and the configured response field equals the submitted username (case-insensitive),
// matching the dashboard's test and /api/v1/verify/external. Legacy configs with only uuid_field_path accept
// any non-empty value there.
func verifyViaCustomAPI(username string, api *types.ExternalAPI) (bool, error) {
	if api.EndpointURL == "" || (api.QueryParam == "" && !strings.Contains(api.EndpointURL, "{")) || (api.ResponseFieldPath == "" && api.UUIDFieldPath == "") {
		return false, fmt.Errorf("external API configuration is incomplete")
	}
	token := strings.TrimSpace(api.APIKey)
	if api.AuthType != "none" && token == "" {
		return false, fmt.Errorf("external API token is missing")
	}

	// {username} / {uuid} in the URL put the player in the path; otherwise the lookup goes in a query parameter.
	inPath := strings.Contains(api.EndpointURL, "{username}") || strings.Contains(api.EndpointURL, "{uuid}")
	needsUUID := api.QueryParamType == "uuid" || strings.Contains(api.EndpointURL, "{uuid}")
	uuid := ""
	if needsUUID {
		var err error
		if uuid, err = mojangUUID(username); err != nil {
			return false, err
		}
		if uuid == "" {
			return false, nil
		}
	}
	rawURL := strings.NewReplacer("{username}", url.PathEscape(username), "{uuid}", url.PathEscape(uuid)).Replace(api.EndpointURL)
	endpoint, err := url.Parse(rawURL)
	if err != nil {
		return false, fmt.Errorf("invalid endpoint URL: %w", err)
	}
	if endpoint.Scheme != "https" || endpoint.User != nil {
		return false, fmt.Errorf("external API URL must use HTTPS without embedded credentials")
	}

	query := endpoint.Query()
	if !inPath {
		lookup := username
		if api.QueryParamType == "uuid" {
			lookup = uuid
		}
		query.Set(api.QueryParam, lookup)
	}
	headers := http.Header{"Accept": {"application/json"}}
	switch api.AuthType {
	case "none":
	case "bearer":
		headers.Set("Authorization", "Bearer "+token)
	case "header":
		name := strings.TrimSpace(api.HeaderName)
		if name == "" {
			name = "API-Key"
		}
		headers.Set(name, token)
	case "query":
		name := strings.TrimSpace(api.TokenQueryParam)
		if name == "" {
			name = "api_key"
		}
		query.Set(name, token)
	default:
		return false, fmt.Errorf("unsupported external API auth type %q", api.AuthType)
	}
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return false, err
	}
	req.Header = headers

	httpClient := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("external API returned status %d", resp.StatusCode)
	}

	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return false, fmt.Errorf("failed to parse API response: %w", err)
	}

	if api.ResponseFieldPath != "" {
		value, ok := extractNestedValue(data, api.ResponseFieldPath).(string)
		return ok && strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(username)), nil
	}
	value := extractNestedValue(data, api.UUIDFieldPath)
	return value != nil && fmt.Sprint(value) != "", nil
}

// mojangUUID resolves a Minecraft username to its UUID ("" when the account does not exist).
func mojangUUID(username string) (string, error) {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get("https://api.mojang.com/users/profiles/minecraft/" + url.PathEscape(username))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var profile struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&profile); err != nil {
			return "", err
		}
		return profile.ID, nil
	case http.StatusNotFound:
		return "", nil
	default:
		return "", fmt.Errorf("minecraft profile lookup failed with status %d", resp.StatusCode)
	}
}

// extractNestedValue follows a dot path through objects and arrays (numeric segments index arrays); nil if absent.
func extractNestedValue(data interface{}, path string) interface{} {
	current := data
	for _, key := range strings.Split(path, ".") {
		if key == "" {
			continue
		}
		switch node := current.(type) {
		case map[string]interface{}:
			next, ok := node[key]
			if !ok {
				return nil
			}
			current = next
		case []interface{}:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(node) {
				return nil
			}
			current = node[index]
		default:
			return nil
		}
	}
	return current
}

// RegistrationNetworkLabel returns user-facing copy for the configured verification mode.
func RegistrationNetworkLabel(cfg *types.RegistrationVerification) string {
	if cfg == nil || cfg.Mode == "" || cfg.Mode == "manual" || cfg.Mode == "disabled" {
		return "this server"
	}
	if cfg.Mode == "custom_api" {
		return "the configured profile API"
	}
	return "Minecraft"
}

// AdjustElo records a staff modification of delta on top of the player's game results (the database clamps the
// rating at 0 and raises highest_elo) and returns the updated row.
func AdjustElo(actor, guildID, pid string, seasonID uint, _ *models.PlayerStatsModel, delta int) (*models.PlayerStatsModel, error) {
	return database.Must().Stats.ApplyElo(actor, guildID, pid, seasonID, sdk.EloEffect{Part: "adjustment", Delta: delta, Category: "modify"})
}

// SyncRankForElo recomputes the rank matching elo from the guild's configured
// rank ladder and persists it if it changed, returning the (possibly
// unchanged) rank name. A guild with no ranks configured leaves rank alone.
func SyncRankForElo(actor, guildID, pid string, seasonID uint, currentRank string, elo int) (string, error) {
	ranks, err := GuildMetaRanks(guildID)
	if err != nil {
		return currentRank, err
	}
	rank := RankForElo(ranks, elo)
	if rank == nil || rank.RankName == "" || rank.RankName == currentRank {
		return currentRank, nil
	}
	if _, err := database.Must().Stats.Patch(actor, guildID, pid, seasonID, map[string]any{"rank": rank.RankName}); err != nil {
		return currentRank, err
	}
	return rank.RankName, nil
}

// NewPerkResolver builds the lookup the EloManager uses to find a player's
// active perks (see EloManager.SetPerkResolver): the guild's enabled perks from
// the game config, filtered by the member's roles. session returns the live
// Discord session (nil = nobody has perks).
//
// A member boosting the server gets one booster
// stack, and a perk whose allowed roles include one of their managed
// (subscriber) roles counts as a booster perk.
func NewPerkResolver(session func() *discordgo.Session) func(guildID, userID string) PerkContext {
	cache := newTTLCache[[]Perk](eloConfigCacheTTL)
	return func(guildID, userID string) PerkContext {
		s := session()
		if s == nil {
			return PerkContext{Err: errors.New("no Discord session to read roles from")}
		}
		perks, err := cache.get(guildID, func() ([]Perk, error) { return GuildPerks(guildID) })
		if err != nil {
			return PerkContext{Err: err}
		}
		if len(perks) == 0 {
			return PerkContext{}
		}
		member, err := s.State.Member(guildID, userID)
		if err != nil {
			if member, err = s.GuildMember(guildID, userID); err != nil {
				if isUnknownMember(err) {
					return PerkContext{} // left the server: no roles, so no perks
				}
				return PerkContext{Err: err} // a lookup failure is not "no perks": scoring retries instead of paying nothing
			}
		}

		held := make(map[string]bool, len(member.Roles))
		for _, id := range member.Roles {
			held[id] = true
		}
		var boosterRoles []string
		stacks := 0
		if member.PremiumSince != nil {
			stacks = 1
			if g, err := s.State.Guild(guildID); err == nil {
				for _, r := range g.Roles {
					if r.Managed && held[r.ID] {
						boosterRoles = append(boosterRoles, r.ID)
					}
				}
			}
		}

		ctx := PerkContext{BoosterStacks: stacks}
		for _, p := range MarkBoosters(perks, boosterRoles...) {
			if p.QualifiesFor(held, nil) {
				ctx.Perks = append(ctx.Perks, p)
			}
		}
		return ctx
	}
}

// ============================================================================
// Party-code registration
// ============================================================================

// With the guild's registration_verification.mode set to "party_code", /register no longer trusts the
// username it is given. The engine's bot invites that Minecraft account to a party and says a one-time code
// in party chat, where only the account's owner can read it; the user then types the code into a Discord
// modal. This file holds the Discord side: the code, who it belongs to and how often it may be requested.
// The pending codes live in memory: a restart just means running /register again.

const (
	// RegistrationModePartyCode is registration_verification.mode for this flow.
	RegistrationModePartyCode = "party_code"

	// Custom IDs of the "enter code" button and its modal.
	RegVerifyOpenID    = "regverify|open"
	RegVerifyModalID   = "regverify|submit"
	RegVerifyCodeField = "code"

	regVerifyTTL         = 10 * time.Minute
	regVerifyMaxAttempts = 5
	regVerifyUserMax     = 3                // requests a user may make...
	regVerifyUserWindow  = 10 * time.Minute // ...in this long
	regVerifyNameWait    = 2 * time.Minute  // the same Minecraft name is never invited more often than this
)

// regVerifyAlphanumeric leaves out look-alikes (0/O, 1/I) so a code read from party chat is hard to mistype.
const regVerifyAlphanumeric = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

// regVerifyCodeSettings resolves the in-game code settings (4-12 characters, 1-60 minutes) to the alphabet,
// length and lifetime to use; nil falls back to a six-digit, ten-minute code.
func regVerifyCodeSettings(cfg *types.InGameCode) (length int, alphabet string, ttl time.Duration) {
	length, alphabet, ttl = 6, "0123456789", regVerifyTTL
	if cfg == nil {
		return
	}
	if cfg.CodeLength >= 4 && cfg.CodeLength <= 12 {
		length = cfg.CodeLength
	}
	if cfg.CodeFormat == "alphanumeric" {
		alphabet = regVerifyAlphanumeric
	}
	if cfg.ExpiresMinutes >= 1 && cfg.ExpiresMinutes <= 60 {
		ttl = time.Duration(cfg.ExpiresMinutes) * time.Minute
	}
	return
}

// RequiresPartyCodeVerification reports whether /register has to prove ownership with a party code.
func RequiresPartyCodeVerification(cfg *types.RegistrationVerification) bool {
	return cfg != nil && cfg.Mode == RegistrationModePartyCode
}

// RateLimitError says when the next code may be requested.
type RateLimitError struct{ Wait time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("too many verification requests, try again in %s", e.Wait.Round(time.Second))
}

// CodeResult is defined in package types (players.go).
type CodeResult = types.CodeResult

const (
	CodeOK      CodeResult = iota
	CodeNone               // nothing pending, or it expired
	CodeWrong              // wrong code; attempts remain
	CodeLocked             // too many wrong codes: the request is void
	CodeInvalid            // not six digits
)

type pendingRegistration struct {
	username string
	code     string
	expires  time.Time
	attempts int
}

// RegistrationVerifierStore keeps the pending codes and the request limits.
type RegistrationVerifierStore struct {
	mu       sync.Mutex
	now      func() time.Time
	pending  map[string]*pendingRegistration
	userHits map[string][]time.Time
	nameLast map[string]time.Time
}

// RegistrationVerifier is the process-wide store.
var RegistrationVerifier = &RegistrationVerifierStore{
	now:      time.Now,
	pending:  map[string]*pendingRegistration{},
	userHits: map[string][]time.Time{},
	nameLast: map[string]time.Time{},
}

func regKey(guildID, userID string) string { return guildID + "|" + userID }

// Begin counts a request and, if it is allowed, makes a fresh six-digit code for the user and name
// (replacing any earlier one). The limits keep /register from being used to spam someone else's account
// with party invites: a few requests per user, and one per Minecraft name every couple of minutes.
func (v *RegistrationVerifierStore) Begin(guildID, userID, username string, cfg *types.InGameCode) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	v.sweep(now)

	nameKey := guildID + "|" + strings.ToLower(username)
	if last, ok := v.nameLast[nameKey]; ok && now.Sub(last) < regVerifyNameWait {
		return "", &RateLimitError{Wait: regVerifyNameWait - now.Sub(last)}
	}
	hits := v.userHits[regKey(guildID, userID)]
	if len(hits) >= regVerifyUserMax {
		return "", &RateLimitError{Wait: regVerifyUserWindow - now.Sub(hits[0])}
	}

	length, alphabet, ttl := regVerifyCodeSettings(cfg)
	code := make([]byte, length)
	for i := range code {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		code[i] = alphabet[n.Int64()]
	}

	v.nameLast[nameKey] = now
	v.userHits[regKey(guildID, userID)] = append(hits, now)
	v.pending[regKey(guildID, userID)] = &pendingRegistration{username: username, code: string(code), expires: now.Add(ttl)}
	return string(code), nil
}

// Discard forgets the user's pending code (the code never reached them).
func (v *RegistrationVerifierStore) Discard(guildID, userID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.pending, regKey(guildID, userID))
}

// Pending reports whether the user has a live code.
func (v *RegistrationVerifierStore) Pending(guildID, userID string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	p := v.pending[regKey(guildID, userID)]
	return p != nil && v.now().Before(p.expires)
}

// Check compares the typed code with the user's pending one. On CodeOK it returns the Minecraft name that
// was verified and the code is spent; after too many wrong tries the request is void.
func (v *RegistrationVerifierStore) Check(guildID, userID, input string) (username string, result CodeResult, attemptsLeft int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	key := regKey(guildID, userID)
	p := v.pending[key]
	if p == nil || !v.now().Before(p.expires) {
		delete(v.pending, key)
		return "", CodeNone, 0
	}
	input = strings.ToUpper(strings.TrimSpace(input))
	if len(input) != len(p.code) || strings.Trim(input, regVerifyAlphanumeric+"01") != "" {
		return "", CodeInvalid, regVerifyMaxAttempts - p.attempts
	}
	if subtle.ConstantTimeCompare([]byte(input), []byte(p.code)) == 1 {
		delete(v.pending, key)
		return p.username, CodeOK, 0
	}
	p.attempts++
	if p.attempts >= regVerifyMaxAttempts {
		delete(v.pending, key)
		return "", CodeLocked, 0
	}
	return "", CodeWrong, regVerifyMaxAttempts - p.attempts
}

// sweep drops expired codes and old limit entries; callers hold the lock.
func (v *RegistrationVerifierStore) sweep(now time.Time) {
	for k, p := range v.pending {
		if !now.Before(p.expires) {
			delete(v.pending, k)
		}
	}
	for k, hits := range v.userHits {
		keep := hits[:0]
		for _, at := range hits {
			if now.Sub(at) < regVerifyUserWindow {
				keep = append(keep, at)
			}
		}
		if len(keep) == 0 {
			delete(v.userHits, k)
		} else {
			v.userHits[k] = keep
		}
	}
	for k, at := range v.nameLast {
		if now.Sub(at) >= regVerifyNameWait {
			delete(v.nameLast, k)
		}
	}
}

// isUnknownMember reports whether Discord answered that the member (or user) does not exist.
func isUnknownMember(err error) bool {
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Message != nil {
		return rest.Message.Code == discordgo.ErrCodeUnknownMember || rest.Message.Code == discordgo.ErrCodeUnknownUser
	}
	return false
}
