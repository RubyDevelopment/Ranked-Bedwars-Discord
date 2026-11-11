package helpers

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/types"
)

// ============================================================================
// Embed Color Constants
// admin/server_config.go
// ============================================================================

// ============================================================================
// admin/server_setup.go
// ============================================================================

// ============================================================================
// general/player_compare.go
// ============================================================================

// ============================================================================
// general/game_view.go
// ============================================================================

// ============================================================================
// general/game_maps.go
// ============================================================================

const MaxEmbedDescriptionLen = 2000

// ============================================================================
// general/game_submit.go
// ============================================================================

func SubmitResultStaffScoringEmbed(i *discordgo.InteractionCreate, gameNumber int, team, screenshotURL string) *discordgo.MessageEmbed {
	return withProofImage(Preset("success", types.PresetOptions{
		Title:       "Result Submitted for Staff Scoring",
		Description: fmt.Sprintf("Your win screenshot for game **#%d** has been submitted. Staff will review and score the match using the screenshot as proof.\n\n> **Your Team:** `%s`\n> **Proof:** %s", gameNumber, team, screenshotURL),
		Thumbnail:   ThumbSprout,
	}), screenshotURL)
}

func SubmitResultCasualEmbed(i *discordgo.InteractionCreate, gameNumber int, team, screenshotURL string) *discordgo.MessageEmbed {
	return withProofImage(Preset("success", types.PresetOptions{
		Title:       "Casual Match Result Recorded",
		Description: fmt.Sprintf("Your win screenshot for game **#%d** has been recorded and scored.\n\n> **Your Team:** `%s`\n> **Proof:** %s", gameNumber, team, screenshotURL),
		Thumbnail:   ThumbSprout,
	}), screenshotURL)
}

// ============================================================================
// general/player_profile.go
// ============================================================================

func PlayerProfileNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. They must run `/register` before a profile is available.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

// ============================================================================
// general/player_register.go
// ============================================================================

func UnexpectedErrorPlayerRegisterEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to process your registration. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRegisterConflictEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Username Taken",
		Description: fmt.Sprintf("The username **%s** is already linked to another player in this guild. Try a different username or contact staff if this is your account.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/player_register_verify.go
// ============================================================================

// ============================================================================
// general/player_settings.go
// ============================================================================

// ============================================================================
// staff/game_rescore.go
// ============================================================================

func GameRescoreInvalidMvpEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid MVP",
		Description: "You must specify the MVP of the game. Select a player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func GameRescoreMvpNotInMatchEmbed(i *discordgo.InteractionCreate, mvp *discordgo.User) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid MVP",
		Description: fmt.Sprintf("<@%s> was not a participant in this game. The MVP must be a player who took part in the match.", mvp.ID),
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// staff/player_wipe.go
// ============================================================================

func PlayerWipeNoSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. An administrator should configure a season first.",
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// staff/moderation_strike.go
// ============================================================================

func UnexpectedErrorModerationStrikeEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while issuing the strike. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

// ModerationStrikeSuccessEmbedBy is the strike result credited to actor (nobody when the bot or a vote did it).
func ModerationStrikeSuccessEmbedBy(actor string, id uint, user *discordgo.User, username, strikeType, reason string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully issued a strike to the player. This action has been logged for staff review.\n\n> **Strike ID:** `#%d`\n> **Player:** <@%s> (**%s**)\n> **Reason:** %s",
		id, user.ID, username, reason,
	)
	if strikeType != "" {
		description += fmt.Sprintf("\n> **Type:** `%s`", strikeType)
	}
	description += PunishmentBy("Issued by", actor)
	return Preset("success", types.PresetOptions{
		Title:       "Strike Issued",
		Description: description,
		Thumbnail:   ThumbHammer,
	})
}

// ============================================================================
// Layouts: the pieces of text the lists below are built from
// ============================================================================

// Each one is a default a guild can rewrite on the dashboard (Commands → the command → Layouts). {variables} are
// filled per row; a line whose variables are all empty is left out. Add a layout here, use it with FillLayout.
var (
	// /queues in a game channel
	LayoutQueuesTeamTitle   = DefineLayout("queues.team_title", "Team heading", "👥 {team} · Avg ELO {avg_elo}", "team", "avg_elo", "players")
	LayoutQueuesPoolTitle   = DefineLayout("queues.pool_title", "Draft pool heading", "📦 Draft Pool · Avg ELO {avg_elo}", "avg_elo", "players")
	LayoutQueuesPlayer      = DefineRepeatedLayout("queues.player_row", "One player of a team or the pool", "{mention} (`{name}`)\n> ELO: `{elo}` · W/L: `{wlr}` · K/D: `{kdr}`\n> MVP: `{mvp}` · WLR: `{wlr_ratio}` · KDR: `{kdr_ratio}`", "mention", "id", "name", "elo", "team", "wlr", "kdr", "mvp", "wlr_ratio", "kdr_ratio")
	LayoutQueuesDraftTitle  = DefineLayout("queues.draft_title", "Draft turn heading", "👑 Draft Turn")
	LayoutQueuesDraftTurn   = DefineLayout("queues.draft_turn", "Draft turn text", "It is **{captain}**'s turn to pick.", "captain", "captain_mention")
	LayoutQueuesGameDetails = DefineLayout("queues.game_details", "Game stats description", "> **Mode:** `{mode}`\n> **Season:** `{season}`", "game", "mode", "season", "players")

	// /queues in a queue voice channel
	LayoutQueuesDetails       = DefineLayout("queues.queue_details", "Queue status description", "> **Channel:** {channel}\n> **Mode:** `{mode}` (`{teams}v{per_team}`)\n> **Total Slots:** `{slots}`\n> **Eligible:** `{eligible}/{present}` in VC", "channel", "mode", "teams", "per_team", "slots", "eligible", "present")
	LayoutQueuesBlockingTitle = DefineLayout("queues.blocking_title", "Not enough players heading", "⛔ Blocking Queue")
	LayoutQueuesBlockingText  = DefineLayout("queues.blocking_text", "Not enough players text", "Not enough eligible players (`{eligible}/{slots}`)", "eligible", "slots")
	LayoutQueuesBlockedTitle  = DefineLayout("queues.blocked_title", "Blocked players heading", "⛔ Blocked Players", "count")
	LayoutQueuesBlockedRow    = DefineRepeatedLayout("queues.blocked_row", "One blocked player", "> {mention} · {reason}", "mention", "id", "reason")
	LayoutQueuesReasonInGame  = DefineLayout("queues.reason_in_game", "Blocked: already in a game", "currently in a game")
	LayoutQueuesReasonNoReg   = DefineLayout("queues.reason_unregistered", "Blocked: not registered", "not registered")
	LayoutQueuesReasonBanned  = DefineLayout("queues.reason_restricted", "Blocked: has a restriction", "has active restriction")
	LayoutQueuesReasonPartyIncomplete = DefineLayout("queues.reason_party_incomplete", "Blocked: party not fully in the channel", "party incomplete (`{present}/{total}` in VC)", "present", "total")
	LayoutQueuesReadyTitle    =DefineLayout("queues.ready_title", "Ready heading", "✅ Ready to Start")
	LayoutQueuesReadyText     = DefineLayout("queues.ready_text", "Ready text", "`{eligible}/{slots}` eligible players present", "eligible", "slots")

	// /bots
	LayoutBotsGroupTitle = DefineLayout("bots.group_title", "Status heading", "{emoji} {label} ({count})", "emoji", "label", "count", "status")
	LayoutBotsBot        = DefineRepeatedLayout("bots.bot", "One bot", "{name}{note}", "name", "tier", "status", "note")
	LayoutBotsDisabled   = DefineLayout("bots.disabled_note", "Note after a disabled bot", " · disabled")
	LayoutBotsSeparator  = DefineLayout("bots.separator", "Between bots (\\n is a new line)", ", ")
)

var (
	// /maps
	LayoutMapsIntro         = DefineLayout("maps.intro", "Text above the list", "View the status of all ({count}) registered game maps for this guild.\n\n{maps}", "count", "maps", "modes")
	LayoutMapsLine          = DefineRepeatedLayout("maps.line", "One map", "**{name}** • Max Height: `{height}`\n> {teams}", "name", "height", "teams", "modes")
	LayoutMapsMode          = DefineRepeatedLayout("maps.mode", "One mode of a map", "**{mode}** • `{teams}`", "mode", "teams")
	LayoutMapsModeSeparator = DefineLayout("maps.mode_separator", "Between a map's modes (\\n is a new line)", ", ")
	LayoutMapsTeamSeparator = DefineLayout("maps.team_separator", "Between a mode's teams", " & ")
	LayoutMapsUnnamed       = DefineLayout("maps.unnamed", "Name of a map with none", "Unnamed Map")
	LayoutMapsNoHeight      = DefineLayout("maps.no_height", "Height of a map with none", "N/A")
	LayoutMapsNoTeams       = DefineLayout("maps.no_teams", "Teams of a mode not set up", "Not configured")
	LayoutMapsNoModes       = DefineLayout("maps.no_modes", "A map with no modes", "Teams: `Not configured`")
	LayoutMapsFooter        = DefineLayout("maps.footer", "Line under the list", "*Active modes: `{modes}`*", "modes", "count")

	// /leaderboard
	LayoutLeaderboardRow = DefineRepeatedLayout("leaderboard.row", "One player's line", "> **#{position}** · `{elo}` ELO · {wins}W/{losses}L (`{win_rate}`)", "position", "elo", "rank", "peak", "wins", "losses", "games", "win_rate", "kills", "deaths", "final_kills", "beds", "mvps")
)

var (
	// /ranks
	LayoutRanksProgress    = DefineLayout("ranks.progress", "Your standing, with a rank to go", "**Your ELO:** `{elo}`\n**Your Rank:** `{rank}` **Next Rank:** `{next_rank}` (in `{next_in}` ELO)", "elo", "rank", "next_rank", "next_min_elo", "next_in")
	LayoutRanksProgressMax = DefineLayout("ranks.progress_max", "Your standing, at the top rank", "**Your ELO:** `{elo}`\n**Your Rank:** `{rank}`\nYou have achieved maximum competitive rank status!", "elo", "rank")
	LayoutRanksGuest       = DefineLayout("ranks.guest", "Shown to someone not registered", "Use `/register` to track your seasonal elo and rank progression!")
	LayoutRanksHeading     = DefineLayout("ranks.heading", "Heading above the ranks", "__**Available Ranks:**__")
	LayoutRanksLine        = DefineRepeatedLayout("ranks.line", "One rank", "{role} · ELO: `{min_elo} - {max_elo}` | Win: `+{win}` | Loss: `-{loss}`", "role", "name", "min_elo", "max_elo", "win", "loss")
	LayoutRanksNoMax       = DefineLayout("ranks.no_max", "Top of the highest rank", "∞")
	LayoutRanksUnranked    = DefineLayout("ranks.unranked", "Name when you have no rank", "Unranked")
)

// ============================================================================
// Event Embeds (parties, tickets, punishments, queues, history)
// ============================================================================

func QueueDeniedEmbed(description string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Queue Entry Denied",
		Description: description,
		Thumbnail:   ThumbFlag,
	})
}

// unassignedGuildEmbed is the notice posted when the bot joins a guild that has no plan yet.
func unassignedGuildEmbed() *discordgo.MessageEmbed {
	return Preset("caution", types.PresetOptions{
		Title:       "Ranked Bedwars Is Connected",
		Description: "Ranked Bedwars has joined this guild and created its configuration. A plan is required before the bot can be used.",
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Getting Started",
				Value:  "1. An administrator opens the dashboard.\n2. Choose a plan, select this guild and request access.\n3. Once the request is approved, the bot switches on.",
				Inline: false,
			},
			{
				Name:   "Until Then",
				Value:  "Commands and buttons stay paused. The setup and configuration commands keep working, so the guild can be prepared in advance.",
				Inline: false,
			},
			{
				Name:   "Need Help?",
				Value:  "Contact support through the dashboard and include the name of this guild.",
				Inline: false,
			},
		},
		Thumbnail: ThumbGuild,
	})
}

// BuildStrikeHistoryEmbed renders the strike history embed for one page.
func BuildStrikeHistoryEmbed(guildID, username string, entries []models.PlayerStrikesModel, page int) *discordgo.MessageEmbed {
	totalPages := HistoryTotalPages(len(entries))
	description := fmt.Sprintf("This player has a total of **%d** strike(s) on record.", len(entries))
	if len(entries) == 0 {
		description = "No strike history was found for this player."
	} else {
		description += "\n\n" + strings.Join(BuildStrikeHistoryDescriptionsDirect(guildID, entries, page), "\n\n")
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's Strike History • Page %d of %d", username, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
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
	} else {
		description += "\n\n" + strings.Join(BuildDecayHistoryDescriptionsDirect(entries, page), "\n\n")
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's ELO Decay History • Page %d of %d", username, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
		FooterText:  HistoryPageFooter(page, totalPages, len(entries)),
	})
}

// BuildPunishmentHistoryEmbed renders the punishment history embed for one page.
func BuildPunishmentHistoryEmbed(guildID, username string, entries []models.PlayerPunishmentsModel, page int, activeOnly bool) *discordgo.MessageEmbed {
	totalPages := HistoryTotalPages(len(entries))
	description := fmt.Sprintf("This player has a total of **%d** punishment(s) on record.", len(entries))
	if len(entries) == 0 {
		description = "No punishment history was found for this player."
	} else {
		description += "\n\n" + strings.Join(BuildPunishmentHistoryDescriptionsDirect(guildID, entries, page), "\n\n")
	}
	titlePrefix := "Punishment History"
	if activeOnly {
		titlePrefix = "Active Punishments"
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's %s • Page %d of %d", username, titlePrefix, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
		FooterText:  HistoryPageFooter(page, totalPages, len(entries)),
	})
}

// BuildGameHistoryEmbedWithPlayers renders game history with the full participant lists.
func BuildGameHistoryEmbedWithPlayers(guildID, username string, entries []sdk.GameHistory, page int, players map[uint][]GameHistoryPlayer) *discordgo.MessageEmbed {
	totalPages := HistoryTotalPages(len(entries))
	description := fmt.Sprintf("This player has a total of **%d** game(s) on record for the current season.", len(entries))
	if len(entries) == 0 {
		description = "No game history was found for this player."
	} else {
		description += "\n\n" + strings.Join(buildGameHistoryDescriptions(guildID, entries, page, players), "\n\n")
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's Game History • Page %d of %d", username, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
		FooterText:  HistoryPageFooter(page, totalPages, len(entries)),
	})
}

func inactiveEmbed(guildID string, disabled bool, expires *time.Time) *discordgo.MessageEmbed {
	if disabled {
		return Preset("error", types.PresetOptions{Title: "Guild Disabled", Description: "Ranked Bedwars has been disabled for this guild. Contact support if you think this is a mistake.", Thumbnail: ThumbFlag})
	}
	desc := "This guild does not have an active plan, so Ranked Bedwars is paused."
	if expires != nil {
		desc = fmt.Sprintf("This guild's plan ended on <t:%d:D>, so Ranked Bedwars is paused.", expires.Unix())
	}
	desc += " An administrator can request a plan or a renewal in the dashboard"
	if url := planPageURL(guildID); url != "" {
		desc += ": " + url
	}
	desc += ". Settings and setup commands still work meanwhile."
	return Preset("error", types.PresetOptions{Title: "Plan Inactive", Description: desc, Thumbnail: ThumbFlag})
}

// optionalAvailableEmbed tells an administrator every required slot is now
// configured and lists cmdName's remaining optional ones, pairing with
// buildOptionalPromptComponents' Continue/Skip buttons.
func optionalAvailableEmbed(cmdName string, optional []types.Requirement) *discordgo.MessageEmbed {
	description := fmt.Sprintf("All required resources for `%s` are now configured, and it's ready to use.\n\n`%s` also has these optional resources available:\n", cmdName, cmdName)
	for _, req := range optional {
		description += fmt.Sprintf("- %s: %s\n", req.Type, req.Key)
	}
	description += "\nWould you like to configure them now, or skip for now? You're able to configure these at any time using `/config`."
	return Preset("caution", types.PresetOptions{
		Title:       "Optional Configuration Available",
		Description: description,
		Thumbnail:   ThumbFlag,
	})
}

func configurationCompleteEmbed(cmdName string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Configuration Complete",
		Description: fmt.Sprintf("`%s` is ready to use. Please re-run the command. You're able to configure it further at any time using `/config`.", cmdName),
		Thumbnail:   ThumbTada,
	})
}

// InvalidTimeFilterEmbed is shown when a time filter string is invalid.
func InvalidTimeFilterEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Time Filter",
		Description: "That time filter format is not recognized. Use values like `1d`, `7d`, `2w`, or omit the filter to show all history.",
	})
}

// UnexpectedErrorGameEmbed is the fallback for a game action that failed for an unknown reason.
func UnexpectedErrorGameEmbed(action string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: fmt.Sprintf("An unexpected error occurred while trying to %s. Please contact an administrator for assistance or try again later.", action),
	})
}

func HostGameDoneEmbed(text string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{Title: "Hosted Game", Description: text})
}

type botDisplayConfig struct {
	status      string
	labelLayout string // layout keys: a guild renames a status or swaps its dot
	emojiLayout string
}

var (
	botDisplayConfigs = []botDisplayConfig{
		botStatus("Available", DotGreen),
		botStatus("Starting", DotYellow),
		botStatus("Busy", DotRed),
		botStatus("Cooldown", DotOrange),
		botStatus("Offline", DotGray),
	}
)

// botStatus defines the layouts that name a status and its dot.
func botStatus(status, emoji string) botDisplayConfig {
	key := "bots.status_" + strings.ToLower(status)
	return botDisplayConfig{
		status:      status,
		labelLayout: DefineLayout(key+"_label", status+" label", status),
		emojiLayout: DefineLayout(key+"_emoji", status+" dot", emoji),
	}
}

// botDisplayStatus is the group an enabled account's stored state belongs in.
func botDisplayStatus(stored string) string {
	switch strings.ToLower(strings.TrimSpace(stored)) {
	case "starting":
		return "Starting"
	case "busy":
		return "Busy"
	case "cooldown":
		return "Cooldown"
	case "maintenance":
		return "Offline"
	}
	return "Available" // available, offline (rested) or not set: idle and ready to host
}

func BotsOverviewEmbed(guildID string, bots []types.Bot) *discordgo.MessageEmbed {
	statusGroups := make(map[string][]string, len(botDisplayConfigs))
	for _, cfg := range botDisplayConfigs {
		statusGroups[cfg.status] = []string{}
	}

	for _, bot := range bots {
		if strings.TrimSpace(bot.Username) == "" {
			continue
		}

		// The engine writes lowercase states: starting (leased), busy (on the server), cooldown (resting after a game) and
		// offline once it is rested, which for an enabled account means idle and ready to host.
		status, note := botDisplayStatus(bot.Status), ""
		if !bot.IsEnabled {
			status = "Offline"
			note = FillLayout(guildID, LayoutBotsDisabled, nil)
		}
		statusGroups[status] = append(statusGroups[status], FillLayout(guildID, LayoutBotsBot, map[string]string{
			"name": bot.Username, "tier": bot.Tier, "status": status, "note": note,
		}))
	}

	separator := LayoutSeparator(guildID, LayoutBotsSeparator)
	fields := make([]*discordgo.MessageEmbedField, 0, len(botDisplayConfigs))
	for _, cfg := range botDisplayConfigs {
		group := statusGroups[cfg.status]
		if len(group) == 0 {
			continue
		}

		value := strings.Join(group, separator)
		if len(value) > 1024 {
			value = value[:1021] + "..."
		}

		fields = append(fields, &discordgo.MessageEmbedField{
			Name: FillLayout(guildID, LayoutBotsGroupTitle, map[string]string{
				"emoji": FillLayout(guildID, cfg.emojiLayout, nil), "label": FillLayout(guildID, cfg.labelLayout, nil),
				"count": strconv.Itoa(len(group)), "status": cfg.status,
			}),
			Value:  value,
			Inline: false,
		})
	}

	return Preset("success", types.PresetOptions{
		Title:       "Registered Bots Status",
		Description: "Displays the status and uptime metrics of all registered game bots for this guild.",
		Fields:      fields,
		Thumbnail:   ThumbBot,
	})
}

var (
	ErrCompareNotRegistered = errors.New("player not registered")
	ErrCompareStatsHidden   = errors.New("player stats hidden")
)

// CompareSide is one player's resolved profile and season stats.
type CompareSide struct {
	Username string
	Stats    *models.PlayerStatsModel
	ShowElo  bool
}

// calculateKDR returns kills per death, treating a deathless record as its kill count.
func calculateKDR(kills, deaths int) float64 {
	if deaths == 0 {
		return float64(kills)
	}
	return float64(kills) / float64(deaths)
}

// CompareSideField renders one player's column of the comparison.
func CompareSideField(side *CompareSide) *discordgo.MessageEmbedField {
	elo := "Hidden"
	if side.ShowElo {
		elo = fmt.Sprintf("%d", side.Stats.Elo)
	}

	total := side.Stats.Wins + side.Stats.Losses
	winRate := 0.0
	if total > 0 {
		winRate = float64(side.Stats.Wins) / float64(total) * 100
	}

	return &discordgo.MessageEmbedField{
		Name: side.Username,
		Value: fmt.Sprintf(
			"> **ELO:** `%s`\n> **Rank:** `%s`\n> **Record:** `%dW` / `%dL`\n> **Win Rate:** `%.1f%%`\n> **KDR:** `%.2f`",
			elo, side.Stats.Rank, side.Stats.Wins, side.Stats.Losses, winRate, calculateKDR(side.Stats.Kills, side.Stats.Deaths),
		),
		Inline: true,
	}
}

// The old bot's thumbnails.
const (
	ThumbDoubleSwords = "https://i.imgur.com/YPxilj8.png"
	ThumbTimer        = "https://i.imgur.com/Ixj8JKk.png"
	ThumbTada         = "https://i.imgur.com/M5yz7dL.png"
	ThumbSprout       = "https://i.imgur.com/XHnyZh9.png"
	ThumbFlag         = "https://i.imgur.com/C2W5O9t.png"
	ThumbTrashCan     = "https://i.imgur.com/bKH6mWU.png"
)

// withProofImage shows a submitted screenshot in full below the embed when the proof is a picture (an attachment or an
// image link); any other link stays a link in the text.
// WithProofImage shows a submitted screenshot as the embed image (nothing when the link is not an image).
func WithProofImage(emb *discordgo.MessageEmbed, proofURL string) *discordgo.MessageEmbed {
	return withProofImage(emb, proofURL)
}

func withProofImage(emb *discordgo.MessageEmbed, proofURL string) *discordgo.MessageEmbed {
	link := strings.TrimSpace(proofURL)
	if !strings.HasPrefix(link, "https://") && !strings.HasPrefix(link, "http://") {
		return emb
	}
	path, _, _ := strings.Cut(link, "?")
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		emb.Image = &discordgo.MessageEmbedImage{URL: link}
	default:
		// A file attached in Discord was already checked to be a picture; its link may carry an unusual extension.
		if strings.Contains(path, "discordapp.com/attachments/") || strings.Contains(path, "discordapp.net/attachments/") {
			emb.Image = &discordgo.MessageEmbedImage{URL: link}
		}
	}
	return emb
}

// Dots used to show a player's state in a roster.
const (
	DotGreen  = "<:dot_green:1559223765953683476>"
	DotRed    = "<:dot_red:1559223764590272512>"
	DotGray   = "<:dot_gray:1559223768289775646>"
	DotYellow = "<:dot_yellow:1559223767270686730>"
	DotOrange = "<:dot_orange:1559223763785093301>"
	DotWhite  = "<:dot_white:1559223762514214953>"
)
