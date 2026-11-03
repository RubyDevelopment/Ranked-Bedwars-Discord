package general

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/screenshare"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(ModerationSsHistoryCommand)
}

// ssHistoryEntries is how many of a player's most recent requests the history shows.
const ssHistoryEntries = 10

// ============================================================================
// Command Definition
// ============================================================================

// ModerationSsHistoryCommand lists the screenshare requests made on a player.
var ModerationSsHistoryCommand = &types.Command{
	Name:        "sshistory",
	Description: "Shows the screenshare requests made on a player",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "user", Description: "Player to view the screenshare history for", Type: discordgo.ApplicationCommandOptionUser, Required: true},
		},
	},
	Execute: executeModerationSsHistoryCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeModerationSsHistoryCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareInvalidTargetEmbed(i))
	}
	username := target.Username
	if svc := database.Get(); svc != nil {
		if view, err := svc.Player.Get(i.GuildID, target.ID); err == nil && view.Config != nil && view.Config.Username != "" {
			username = view.Config.Username
		}
	}
	return helpers.EditInteractionEmbed(s, i, ModerationSsHistoryEmbed(target, username, screenshare.Requests.ByTarget(i.GuildID, target.ID)))
}

// ============================================================================
// Helper functions
// ============================================================================

var ssStatusLabels = map[string]struct{ dot, label string }{
	types.ScreenshareActive:   {helpers.DotGreen, "Open"},
	types.ScreenshareClaimed:  {helpers.DotYellow, "Claimed"},
	types.ScreenshareClosed:   {helpers.DotGray, "Closed"},
	types.ScreenshareExpired:  {helpers.DotGray, "Expired"},
	types.ScreensharePending:  {helpers.DotYellow, "Awaiting review"},
	types.ScreenshareApproved: {helpers.DotGreen, "Approved"},
	types.ScreenshareDenied:   {helpers.DotRed, "Denied"},
}

func ModerationSsHistoryEmbed(target *discordgo.User, username string, requests []types.ScreenshareRequest) *discordgo.MessageEmbed {
	if len(requests) == 0 {
		return helpers.Preset("default", types.PresetOptions{
			Title:       "No Screenshare History",
			Description: fmt.Sprintf("<@%s> has never had a screenshare requested on them.", target.ID),
			Thumbnail:   helpers.MinecraftBustAvatarURL(username),
		})
	}
	var entries []string
	for _, r := range requests[:min(len(requests), ssHistoryEntries)] {
		status := ssStatusLabels[r.Status]
		review := ""
		if r.Kind == types.ScreenshareKindReview {
			review = " · SS Restricted request"
		}
		reason := r.Reason
		if runes := []rune(reason); len(runes) > 200 {
			reason = string(runes[:200])
		}
		entries = append(entries, strings.Join([]string{
			fmt.Sprintf("%s **Request #%d** | <t:%d:R>%s", status.dot, r.ID, r.CreatedAt, review),
			fmt.Sprintf("> **Status:** %s", status.label),
			fmt.Sprintf("> **Requested by:** <@%s>", r.RequesterID),
			fmt.Sprintf("> **Reason:** `%s`", strings.ReplaceAll(reason, "`", "'")),
		}, "\n"))
	}
	footer := fmt.Sprintf("%d requests", len(requests))
	if len(requests) == 1 {
		footer = "1 request"
	}
	if len(requests) > ssHistoryEntries {
		footer = fmt.Sprintf("Showing the latest %d of %d requests", ssHistoryEntries, len(requests))
	}
	return helpers.Preset("default", types.PresetOptions{
		Title:       "Screenshare History — " + username,
		Description: strings.Join(entries, "\n\n"),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
		FooterText:  footer,
	})
}
