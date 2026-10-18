package helpers

import (
	"bytes"
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

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"
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
		_, _ = s.ChannelMessageSendEmbed(dm.ID, queueDeniedEmbed(msg))
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

// UpdateGuildMemberBio updates the bot's bio for a specific guild via REST API
func (api *DiscordRestAPI) UpdateGuildMemberBio(guildID, bio string) error {
	endpoint := fmt.Sprintf("%s/guilds/%s/members/@me", api.baseURL, guildID)

	var payload GuildMember
	if bio == "" {
		// Send null to remove bio
		payload.Bio = nil
	} else {
		// Safely trim bio to Discord's 190 character limit using runes (UTF-8 safe)
		runes := []rune(bio)
		if len(runes) > 190 {
			bio = string(runes[:190])
		}
		payload.Bio = &bio
	}

	return api.makeRequest("PATCH", endpoint, payload)
}

// UpdateGuildMemberAvatar updates the bot's avatar for a specific guild via REST API
func (api *DiscordRestAPI) UpdateGuildMemberAvatar(guildID, imageURL string) error {
	endpoint := fmt.Sprintf("%s/guilds/%s/members/@me", api.baseURL, guildID)

	var payload GuildMember
	if imageURL == "" {
		// Send null to remove avatar
		payload.Avatar = nil
	} else {
		// Fetch and convert image to base64
		imageData, err := api.fetchImageAsBase64(imageURL)
		if err != nil {
			return fmt.Errorf("failed to fetch avatar image: %w", err)
		}
		payload.Avatar = &imageData
	}

	return api.makeRequest("PATCH", endpoint, payload)
}

// UpdateGuildMemberBanner updates the bot's banner for a specific guild via REST API
func (api *DiscordRestAPI) UpdateGuildMemberBanner(guildID, imageURL string) error {
	endpoint := fmt.Sprintf("%s/guilds/%s/members/@me", api.baseURL, guildID)

	var payload GuildMember
	if imageURL == "" {
		// Send null to remove banner
		payload.Banner = nil
	} else {
		// Fetch and convert image to base64
		imageData, err := api.fetchImageAsBase64(imageURL)
		if err != nil {
			return fmt.Errorf("failed to fetch banner image: %w", err)
		}
		payload.Banner = &imageData
	}

	return api.makeRequest("PATCH", endpoint, payload)
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

// BuildStrikeHistoryEmbed renders the strike history embed for one page.
func BuildStrikeHistoryEmbed(username string, entries []models.PlayerStrikesModel, page int) *discordgo.MessageEmbed {
	totalPages := HistoryTotalPages(len(entries))
	description := fmt.Sprintf("This player has a total of **%d** strike(s) on record.", len(entries))
	if len(entries) == 0 {
		description = "No strike history was found for this player."
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's Strike History • Page %d of %d", username, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
		Fields:      BuildStrikeHistoryFieldsDirect(entries, page),
		FooterText:  HistoryPageFooter(page, totalPages, len(entries)),
	})
}

// BuildDecayHistoryEmbed renders the ELO decay history embed for one page.
func BuildDecayHistoryEmbed(username string, entries []models.PlayerDecaysModel, page int) *discordgo.MessageEmbed {
	totalPages := HistoryTotalPages(len(entries))
	totalDecayed, voided := 0, 0
	for _, entry := range entries {
		if entry.IsVoided {
			voided++
			continue
		}
		totalDecayed += entry.AmountDeducted
	}
	description := fmt.Sprintf("This player has lost **%d** ELO across **%d** decay(s) this season.", totalDecayed, len(entries)-voided)
	if voided > 0 {
		description += fmt.Sprintf(" **%d** voided decay(s) are not counted.", voided)
	}
	if len(entries) == 0 {
		description = "No decay history was found for this player."
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's ELO Decay History • Page %d of %d", username, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
		Fields:      BuildDecayHistoryFieldsDirect(entries, page),
		FooterText:  HistoryPageFooter(page, totalPages, len(entries)),
	})
}

// BuildPunishmentHistoryEmbed renders the punishment history embed for one page.
func BuildPunishmentHistoryEmbed(username string, entries []models.PlayerPunishmentsModel, page int, activeOnly bool) *discordgo.MessageEmbed {
	totalPages := HistoryTotalPages(len(entries))
	description := fmt.Sprintf("This player has a total of **%d** punishment(s) on record.", len(entries))
	if len(entries) == 0 {
		description = "No punishment history was found for this player."
	}
	titlePrefix := "Punishment History"
	if activeOnly {
		titlePrefix = "Active Punishments"
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's %s • Page %d of %d", username, titlePrefix, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
		Fields:      BuildPunishmentHistoryFieldsDirect(entries, page),
		FooterText:  HistoryPageFooter(page, totalPages, len(entries)),
	})
}

// BuildGameHistoryEmbed renders the game history embed for one page.
func BuildGameHistoryEmbed(username string, entries []sdk.GameHistory, page int) *discordgo.MessageEmbed {
	totalPages := HistoryTotalPages(len(entries))
	description := fmt.Sprintf("This player has a total of **%d** game(s) on record for the current season.", len(entries))
	if len(entries) == 0 {
		description = "No game history was found for this player."
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's Game History • Page %d of %d", username, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
		Fields:      BuildGameHistoryFieldsDirect(entries, page),
		FooterText:  HistoryPageFooter(page, totalPages, len(entries)),
	})
}

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

// BuildStrikeHistoryFieldsDirect builds embed fields for one page of strike history (direct type)
func BuildStrikeHistoryFieldsDirect(entries []models.PlayerStrikesModel, page int) []*discordgo.MessageEmbedField {
	start := page * HistoryPerPage
	if start >= len(entries) {
		return nil
	}
	end := start + HistoryPerPage
	if end > len(entries) {
		end = len(entries)
	}

	fields := make([]*discordgo.MessageEmbedField, 0, end-start)
	for i := start; i < end; i++ {
		strike := entries[i]
		emoji := EmojiYellowDot
		if strike.IsVoided {
			emoji = EmojiRedDot
		}

		valueLines := []string{
			fmt.Sprintf("> **ID:** `#%d`", strike.ID),
			fmt.Sprintf("> **Staff:** %s", formatStaffDisplay(strike.StaffID)),
			fmt.Sprintf("> **Reason:** %s", strike.Reason),
			fmt.Sprintf("> **Issued:** %s", DiscordRelativeTimestamp(strike.CreatedAt)),
		}

		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("%s Strike #%d", emoji, strike.ID),
			Value:  strings.Join(valueLines, "\n"),
			Inline: false,
		})
	}
	return fields
}

// BuildDecayHistoryFieldsDirect builds embed fields for one page of ELO decay history
func BuildDecayHistoryFieldsDirect(entries []models.PlayerDecaysModel, page int) []*discordgo.MessageEmbedField {
	start := page * HistoryPerPage
	if start >= len(entries) {
		return nil
	}
	end := start + HistoryPerPage
	if end > len(entries) {
		end = len(entries)
	}

	fields := make([]*discordgo.MessageEmbedField, 0, end-start)
	for i := start; i < end; i++ {
		decay := entries[i]
		emoji := EmojiYellowDot
		if decay.IsVoided {
			emoji = EmojiRedDot
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

		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("%s Decay #%d", emoji, decay.ID),
			Value:  strings.Join(valueLines, "\n"),
			Inline: false,
		})
	}
	return fields
}

// BuildPunishmentHistoryFieldsDirect builds embed fields for one page of punishment history (direct type)
func BuildPunishmentHistoryFieldsDirect(entries []models.PlayerPunishmentsModel, page int) []*discordgo.MessageEmbedField {
	start := page * HistoryPerPage
	if start >= len(entries) {
		return nil
	}
	end := start + HistoryPerPage
	if end > len(entries) {
		end = len(entries)
	}

	fields := make([]*discordgo.MessageEmbedField, 0, end-start)
	for i := start; i < end; i++ {
		punishment := entries[i]
		emoji := EmojiYellowDot
		if !punishment.IsEnabled || punishment.IsVoided {
			emoji = EmojiRedDot
		}

		valueLines := []string{
			fmt.Sprintf("> **ID:** `#%d`", punishment.ID),
			fmt.Sprintf("> **Type:** `%s`", punishment.Type),
			fmt.Sprintf("> **Staff:** %s", formatStaffDisplay(punishment.StaffID)),
			fmt.Sprintf("> **Reason:** %s", punishment.Reason),
			fmt.Sprintf("> **Issued:** %s", DiscordRelativeTimestamp(punishment.CreatedAt)),
		}

		if punishment.ExpiresAt != nil {
			if punishment.ExpiresAt.Before(time.Now()) {
				valueLines = append(valueLines, fmt.Sprintf("> **Expired:** %s", DiscordRelativeTimestamp(*punishment.ExpiresAt)))
			} else {
				valueLines = append(valueLines, fmt.Sprintf("> **Expires:** %s", DiscordRelativeTimestamp(*punishment.ExpiresAt)))
			}
		}

		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("%s %s #%d", emoji, strings.Title(punishment.Type), punishment.ID),
			Value:  strings.Join(valueLines, "\n"),
			Inline: false,
		})
	}
	return fields
}

// BuildGameHistoryFieldsDirect builds embed fields for one page of game history.
func BuildGameHistoryFieldsDirect(entries []sdk.GameHistory, page int) []*discordgo.MessageEmbedField {
	start := page * HistoryPerPage
	if start >= len(entries) {
		return nil
	}
	end := start + HistoryPerPage
	if end > len(entries) {
		end = len(entries)
	}

	fields := make([]*discordgo.MessageEmbedField, 0, end-start)
	for i := start; i < end; i++ {
		entry := entries[i]
		game := entry.Game
		if game == nil { // the game itself was removed; the participation record survives
			continue
		}
		participant := entry.GamePlayersModel

		emoji := EmojiYellowDot
		if game.Status == string(types.GameStatusVoided) || game.Status == string(types.GameStatusCancelled) {
			emoji = EmojiRedDot
		}

		valueLines := []string{
			fmt.Sprintf("> **Map:** `%s` • **Mode:** `%s`", game.MapName, game.ModeName),
			fmt.Sprintf("> **Team:** `%s` • **Result:** %s", participant.Team, formatGameResult(game.Status, participant.IsWinner)),
			fmt.Sprintf("> **ELO:** `%s`", formatEloChange(participant.EloChange)),
			fmt.Sprintf("> **K/D:** `%d/%d` • **Finals:** `%d` • **Beds:** `%d/%d`", participant.Kills, participant.Deaths, participant.FinalKills, participant.BedsDestroyed, participant.BedsLost),
			fmt.Sprintf("> **Date:** %s", DiscordRelativeTimestamp(gameWhen(*game))),
		}

		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("%s Game #%d", emoji, game.GameNumber),
			Value:  strings.Join(valueLines, "\n"),
			Inline: false,
		})
	}
	return fields
}

// formatGameResult renders the match outcome label for a game history entry.
func formatGameResult(status string, isWinner bool) string {
	switch status {
	case string(types.GameStatusCompleted):
		if isWinner {
			return "Victory"
		}
		return "Defeat"
	case string(types.GameStatusVoided):
		return "Voided"
	case string(types.GameStatusCancelled):
		return "Cancelled"
	case string(types.GameStatusInProgress):
		return "In Progress"
	default:
		return "Waiting"
	}
}

// formatEloChange renders a signed ELO delta for display.
func formatEloChange(change int) string {
	if change > 0 {
		return fmt.Sprintf("+%d", change)
	}
	if change < 0 {
		return fmt.Sprintf("%d", change)
	}
	return "±0"
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
	_, err = database.Must().Stats.Patch(actorID, guildID, sdk.UintToString(playerID), 0, map[string]any{
		"rank": rankName, "elo": startingElo, "highest_elo": startingElo,
	})
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

// verifyViaCustomAPI checks username existence via a configured external API.
func verifyViaCustomAPI(username string, api *types.ExternalAPI) (bool, error) {
	if api.EndpointURL == "" || api.APIKey == "" {
		return false, fmt.Errorf("external API configuration is incomplete")
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}

	// Build request with authentication
	headers := make(map[string]string)
	if api.AuthType == "bearer" {
		headers["Authorization"] = "Bearer " + api.APIKey
	} else {
		headers[api.HeaderName] = api.APIKey
	}

	// Build URL with query parameter
	url, err := url.Parse(api.EndpointURL)
	if err != nil {
		return false, fmt.Errorf("invalid endpoint URL: %w", err)
	}

	query := url.Query()
	query.Set(api.QueryParam, username)
	url.RawQuery = query.Encode()

	req, err := http.NewRequest(http.MethodGet, url.String(), nil)
	if err != nil {
		return false, err
	}

	// Set headers
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("external API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse JSON response
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return false, fmt.Errorf("failed to parse API response: %w", err)
	}

	// Extract UUID using the configured field path
	uuid, err := extractNestedValue(data, api.UUIDFieldPath)
	if err != nil {
		return false, fmt.Errorf("could not extract UUID from response: %w", err)
	}

	return uuid != "", nil
}

// extractNestedValue extracts a value from a nested map using a dot-notation path.
func extractNestedValue(data map[string]interface{}, path string) (string, error) {
	keys := strings.Split(path, ".")
	var current interface{} = data

	for _, key := range keys {
		if m, ok := current.(map[string]interface{}); ok {
			if val, exists := m[key]; exists {
				current = val
			} else {
				return "", fmt.Errorf("key '%s' not found in path", key)
			}
		} else {
			return "", fmt.Errorf("expected map at key '%s', got %T", key, current)
		}
	}

	if current == nil {
		return "", nil
	}

	if str, ok := current.(string); ok {
		return str, nil
	}

	return fmt.Sprintf("%v", current), nil
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

// AdjustElo applies delta to current's elo (clamped at 0), raising highest_elo
// to match when it's now higher, and returns the updated row.
func AdjustElo(actor, guildID, pid string, seasonID uint, current *models.PlayerStatsModel, delta int) (*models.PlayerStatsModel, error) {
	target := current.Elo + delta
	if target < 0 {
		target = 0
	}
	return database.Must().Stats.SetElo(actor, guildID, pid, seasonID, target)
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
// Vote perks (Source "topgg_vote") are skipped: they follow vote state, which
// the bot doesn't track yet. A member boosting the server gets one booster
// stack, and a perk whose allowed roles include one of their managed
// (subscriber) roles counts as a booster perk.
func NewPerkResolver(session func() *discordgo.Session) func(guildID, userID string) PerkContext {
	cache := newTTLCache[[]Perk](eloConfigCacheTTL)
	return func(guildID, userID string) PerkContext {
		s := session()
		if s == nil {
			return PerkContext{}
		}
		perks, err := cache.get(guildID, func() ([]Perk, error) { return GuildPerks(guildID) })
		if err != nil || len(perks) == 0 {
			return PerkContext{}
		}
		member, err := s.State.Member(guildID, userID)
		if err != nil {
			if member, err = s.GuildMember(guildID, userID); err != nil {
				return PerkContext{}
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
			if p.Source != "topgg_vote" && p.QualifiesFor(held, nil) {
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
func (v *RegistrationVerifierStore) Begin(guildID, userID, username string) (string, error) {
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

	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("%06d", n.Int64())

	v.nameLast[nameKey] = now
	v.userHits[regKey(guildID, userID)] = append(hits, now)
	v.pending[regKey(guildID, userID)] = &pendingRegistration{username: username, code: code, expires: now.Add(regVerifyTTL)}
	return code, nil
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
	input = strings.TrimSpace(input)
	if len(input) != 6 || strings.Trim(input, "0123456789") != "" {
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
