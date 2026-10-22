// This file holds every embed the commands send, one function per embed, so they can be read and edited in one place.
package helpers

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/types"
)

// ============================================================================
// admin/server_config.go
// ============================================================================

func RequirementMention(resourceType types.RequirementType, id string) string {
	if resourceType == types.ReqRole {
		return fmt.Sprintf("<@&%s>", id)
	}
	return fmt.Sprintf("<#%s>", id)
}

func EnabledLabel(enabled bool) string {
	if enabled {
		return "Enabled"
	}
	return "Disabled"
}

func ToggleLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func UnexpectedErrorServerConfigEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to update the configuration for this guild. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func ServerConfigUnknownCommandEmbed(i *discordgo.InteractionCreate, name string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unknown Command",
		Description: fmt.Sprintf("No registered command named **%s**.", name),
		Thumbnail:   ThumbFlag,
	})
}

func ServerConfigCommandNotSetUpEmbed(i *discordgo.InteractionCreate, name string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Command Not Set Up",
		Description: fmt.Sprintf("**%s** has no configuration yet. Run `/config setup` first.", name),
		Thumbnail:   ThumbFlag,
	})
}

func ServerConfigUnknownRequirementKeyEmbed(i *discordgo.InteractionCreate, cmdName, key string, slots map[string]types.RequirementSlot) *discordgo.MessageEmbed {
	valid := make([]string, 0, len(slots))
	for k := range slots {
		valid = append(valid, "`"+k+"`")
	}
	sort.Strings(valid)
	desc := fmt.Sprintf("**%s** has no requirement key **%s**.", cmdName, key)
	if len(valid) > 0 {
		desc += " Valid keys: " + strings.Join(valid, ", ")
	} else {
		desc += " This command declares no requirements of this type."
	}
	return Preset("error", types.PresetOptions{Title: "Unknown Requirement Key", Description: desc, Thumbnail: ThumbFlag})
}

func ServerConfigRequirementListEmbed(i *discordgo.InteractionCreate, resourceType types.RequirementType, cmdName string, slots map[string]types.RequirementSlot) *discordgo.MessageEmbed {
	title := fmt.Sprintf("%s Requirements — /%s", strings.ToUpper(string(resourceType[:1]))+string(resourceType[1:]), cmdName)
	if len(slots) == 0 {
		return Preset("info", types.PresetOptions{
			Title:       title,
			Description: fmt.Sprintf("**%s** declares no %s requirements.", cmdName, resourceType),
			Thumbnail:   ThumbGuild,
		})
	}

	keys := make([]string, 0, len(slots))
	for key := range slots {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	fields := make([]*discordgo.MessageEmbedField, 0, len(keys))
	for _, key := range keys {
		slot := slots[key]
		value := "`Not configured`"
		if slot.ID != "" {
			value = RequirementMention(resourceType, slot.ID)
		}
		state := "optional"
		if slot.Required {
			state = "required"
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("%s (%s)", key, state),
			Value:  value,
			Inline: true,
		})
	}
	return Preset("info", types.PresetOptions{
		Title:       title,
		Description: "Configured requirement keys for this command.",
		Fields:      fields,
		Thumbnail:   ThumbGuild,
	})
}

func ServerConfigRequirementSetEmbed(i *discordgo.InteractionCreate, resourceType types.RequirementType, cmdName, key, label string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Requirement Updated",
		Description: fmt.Sprintf("Successfully updated the command requirement.\n\n> **Command:** `/%s`\n> **Key:** `%s`\n> **Type:** `%s`\n> **Value:** %s", cmdName, key, resourceType, label),
		Thumbnail:   ThumbGuild,
	})
}

func ServerConfigRequirementGetEmbed(i *discordgo.InteractionCreate, resourceType types.RequirementType, cmdName, key string, slot types.RequirementSlot) *discordgo.MessageEmbed {
	value := "`Not configured`"
	if slot.ID != "" {
		value = RequirementMention(resourceType, slot.ID)
	}
	state := "optional"
	if slot.Required {
		state = "required"
	}
	return Preset("info", types.PresetOptions{
		Title:       "Command Requirement",
		Description: fmt.Sprintf("> **Command:** `/%s`\n> **Key:** `%s`\n> **Type:** `%s`\n> **State:** `%s`\n> **Value:** %s", cmdName, key, resourceType, state, value),
		Thumbnail:   ThumbGuild,
	})
}

func ServerConfigRequirementClearedEmbed(i *discordgo.InteractionCreate, resourceType types.RequirementType, cmdName, key string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Requirement Cleared",
		Description: fmt.Sprintf("Successfully cleared the command requirement.\n\n> **Command:** `/%s`\n> **Key:** `%s`\n> **Type:** `%s`", cmdName, key, resourceType),
		Thumbnail:   ThumbGuild,
	})
}

func ServerConfigUnknownRouteEmbed(i *discordgo.InteractionCreate, group, sub string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unknown Subcommand",
		Description: fmt.Sprintf("Unknown config route `%s %s`. Use `/config` without arguments to see available options.", group, sub),
		Thumbnail:   ThumbFlag,
	})
}

func ServerConfigInvalidKeyEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Key",
		Description: "You must provide a non-empty key.",
		Thumbnail:   ThumbFlag,
	})
}

func ServerConfigMissingValueEmbed(i *discordgo.InteractionCreate, kind string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Missing Value",
		Description: fmt.Sprintf("You must provide a %s.", kind),
		Thumbnail:   ThumbFlag,
	})
}

func ServerConfigBotViewEmbed(i *discordgo.InteractionCreate, prefix string, prefixEnabled, slashEnabled bool) *discordgo.MessageEmbed {
	lines := []string{
		fmt.Sprintf("> **Prefix:** `%s` (`%s`)", prefix, EnabledLabel(prefixEnabled)),
		fmt.Sprintf("> **Slash Commands:** `%s`", EnabledLabel(slashEnabled)),
	}
	return Preset("info", types.PresetOptions{
		Title:       "Bot Settings",
		Description: strings.Join(lines, "\n"),
		Thumbnail:   ThumbGuild,
	})
}

func ServerConfigInvalidPrefixEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Prefix",
		Description: "Please provide a valid prefix of 1-5 characters.",
		Thumbnail:   ThumbFlag,
	})
}

func ServerConfigPrefixUpdatedEmbed(i *discordgo.InteractionCreate, prefix string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Prefix Updated",
		Description: fmt.Sprintf("Successfully updated the command prefix.\n\n> **Prefix:** `%s`", prefix),
		Thumbnail:   ThumbGuild,
	})
}

func ServerConfigBotToggleEmbed(i *discordgo.InteractionCreate, target string, enabled bool) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Bot Setting Updated",
		Description: fmt.Sprintf("Successfully updated the command mode.\n\n> **Mode:** `%s`\n> **State:** `%s`", target, ToggleLabel(enabled)),
		Thumbnail:   ThumbGuild,
	})
}

// ============================================================================
// admin/server_setup.go
// ============================================================================

func SetupResultLines(result *types.SetupResult) []string {
	lines := []string{}
	if result.GuildCreated {
		lines = append(lines, "> Guild database record")
	}
	if result.ConfigCreated {
		lines = append(lines, "> Guild configuration scaffold")
	}
	if result.SnapshotCreated {
		lines = append(lines, "> Guild snapshot scaffold")
	}
	if result.SnapshotSynced {
		lines = append(lines, "> Discord role and channel snapshot sync")
	}
	if result.MetaCreated {
		lines = append(lines, "> Game meta scaffold")
	}
	if result.GameConfigCreated {
		lines = append(lines, "> Game configuration scaffold")
	}
	if len(lines) == 0 {
		lines = append(lines, "> Nothing new was required; existing records were refreshed")
	}
	return lines
}

func ServerSetupInvalidSubcommandEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "Use `/setup init` or `/setup sync`.",
		Thumbnail:   ThumbFlag,
	})
}

func ServerSetupFailedEmbed(i *discordgo.InteractionCreate, err error) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Guild Setup Failed",
		Description: fmt.Sprintf("Could not initialize this guild with Ranked Bedwars: %v. Verify the bot has the required permissions and try again.", err),
		Thumbnail:   ThumbFlag,
	})
}

func ServerSyncFailedEmbed(i *discordgo.InteractionCreate, err error) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Guild Sync Failed",
		Description: fmt.Sprintf("Could not sync this guild's data: %v. Verify the bot has the required permissions and try again.", err),
		Thumbnail:   ThumbFlag,
	})
}

func ServerSetupSuccessEmbed(i *discordgo.InteractionCreate, result *types.SetupResult) *discordgo.MessageEmbed {
	lines := []string{
		"This guild is now registered with Ranked Bedwars.",
		"",
		"**What was created:**",
	}
	lines = append(lines, SetupResultLines(result)...)
	lines = append(lines, "", "Use `/config` or the web dashboard to assign roles, channels, and bots.")

	return Preset("success", types.PresetOptions{
		Title:       "Guild Setup Complete",
		Description: strings.Join(lines, "\n"),
		Thumbnail:   ThumbGuild,
	})
}

func ServerSyncSuccessEmbed(i *discordgo.InteractionCreate, result *types.SetupResult) *discordgo.MessageEmbed {
	lines := []string{
		"This guild's data has been synced successfully.",
		"",
		"**What was synced:**",
	}
	lines = append(lines, SetupResultLines(result)...)
	lines = append(lines, "", "Use `/config` or the web dashboard to assign roles, channels, and bots.")

	return Preset("success", types.PresetOptions{
		Title:       "Guild Sync Complete",
		Description: strings.Join(lines, "\n"),
		Thumbnail:   ThumbGuild,
	})
}

// ============================================================================
// general/bot_status.go
// ============================================================================

func UnexpectedErrorBotsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the registered game bots for this guild. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func EmptyBotsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Registered Bots",
		Description: "This guild has no registered game bots. If you think this is an error, please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// general/game_compare.go
// ============================================================================

func UnexpectedErrorCompareEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to compare these players. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func CompareInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify an opponent to compare against. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func CompareSelfComparisonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You cannot compare a player against themselves. Select a different opponent.",
		Thumbnail:   ThumbFlag,
	})
}

func CompareBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have ranked stats. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func CompareNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. If you think this is an error, please contact an administrator for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// general/game_history.go
// ============================================================================

func UnexpectedErrorGameHistoryEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the game history. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func GameHistoryBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have a game history. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func GameHistoryNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register with `/register` to link a Minecraft account first.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func GameHistoryNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. If you think this is an error, please contact an administrator for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

func GameHistoryEmptyEmbed(i *discordgo.InteractionCreate, username, filter string) *discordgo.MessageEmbed {
	description := "No game history was found for this player in the current season."
	if filter != "" {
		description = fmt.Sprintf("No game history was found for this player in the last **%s**. Try a longer filter or remove it.", filter)
	}
	return Preset("success", types.PresetOptions{
		Title:       "No Games Found",
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/game_info.go
// ============================================================================

func OnOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func GameInfoNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func GameInfoNoGameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Game",
		Description: "You must either run this command in a game channel or specify a game number.",
		Thumbnail:   ThumbFlag,
	})
}

func GameInfoOverviewEmbed(i *discordgo.InteractionCreate, game Game) *discordgo.MessageEmbed {
	var description strings.Builder

	// Basic info
	description.WriteString(fmt.Sprintf("**Game Number:** #%d\n", game.Number))
	description.WriteString(fmt.Sprintf("**Mode:** %s\n", game.Mode.Name))
	description.WriteString(fmt.Sprintf("**State:** %s\n", game.State))
	description.WriteString(fmt.Sprintf("**Map:** %s\n", game.Map))

	// Timing
	if !game.CreatedAt.IsZero() {
		description.WriteString(fmt.Sprintf("**Created:** %s\n", game.CreatedAt.UTC().Format("Jan 2, 2006 15:04")))
	}
	if !game.StartedAt.IsZero() {
		description.WriteString(fmt.Sprintf("**Started:** %s\n", game.StartedAt.UTC().Format("Jan 2, 2006 15:04")))
	}

	// Teams
	if len(game.Teams) > 0 {
		description.WriteString("\n**Teams:**\n")
		for _, teamName := range game.Teams {
			teamPlayers := game.TeamPlayers(teamName)
			playerList := make([]string, len(teamPlayers))
			for idx, p := range teamPlayers {
				captainMark := ""
				if p.Captain {
					captainMark = "👑 "
				}
				playerList[idx] = fmt.Sprintf("%s<@%s>", captainMark, p.UserID)
			}
			description.WriteString(fmt.Sprintf("> **%s:** %s\n", teamName, strings.Join(playerList, ", ")))
		}
	}

	// Custom game info
	if game.Custom != nil {
		description.WriteString("\n**Custom Game:**\n")
		description.WriteString(fmt.Sprintf("> **Ranked:** %s\n", OnOff(game.Custom.Ranked)))
		description.WriteString(fmt.Sprintf("> **Stats:** %s\n", OnOff(game.Custom.Stats)))
		if game.Custom.HostedBy != "" {
			description.WriteString(fmt.Sprintf("> **Host:** <@%s>\n", game.Custom.HostedBy))
		}
	}

	// Void info
	if game.State == GameVoided {
		description.WriteString("\n**Void Information:**\n")
		description.WriteString(fmt.Sprintf("> **Reason:** %s\n", game.VoidReason))
		if game.VoidedBy != "" {
			description.WriteString(fmt.Sprintf("> **Voided By:** <@%s>\n", game.VoidedBy))
		}
	}

	// Winner info
	if game.Winner != "" {
		description.WriteString(fmt.Sprintf("\n**Winner:** %s\n", game.Winner))
	}
	if game.MVP != "" {
		description.WriteString(fmt.Sprintf("**MVP:** <@%s>\n", game.MVP))
	}

	// Channels
	if game.Channels.Text != "" {
		description.WriteString("\n**Channels:**\n")
		description.WriteString(fmt.Sprintf("> **Text:** <#%s>\n", game.Channels.Text))
		if len(game.Channels.Voice) > 0 {
			for team, channelID := range game.Channels.Voice {
				description.WriteString(fmt.Sprintf("> **%s Voice:** <#%s>\n", team, channelID))
			}
		}
	}

	// Duration if in game or completed
	if !game.StartedAt.IsZero() {
		var endTime time.Time
		if game.State == GameCleanup || game.State == GameVoided {
			endTime = game.StateAt
		} else {
			endTime = time.Now()
		}
		duration := endTime.Sub(game.StartedAt)
		description.WriteString(fmt.Sprintf("\n**Duration:** %s\n", duration.Round(time.Second)))
	}

	return Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Game Information · #%d", game.Number),
		Description: description.String(),
		Thumbnail:   ThumbDoubleSwords,
	})
}

// ============================================================================
// general/game_leaderboard.go
// ============================================================================

func UnexpectedErrorLeaderboardEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the leaderboard for this guild. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func LeaderboardNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured.",
		Thumbnail:   ThumbFlag,
	})
}

func LeaderboardEmptyEmbed(i *discordgo.InteractionCreate, season string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("Leaderboard — %s", season),
		Description: "No players have stats for the active season yet.",
		Thumbnail:   ThumbPlayers,
	})
}

func LeaderboardEmptyPageEmbed(i *discordgo.InteractionCreate, page int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Page Empty",
		Description: fmt.Sprintf("There are no leaderboard entries on page `%d`. Try a lower page number.", page),
		Thumbnail:   ThumbFlag,
	})
}

func LeaderboardOverviewEmbed(i *discordgo.InteractionCreate, rows []sdk.LeaderboardEntry, season string, page, totalPages int) *discordgo.MessageEmbed {
	var b strings.Builder
	for _, row := range rows {
		total := row.Wins + row.Losses
		winRate := 0.0
		if total > 0 {
			winRate = float64(row.Wins) / float64(total) * 100
		}
		b.WriteString(FillLayout(GuildOf(i), LayoutLeaderboardRow, map[string]string{
			"position": fmt.Sprint(row.Position), "elo": fmt.Sprint(row.Elo), "wins": fmt.Sprint(row.Wins), "losses": fmt.Sprint(row.Losses),
			"games": fmt.Sprint(total), "win_rate": fmt.Sprintf("%.0f%%", winRate), "rank": row.Rank, "peak": fmt.Sprint(row.HighestElo),
			"kills": fmt.Sprint(row.Kills), "deaths": fmt.Sprint(row.Deaths), "final_kills": fmt.Sprint(row.FinalKills), "beds": fmt.Sprint(row.BedsDestroyed), "mvps": fmt.Sprint(row.MvpCount),
		}) + "\n")
	}

	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("Leaderboard — %s", season),
		Description: b.String(),
		FooterText:  fmt.Sprintf("Page %d/%d", page, totalPages),
		Thumbnail:   ThumbPlayers,
	})
}

// ============================================================================
// general/game_maps.go
// ============================================================================

const MaxEmbedDescriptionLen = 2000

func FormatMapDisplayLine(guildID string, mapRecord types.Map, modes []string) string {
	mapName := strings.TrimSpace(mapRecord.Name)
	if mapName == "" {
		mapName = FillLayout(guildID, LayoutMapsUnnamed, nil)
	}

	height := FillLayout(guildID, LayoutMapsNoHeight, nil)
	if mapRecord.MapHeight > 0 {
		height = fmt.Sprintf("%d", mapRecord.MapHeight)
	}

	// Each mode can have its own teams on a map; modes that share a list are shown once.
	var segments []string
	for _, mode := range modes {
		teams := mapRecord.TeamsFor(mode)
		list := FillLayout(guildID, LayoutMapsNoTeams, nil)
		if len(teams) > 0 {
			list = strings.Join(teams, FillLayout(guildID, LayoutMapsTeamSeparator, nil))
		}
		segments = append(segments, FillLayout(guildID, LayoutMapsMode, map[string]string{"mode": mode, "teams": list}))
	}
	teamsLine := FillLayout(guildID, LayoutMapsNoModes, nil)
	if len(segments) > 0 {
		teamsLine = strings.Join(segments, LayoutSeparator(guildID, LayoutMapsModeSeparator))
	}

	return FillLayout(guildID, LayoutMapsLine, map[string]string{"name": mapName, "height": height, "teams": teamsLine, "modes": strings.Join(modes, ", ")})
}

func UnexpectedErrorMapsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the registered game maps for this guild. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func EmptyMapsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Registered Maps",
		Description: "This guild has no registered game maps. If you think this is an error, please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func MapsRotationEmbed(i *discordgo.InteractionCreate, maps []types.Map, targetModes []string) *discordgo.MessageEmbed {
	lines := make([]string, 0, len(maps)+1)
	currentLength := 0

	for _, mapRecord := range maps {
		line := FormatMapDisplayLine(GuildOf(i), mapRecord, targetModes)
		if currentLength+len(line)+1 > MaxEmbedDescriptionLen {
			break
		}
		lines = append(lines, line)
		currentLength += len(line) + 1
	}

	modesLine := FillLayout(GuildOf(i), LayoutMapsFooter, map[string]string{"modes": strings.Join(targetModes, ", "), "count": fmt.Sprint(len(maps))})
	if currentLength+len(modesLine)+1 <= MaxEmbedDescriptionLen {
		lines = append(lines, modesLine)
	}

	return Preset("success", types.PresetOptions{
		Title:       "Registered Maps",
		Description: fmt.Sprintf("View the status of all (%d) registered game maps for this guild.\n\n%s", len(maps), strings.Join(lines, "\n")),
		Thumbnail:   ThumbBed,
	})
}

// ============================================================================
// general/game_pugs.go
// ============================================================================

func PugsListUnavailableEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not Yet Implemented",
		Description: "The PUGS division list requires additional infrastructure (snapshot system, role management) and is not available yet.",
		Thumbnail:   ThumbFlag,
	})
}

func PugsVoteUnavailableEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not Yet Implemented",
		Description: "The PUGS voting system requires additional infrastructure (voting channels, role management) and is not available yet.",
		Thumbnail:   ThumbFlag,
	})
}

func PugsInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a real player to vote for. Select a member from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PugsInvalidSubcommandEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "Use `/pugs list` or `/pugs vote`.",
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// general/game_ranks.go
// ============================================================================

func UnexpectedErrorRanksEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the registered ranks for this guild. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func EmptyRanksEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Configured Ranks",
		Description: "This guild has no configured rank tiers. If you think this is an error, please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func RanksOverviewEmbed(i *discordgo.InteractionCreate, content string) *discordgo.MessageEmbed {
	header := "View the status of all registered game ranks for this guild.\n\n"
	fullDescription := header + content

	if len(fullDescription) > 2000 {
		fullDescription = fullDescription[:1997] + "..."
	}

	return Preset("success", types.PresetOptions{
		Title:       "Registered Ranks",
		Description: fullDescription,
		Thumbnail:   ThumbGuild,
	})
}

// ============================================================================
// general/game_submit.go
// ============================================================================

func SubmitResultNotGameChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not a Game Channel",
		Description: "This command can only be used in a game text channel. Run it in the text channel of your match.",
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultNotInMatchEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in Match",
		Description: "You must be a participant in this game to submit a result.",
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultMissingAttachmentEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Missing Attachment",
		Description: "You must provide a screenshot of the win screen as an attachment or provide a valid proof URL.",
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultInvalidAttachmentEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Attachment",
		Description: "The attachment must be an image file (PNG, JPG, GIF or WebP).",
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultNotInProgressEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not In Progress",
		Description: "This game is not currently in progress, so a result cannot be submitted for it.",
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultTooEarlyEmbed(i *discordgo.InteractionCreate, remaining time.Duration) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Too Early",
		Description: fmt.Sprintf("You must wait **%s** before submitting a result for this game.", remaining.Round(time.Second)),
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultAlreadySubmittedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Submitted",
		Description: "A result has already been submitted for this game and is waiting for staff to score it.",
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultNoTeamEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Team",
		Description: "You are not assigned to a team in this game, so you cannot submit a result.",
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultStaffScoringEmbed(i *discordgo.InteractionCreate, gameNumber int, team, screenshotURL string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Result Submitted for Staff Scoring",
		Description: fmt.Sprintf("Your win screenshot for game **#%d** has been submitted. Staff will review and score the match using the screenshot as proof.\n\n> **Your Team:** `%s`\n> **Proof:** %s", gameNumber, team, screenshotURL),
		Thumbnail:   ThumbSprout,
	})
}

func SubmitResultCasualEmbed(i *discordgo.InteractionCreate, gameNumber int, team, screenshotURL string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Casual Match Result Recorded",
		Description: fmt.Sprintf("Your win screenshot for game **#%d** has been recorded and scored.\n\n> **Your Team:** `%s`\n> **Proof:** %s", gameNumber, team, screenshotURL),
		Thumbnail:   ThumbSprout,
	})
}

// ============================================================================
// general/game_void.go
// ============================================================================

func VoidMatchNotGameChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not a Game Channel",
		Description: "This command can only be used in a game text channel. Run it in the text channel of the match you want to void.",
		Thumbnail:   ThumbFlag,
	})
}

func VoidMatchNotInMatchEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in Match",
		Description: "You must be a participant in this game to void it.",
		Thumbnail:   ThumbFlag,
	})
}

func VoidMatchStaffOnlyEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Staff Only",
		Description: "Only staff can void matches. Please contact a staff member for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

func VoidMatchSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Game Voided",
		Description: fmt.Sprintf("Game **#%d** has been voided. No ELO or stats were recorded.", gameNumber),
		Thumbnail:   ThumbTrashCan,
	})
}

// ============================================================================
// general/player_claim_elo.go
// ============================================================================

func UnexpectedErrorClaimEloEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while claiming your starting ELO. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func ClaimEloNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not Registered",
		Description: "You must register with `/register` before claiming ELO.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func ClaimEloNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. If you think this is an error, please contact an administrator for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

func ClaimEloNoPerkEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Starting ELO Perk",
		Description: "None of your perks grant starting ELO. Perks come from roles, so check that you hold the role a perk requires.",
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func ClaimEloSuccessEmbed(i *discordgo.InteractionCreate, username string, c *Claim) *discordgo.MessageEmbed {
	rank := c.RankAfter
	if c.RankBefore != c.RankAfter {
		rank = fmt.Sprintf("%s → %s", c.RankBefore, c.RankAfter)
	}
	return Preset("success", types.PresetOptions{
		Title:       "Starting ELO Claimed",
		Description: fmt.Sprintf("**%s** received **%d** starting ELO.", username, c.Amount),
		Thumbnail:   MinecraftBustAvatarURL(username),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "ELO", Value: fmt.Sprintf("%d → %d", c.Before, c.After), Inline: true},
			{Name: "Rank", Value: rank, Inline: true},
			{Name: "Perks", Value: strings.Join(c.Perks, ", "), Inline: false},
		},
	})
}

// ============================================================================
// general/player_decays.go
// ============================================================================

func UnexpectedErrorDecaysEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the decay history. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func DecaysBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have a decay history. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func DecaysNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register with `/register` to link a Minecraft account first.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func DecaysNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. If you think this is an error, please contact an administrator for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

func DecaysEmptyEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "No Decays Found",
		Description: "No ELO decay was found for this player this season. This player has stayed active.",
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func DecaysEmptyFilteredEmbed(i *discordgo.InteractionCreate, username, filter string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Decays Empty",
		Description: fmt.Sprintf("No ELO decay was found for this player in the last **%s**. Try a longer filter or remove it.", filter),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/player_nickname.go
// ============================================================================

func UnexpectedErrorNicknameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to update your nickname. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func NicknameRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register before setting a nickname. Run `/register` to link your Minecraft account first.",
		Thumbnail:   ThumbFlag,
	})
}

func NicknameNoneActiveEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Nickname Set",
		Description: "It looks like you do not have an active nickname to remove. Provide a nickname with `/nickname` to assign one first.",
		Thumbnail:   ThumbFlag,
	})
}

func NicknameInvalidUsernameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Nickname",
		Description: "That nickname format is not valid. Nicknames must be 3-16 characters and contain only letters, numbers, and underscores.",
		Thumbnail:   ThumbFlag,
	})
}

func NicknameAlreadySetEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Nickname Already Set",
		Description: fmt.Sprintf("Your nickname is already set to **%s**. No further action is needed.", nickname),
		Thumbnail:   MinecraftBustAvatarURL(nickname),
	})
}

func NicknamePremiumNameEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Premium Username",
		Description: fmt.Sprintf("**%s** is a premium Minecraft username and cannot be used as a nickname. Choose a different nickname that is not a registered premium name.", nickname),
		Thumbnail:   MinecraftBustAvatarURL(nickname),
	})
}

func NicknameConflictEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Nickname Taken",
		Description: fmt.Sprintf("The nickname **%s** is already in use in this guild. Try a different nickname.", nickname),
		Thumbnail:   MinecraftBustAvatarURL(nickname),
	})
}

func NicknameAPIOutageEmbed(i *discordgo.InteractionCreate, label string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Verification Unavailable",
		Description: fmt.Sprintf("The %s verification service is currently unavailable. Please try again later.", label),
		Thumbnail:   ThumbFlag,
	})
}

func NicknameRemovedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Nickname Removed",
		Description: "Your in-game nickname has been cleared. Use `/player_refresh` to refresh your displayed name.",
		Thumbnail:   ThumbLoad,
	})
}

func NicknameUpdatedEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Nickname Updated",
		Description: fmt.Sprintf(
			"Your in-game nickname has been updated to **%s**. Please wait a moment for your displayed name to update.",
			nickname,
		),
		Thumbnail: MinecraftBustAvatarURL(nickname),
	})
}

// ============================================================================
// general/player_premium.go
// ============================================================================

func PlayerPremiumUnavailableEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Premium Features",
		Description: "Premium management features are currently under development. Check back soon for subscription benefits, perk overrides, and custom styling options.",
		Thumbnail:   ThumbSprout,
	})
}

// ============================================================================
// general/player_profile.go
// ============================================================================

func UnexpectedErrorPlayerProfileEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the profile. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerProfileBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have ranked profiles. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerProfileNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. They must run `/register` before a profile is available.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func PlayerProfilePrivacyEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Profile Hidden",
		Description: fmt.Sprintf("**%s** has chosen to hide their profile from other players. Only they can view their own profile.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerProfileOverviewEmbed(i *discordgo.InteractionCreate, user *discordgo.User, config *models.PlayerConfigsModel, stats *models.PlayerStatsModel, season string, showElo bool) *discordgo.MessageEmbed {
	nickname := "None"
	if config.Nickname != nil && strings.TrimSpace(*config.Nickname) != "" {
		nickname = *config.Nickname
	}

	rank := "Unranked"
	eloLine := "> **ELO:** `Hidden`"
	recordLine := "> **Record:** `No games played`"
	streakLine := "> **Win Streak:** `0`"

	if stats != nil {
		rank = stats.Rank
		if showElo {
			eloLine = fmt.Sprintf("> **ELO:** `%d` (Peak `%d`)", stats.Elo, stats.HighestElo)
		}

		total := stats.Wins + stats.Losses
		winRate := 0.0
		if total > 0 {
			winRate = float64(stats.Wins) / float64(total) * 100
		}
		recordLine = fmt.Sprintf("> **Record:** `%dW` / `%dL` (`%.1f%%` WR)", stats.Wins, stats.Losses, winRate)
		streakLine = fmt.Sprintf("> **Win Streak:** `%d` (Best `%d`)", stats.WinStreak, stats.HighestWinStreak)
	} else if showElo {
		eloLine = "> **ELO:** `N/A`"
	}

	seasonLine := "> **Season:** `None`"
	if strings.TrimSpace(season) != "" {
		seasonLine = fmt.Sprintf("> **Season:** `%s`", season)
	}

	registered := "Unknown"
	if !config.CreatedAt.IsZero() {
		registered = config.CreatedAt.UTC().Format("Jan 2, 2006")
	}

	description := strings.Join([]string{
		fmt.Sprintf("> **Discord:** <@%s>", user.ID),
		fmt.Sprintf("> **Username:** `%s`", config.Username),
		fmt.Sprintf("> **Nickname:** `%s`", nickname),
		fmt.Sprintf("> **Rank:** `%s`", rank),
		eloLine,
		recordLine,
		streakLine,
		seasonLine,
		fmt.Sprintf("> **Registered:** `%s`", registered),
	}, "\n")

	return Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Player Profile — %s", config.Username),
		Description: description + "\n\nUse `/stats` for a full seasonal breakdown.",
		Thumbnail:   MinecraftBustAvatarURL(config.Username),
	})
}

// ============================================================================
// general/player_punishments.go
// ============================================================================

func UnexpectedErrorPlayerPunishmentsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the punishment history. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerPunishmentsBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have a punishment history. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerPunishmentsNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register with `/register` to link a Minecraft account first.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func PlayerPunishmentsEmptyEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "History Clear",
		Description: "No punishment history was found for this player. This player maintains a clean record.",
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerPunishmentsEmptyActiveEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "No Active Punishments",
		Description: "No active punishments were found for this player. This player maintains a clean record.",
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerPunishmentsEmptyFilteredEmbed(i *discordgo.InteractionCreate, username, filter string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "History Empty",
		Description: fmt.Sprintf("No punishment history was found for this player in the last **%s**. Try a longer filter or remove it.", filter),
		Thumbnail:   MinecraftBustAvatarURL(username),
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

func PlayerRegisterNoRanksEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Unavailable",
		Description: "No ranks have been configured for this guild yet, so you cannot be registered. Please ask an administrator to set up at least one rank.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRegisterNoSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Unavailable",
		Description: "This guild has no active season yet, so you cannot be registered. Please ask an administrator to start one on the dashboard's Season Periods page.",
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

func PlayerRegisterAlreadyRegisteredEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Registered",
		Description: fmt.Sprintf("You are already registered as **%s**. Use `/rename` to change your username.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerRegisterAPIOutageEmbed(i *discordgo.InteractionCreate, network string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Verification Unavailable",
		Description: fmt.Sprintf("Could not verify that account on **%s** right now. Please try again in a few minutes.", network),
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRegisterAccountNotFoundEmbed(i *discordgo.InteractionCreate, username, network string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Account Not Found",
		Description: fmt.Sprintf("Could not find a Minecraft account named **%s** on **%s**. Double-check the spelling and try again.", username, network),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerRegisterInvalidUsernameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Username",
		Description: "That Minecraft username format is not valid. Usernames must be 3-16 characters and contain only letters, numbers, and underscores.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRegisterSuccessEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Registration Complete",
		Description: fmt.Sprintf(
			"Your Discord account has been successfully linked. Please wait a moment for your roles and display name to update.\n\n> **Username:** `%s`",
			username,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/player_register_verify.go
// ============================================================================

func Plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func RegisterContactingEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Searching For Player",
		Description: fmt.Sprintf("A bot is inviting **%s** to a party on the Minecraft server. Make sure you are online and **accept the party invite**. This can take up to a minute or two.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func RegisterCodePromptEmbed(i *discordgo.InteractionCreate, username string, length int, cfg *types.InGameCode) *discordgo.MessageEmbed {
	minutes := 10
	if cfg != nil && cfg.ExpiresMinutes >= 1 && cfg.ExpiresMinutes <= 60 {
		minutes = cfg.ExpiresMinutes
	}
	return Preset("info", types.PresetOptions{
		Title:       "Check Your Party Chat",
		Description: fmt.Sprintf("A %d-character code was sent to **%s** in the Minecraft party chat. Press **Enter code** and type it in to finish registering. The code expires in %d minute%s.", length, username, minutes, Plural(minutes)),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func RegisterUseSlashEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Slash Command Required",
		Description: "This guild verifies Minecraft accounts with a code, which needs buttons. Please use `/register` instead of the prefix command.",
		Thumbnail:   ThumbFlag,
	})
}

func RegisterVerifyUnavailableEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Verification Unavailable",
		Description: "Account verification is not available right now. Please try again in a few minutes, or contact an administrator if it keeps happening.",
		Thumbnail:   ThumbFlag,
	})
}

func RegisterRateLimitedEmbed(i *discordgo.InteractionCreate, wait time.Duration) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Slow Down",
		Description: fmt.Sprintf("A verification code was requested very recently. Please try again in **%s**.", wait.Round(time.Second)),
		Thumbnail:   ThumbFlag,
	})
}

func RegisterNoPendingEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Nothing To Verify",
		Description: "You have no registration waiting for a code, or it expired. Run `/register` to start again.",
		Thumbnail:   ThumbFlag,
	})
}

func RegisterCodeRejectedEmbed(i *discordgo.InteractionCreate, result CodeResult, attemptsLeft int) *discordgo.MessageEmbed {
	switch result {
	case CodeWrong:
		return Preset("error", types.PresetOptions{
			Title:       "Invalid Code",
			Description: fmt.Sprintf("That code is not correct. You have **%d** attempt%s left.", attemptsLeft, Plural(attemptsLeft)),
			Thumbnail:   ThumbFlag,
		})
	case CodeInvalid:
		return Preset("error", types.PresetOptions{
			Title:       "Invalid Code",
			Description: "That is not the code format. Check the Minecraft party chat and try again.",
			Thumbnail:   ThumbFlag,
		})
	case CodeLocked:
		return Preset("error", types.PresetOptions{
			Title:       "Too Many Attempts",
			Description: "That was the last attempt. Run `/register` again to get a new code.",
			Thumbnail:   ThumbFlag,
		})
	}
	return RegisterNoPendingEmbed(i)
}

func RegisterVerifyFailedEmbed(i *discordgo.InteractionCreate, username, status string) *discordgo.MessageEmbed {
	title, description := "Verification Unavailable", "Account verification is not available right now. Please try again in a few minutes, or contact an administrator if it keeps happening."
	switch status {
	case "offline":
		title = "Join The Server First"
		description = fmt.Sprintf("**%s** does not appear to be online on the Minecraft server. Join the server, then run `/register` again.", username)
	case "not_joined":
		title = "Party Invite Not Accepted"
		description = fmt.Sprintf("A party invite was sent to **%s**, but you did not join it in time. Make sure you are on the server and accept the invite, then run `/register` again.", username)
	case "no_bot":
		title = "Verification Busy"
		description = "Every verification bot is busy right now. Please try again in a minute."
	}
	return Preset("error", types.PresetOptions{Title: title, Description: strings.TrimSpace(description), Thumbnail: ThumbFlag})
}

// ============================================================================
// general/player_rename.go
// ============================================================================

func UnexpectedErrorPlayerRenameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to update your username. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRenameRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register before you can rename your linked account. Run `/register` to link your Minecraft account first.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRenameProfileNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Profile Not Found",
		Description: "Your guild player profile could not be found. Try running `/player_refresh` or contact staff if the issue persists.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRenameAlreadyLinkedEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Linked",
		Description: fmt.Sprintf("Your account is already linked to **%s**. No further changes are needed.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerRenameInvalidUsernameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Username",
		Description: "That Minecraft username format is not valid. Usernames must be 3-16 characters and contain only letters, numbers, and underscores.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRenameAPIOutageEmbed(i *discordgo.InteractionCreate, network string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Verification Unavailable",
		Description: fmt.Sprintf("Could not verify that account on **%s** right now. Please try again in a few minutes.", network),
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRenameAccountNotFoundEmbed(i *discordgo.InteractionCreate, username, network string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Account Not Found",
		Description: fmt.Sprintf("Could not find a Minecraft account named **%s** on **%s**. Double-check the spelling and try again.", username, network),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerRenameConflictEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Username Taken",
		Description: fmt.Sprintf("The username **%s** is already linked to another player in this guild. Try a different username or contact staff if this is your account.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerRenameSuccessEmbed(i *discordgo.InteractionCreate, oldUsername, newUsername string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Username Updated",
		Description: fmt.Sprintf(
			"Your linked Minecraft username has been updated. Please wait a moment for your display name to update.\n\n> **Previous Username:** `%s`\n> **New Username:** `%s`",
			oldUsername, newUsername,
		),
		Thumbnail: MinecraftBustAvatarURL(newUsername),
	})
}

// ============================================================================
// general/player_screenshare.go
// ============================================================================

func PlayerScreenshareSubmittedEmbed(i *discordgo.InteractionCreate, target *discordgo.User, username, reason string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Your screenshare request has been submitted. Staff will review the request and contact you shortly.\n\n> **Player:** <@%s> (**%s**)",
		target.ID, username,
	)
	if reason != "" {
		description += fmt.Sprintf("\n> **Reason:** %s", reason)
	}

	return Preset("success", types.PresetOptions{
		Title:       "Screenshare Request Submitted",
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerScreenshareDatabaseUnavailableEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to submit the screenshare request because the database service is unavailable. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerScreenshareInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to request a screenshare for. Select a member from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerScreenshareBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated application bot profiles cannot be screenshared. Target a real player instead.",
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// general/player_settings.go
// ============================================================================

func SettingsStateLabel(enabled bool) string {
	if enabled {
		return "Enabled"
	}
	return "Disabled"
}

func UnexpectedErrorPlayerSettingsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while updating your settings. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerSettingsInvalidSubcommandEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "Use `/settings view` or `/settings toggle`.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerSettingsRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register before you can manage your settings. Run `/register` to link your Minecraft account first.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerSettingsUnknownOptionEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unknown Setting",
		Description: "That is not a setting you can change. Pick one of the options listed in the command.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerSettingsOverviewEmbed(i *discordgo.InteractionCreate, config *models.PlayerConfigsModel) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Player Settings — %s", config.Username),
		Description: "Your current ranked profile preferences. Use `/settings toggle` to change one.",
		Thumbnail:   MinecraftBustAvatarURL(config.Username),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "ELO Visibility", Value: fmt.Sprintf("**%s**", SettingsStateLabel(config.IsEloVisible)), Inline: true},
			{Name: "Stats Visibility", Value: fmt.Sprintf("**%s**", SettingsStateLabel(config.IsStatsVisible)), Inline: true},
			{Name: "Party Invites", Value: fmt.Sprintf("**%s**", SettingsStateLabel(config.IsPartyInvitesEnabled)), Inline: true},
			{Name: "Party Autowarp", Value: fmt.Sprintf("**%s**", SettingsStateLabel(config.IsPartyAutowarpEnabled)), Inline: true},
			{Name: "Ping On Results", Value: fmt.Sprintf("**%s**", SettingsStateLabel(config.IsPingOnResultsEnabled)), Inline: true},
			{Name: "Ping On Alerts", Value: fmt.Sprintf("**%s**", SettingsStateLabel(config.IsPingOnAlertsEnabled)), Inline: true},
		},
	})
}

func PlayerSettingsToggledEmbed(i *discordgo.InteractionCreate, username, label string, enabled bool) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Setting Updated",
		Description: fmt.Sprintf("Successfully updated your settings.\n\n> **Setting:** `%s`\n> **State:** `%s`", label, SettingsStateLabel(enabled)),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/player_stats.go
// ============================================================================

func UnexpectedErrorPlayerStatsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the seasonal statistics for this player. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerStatsInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a user to view stats for. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerStatsBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have stats. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerStatsNotRegisteredEmbed(i *discordgo.InteractionCreate, user *discordgo.User) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: fmt.Sprintf("<@%s> is not registered in this guild. They must run `/register` before stats are available.", user.ID),
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func PlayerStatsPrivateEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Stats Private",
		Description: fmt.Sprintf("<@%s> has their stats set to private. Only they can view their own stats.", user.ID),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerStatsNoActiveSeasonEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: fmt.Sprintf("This guild does not have an active season configured for **%s**. Please contact an administrator for assistance.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerStatsOverviewEmbed(i *discordgo.InteractionCreate, username string, stats *models.PlayerStatsModel) *discordgo.MessageEmbed {
	totalGames := stats.Wins + stats.Losses
	winRate := 0.0
	if totalGames > 0 {
		winRate = float64(stats.Wins) / float64(totalGames) * 100
	}
	kdr := 0.0
	if stats.Deaths > 0 {
		kdr = float64(stats.Kills) / float64(stats.Deaths)
	}

	description := fmt.Sprintf(
		"Seasonal statistics for **%s**.\n\n> **ELO:** `%d`\n> **Rank:** `%s`\n> **Wins:** `%d`\n> **Losses:** `%d`\n> **Win Rate:** `%.1f%%`\n> **Kills:** `%d`\n> **Deaths:** `%d`\n> **KDR:** `%.2f`\n> **Beds Destroyed:** `%d`\n> **Final Kills:** `%d`",
		username, stats.Elo, stats.Rank, stats.Wins, stats.Losses, winRate, stats.Kills, stats.Deaths, kdr, stats.BedsDestroyed, stats.FinalKills,
	)

	return Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Player Stats — %s", username),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/player_sync.go
// ============================================================================

func UnexpectedErrorPlayerSyncEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while synchronizing your profile. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerSyncRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register before you can refresh your profile. Run `/register` to link your Minecraft account first.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerSyncMemberNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Member Not Found",
		Description: "Your guild membership could not be found. Verify you are still a member of this guild and try again.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerSyncUpToDateEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Profile Synchronized",
		Description: fmt.Sprintf("Your profile for **%s** is already up to date. No further changes are needed.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerSyncSuccessEmbed(i *discordgo.InteractionCreate, username string, actions []string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Account Synchronized",
		Description: fmt.Sprintf(
			"Successfully refreshed your roles and nickname.\n\n> **Username:** `%s`\n\n%s",
			username, strings.Join(actions, "\n"),
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/queue_call.go
// ============================================================================

func UnexpectedErrorQueueCallEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to add the player to your team voice channel. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to add to your team voice channel. Select a member from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallSelfTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You cannot add yourself to your own team voice channel. Target another player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be added to voice channels. Target a real player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallNotInVoiceEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in Voice",
		Description: "You must be connected to your team voice channel to use this command. Join it and try again.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallNotInGameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in Game",
		Description: "The voice channel you are in does not belong to an active game. Join your team voice channel and try again.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallNotInMatchEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in Match",
		Description: "You are not a participant in this game, so you cannot add players to its voice channels.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallAlreadyInMatchEmbed(i *discordgo.InteractionCreate, target *discordgo.User) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already in Match",
		Description: fmt.Sprintf("<@%s> is already a participant in this game and cannot be added again.", target.ID),
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallWrongChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Wrong Channel",
		Description: "You are not in your assigned team voice channel. Join it and try again.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallMissingPermissionsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Missing Permissions",
		Description: "The bot does not have permission to manage permissions in this voice channel. Please contact an administrator for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallSuccessEmbed(i *discordgo.InteractionCreate, target *discordgo.User, gameNumber int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Voice Access Granted",
		Description: fmt.Sprintf("Successfully added the player to your team voice channel.\n\n> **Player:** <@%s>\n> **Game:** `#%d`", target.ID, gameNumber),
		Thumbnail:   ThumbPlayers,
	})
}

// ============================================================================
// general/queue_lfg.go
// ============================================================================

func UnexpectedErrorQueueLfgEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to post your looking-for-group request. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueLfgInvalidUserEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid User",
		Description: "Your user account could not be identified. Please try again later or contact an administrator for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueLfgNotConfiguredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "LFG Not Configured",
		Description: "The looking-for-group system is not configured in this guild. Please contact an administrator for assistance to configure the LFG channel and role settings.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueLfgQueuesPausedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Queues Paused",
		Description: "Looking-for-group posting is currently paused by an administrator. Please try again later when queues are reopened.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueLfgNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register with the ranked system to use the looking-for-group feature. Use `/register` to link your account and track your seasonal progress.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueLfgSuccessEmbed(i *discordgo.InteractionCreate, username, channelID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "LFG Posted",
		Description: fmt.Sprintf("Your looking-for-group post has been sent. Good luck finding a game!\n\n> **Player:** `%s`\n> **Channel:** <#%s>", username, channelID),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func QueueLfgPostEmbed(i *discordgo.InteractionCreate, username, userID string) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Looking for Group",
		Description: fmt.Sprintf("<@%s> (**%s**) is looking for a ranked game! React or message them to join their party.", userID, username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/queue_pick.go
// ============================================================================

func QueuePickNotGameChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not a Game Channel",
		Description: "This command can only be used in a game text channel. Run it in the text channel of the game you are drafting for.",
		Thumbnail:   ThumbFlag,
	})
}

func QueuePickNoActiveDraftEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Draft",
		Description: "There is no active captain draft for this game. If you think this is an error, please contact an administrator for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

func QueuePickDraftCompleteEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Draft Complete",
		Description: "The captain draft for this game is already complete. No more players can be picked.",
		Thumbnail:   ThumbFlag,
	})
}

func QueuePickMissingOptionEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Missing Option",
		Description: "You must provide either a player mention or a username to pick.",
		Thumbnail:   ThumbFlag,
	})
}

func QueuePickPlayerNotFoundEmbed(i *discordgo.InteractionCreate, name string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: fmt.Sprintf("Could not find a player in the pick pool matching **%s**. Check the spelling or mention the player instead.", name),
		Thumbnail:   ThumbFlag,
	})
}

func QueuePickInvalidSelectionEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Selection",
		Description: "The selected player is not in the available pick pool. Choose a player who has not been picked yet.",
		Thumbnail:   ThumbFlag,
	})
}

func QueuePickSuccessEmbed(i *discordgo.InteractionCreate, username, captainID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Player Picked",
		Description: fmt.Sprintf("Successfully drafted the player onto the team.\n\n> **Player:** `%s`\n> **Captain:** <@%s>", username, captainID),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/queue_status.go
// ============================================================================

func UnexpectedErrorQueueStatusEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the game or queue information. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueStatusNoGameOrQueueEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Game or Queue",
		Description: "Use this command in a game text channel to view game stats, or join a queue voice channel to check the queue status.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueStatusNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. Please contact an administrator to configure a season.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueStatusNotQueueChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not a Queue Channel",
		Description: "This voice channel is not configured as a queue channel. Join a queue voice channel to check its status.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueStatusClosedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Queues Closed",
		Description: "Ranked queues are currently **closed** by staff.\n-# Games will not start until queues are opened again.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueStatusGameStatsEmbed(i *discordgo.InteractionCreate, game Game, season string, fields []*discordgo.MessageEmbedField) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Game Stats · #%d", game.Number),
		Description: fmt.Sprintf("> **Mode:** `%s`\n> **Season:** `%s`", game.Mode.Name, season),
		Fields:      fields,
		Thumbnail:   ThumbPlayers,
	})
}

func QueueStatusQueueEmbed(i *discordgo.InteractionCreate, modeName, description string, fields []*discordgo.MessageEmbedField) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Queue Status · %s", modeName),
		Description: description,
		Fields:      fields,
		Thumbnail:   ThumbPlayers,
	})
}

// ============================================================================
// general/strike_list.go
// ============================================================================

func UnexpectedErrorStrikeListEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the strike history. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeListBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have a strike history. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeListNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register with `/register` to link a Minecraft account first.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func StrikeListEmptyEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Strikes Clear",
		Description: "No strike history was found for this player. This player maintains a clean record.",
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func StrikeListEmptyFilteredEmbed(i *discordgo.InteractionCreate, username, filter string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Strikes Empty",
		Description: fmt.Sprintf("No strike history was found for this player in the last **%s**. Try a longer filter or remove it.", filter),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/strike_request.go
// ============================================================================

func UnexpectedErrorStrikeRequestEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while submitting the strike review request. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeRequestInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to request a strike review for. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeRequestBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have strikes to review. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeRequestReasonRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Reason Required",
		Description: "You must explain why the strike should be reviewed. Include a clear explanation in the reason field.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeRequestNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can have a strike review requested.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func StrikeRequestSubmittedEmbed(i *discordgo.InteractionCreate, target *discordgo.User, username, reason string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Strike Review Requested",
		Description: fmt.Sprintf(
			"Your strike review request has been submitted. Staff will review the strike and respond on this ticket.\n\n> **Player:** <@%s> (**%s**)\n> **Reason:** %s",
			target.ID, username, reason,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/game_force_void.go
// ============================================================================

func GameForceVoidNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func GameForceVoidSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int, reason string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Game Force Voided",
		Description: fmt.Sprintf("Game **#%d** has been force voided. This action has been logged for staff review.\n\n> **Reason:** %s", gameNumber, reason),
		Thumbnail:   ThumbTrashCan,
	})
}

// ============================================================================
// staff/game_host.go
// ============================================================================

func GameHostNotFoundEmbed(i *discordgo.InteractionCreate, number int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Hosted Game Not Found",
		Description: fmt.Sprintf("No live hosted game **#%d** was found. Use `/host list` to see the running ones.", number),
		Thumbnail:   ThumbFlag,
	})
}

func GameHostListEmbed(i *discordgo.InteractionCreate, games []Game) *discordgo.MessageEmbed {
	if len(games) == 0 {
		return Preset("info", types.PresetOptions{
			Title:       "Hosted Games",
			Description: "No hosted games are running. Start one with `/host create`.",
			Thumbnail:   ThumbDoubleSwords,
		})
	}
	lines := make([]string, len(games))
	for i, g := range games {
		sizes := make([]string, len(g.Custom.Teams))
		for t, team := range g.Custom.Teams {
			sizes[t] = strconv.Itoa(len(team))
		}
		lines[i] = fmt.Sprintf("> `#%d` · `%s` · `%s` · `%s` · ranks %s · stats %s · <@%s>", g.Number, g.Mode.Name, strings.Join(sizes, "v"), g.State, HostOnOff(g.Custom.Ranked), HostOnOff(g.Custom.Stats), g.Custom.HostedBy)
	}
	return Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Hosted Games (%d)", len(games)),
		Description: strings.Join(lines, "\n"),
		Thumbnail:   ThumbDoubleSwords,
	})
}

func GameHostViewEmbed(i *discordgo.InteractionCreate, g Game) *discordgo.MessageEmbed {
	var fields []*discordgo.MessageEmbedField
	for t, team := range g.Custom.Teams {
		value := "<@" + strings.Join(team, "> <@") + ">"
		if name := TeamName(t); g.Channels.Voice[name] != "" {
			value += "\n<#" + g.Channels.Voice[name] + ">"
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: TeamName(t), Value: value, Inline: true})
	}
	hosting := "by hand"
	if g.Bot != "" {
		hosting = "`" + g.Bot + "`"
	}
	return Preset("info", types.PresetOptions{
		Title: fmt.Sprintf("Hosted Game #%d", g.Number),
		Description: fmt.Sprintf("> **Mode:** `%s`\n> **State:** `%s`\n> **Map:** `%s`\n> **Ranks:** %s\n> **Stats:** %s\n> **Hosted:** %s\n> **By:** <@%s>",
			g.Mode.Name, g.State, g.Map, HostOnOff(g.Custom.Ranked), HostOnOff(g.Custom.Stats), hosting, g.Custom.HostedBy),
		Fields:    fields,
		Thumbnail: ThumbDoubleSwords,
	})
}

// ============================================================================
// staff/game_loss.go
// ============================================================================

func UnexpectedErrorGameLossEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while removing ELO from the player. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func GameLossInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to remove ELO from. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func GameLossBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive ELO removals. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func GameLossRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register them with `/forceregister` before removing ELO.",
		Thumbnail:   ThumbFlag,
	})
}

func GameLossNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. ELO can only be removed during an active season. Please contact an administrator to configure a season.",
		Thumbnail:   ThumbFlag,
	})
}

func GameLossSuccessEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string, lossCount, totalEloLoss, previousElo, newElo int, oldRank, newRank string, countStats bool, lossDetails []string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully removed ELO from <@%s> (**%s**). This action has been logged for staff review.\n\n> **Losses Applied:** `%d`\n> **Total ELO Loss:** `-%d`\n> **Previous ELO:** `%d`\n> **New ELO:** `%d`\n> **Previous Rank:** `%s`\n> **New Rank:** `%s`\n> **Counted Towards Stats:** `%t`",
		user.ID, username, lossCount, totalEloLoss, previousElo, newElo, oldRank, newRank, countStats,
	)

	// Add loss breakdown if there are rank changes
	if len(lossDetails) > 0 {
		description += "\n\n__**ELO Breakdown:**__\n"
		for _, detail := range lossDetails {
			description += fmt.Sprintf("> %s\n", detail)
		}
	}

	return Preset("success", types.PresetOptions{
		Title:       "ELO Removed",
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/game_modify.go
// ============================================================================

func UnexpectedErrorGameModifyEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while modifying the player's statistics. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func GameModifyInvalidSubcommandEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "You must specify a subcommand: `add`, `remove`, or `set`. Select one from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func GameModifyInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to modify. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func GameModifyBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot have their statistics modified. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func GameModifyUnknownStatEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unknown Statistic",
		Description: "That is not a valid statistic that can be modified. Please select a valid statistic from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func GameModifyRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register them with `/forceregister` before modifying their statistics.",
		Thumbnail:   ThumbFlag,
	})
}

func GameModifyNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. Statistics can only be modified during an active season. Please contact an administrator to configure a season.",
		Thumbnail:   ThumbFlag,
	})
}

func GameModifySuccessEmbed(i *discordgo.InteractionCreate, statLabel string, user *discordgo.User, username, operation string, newVal, currentVal int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Statistics Modified",
		Description: fmt.Sprintf(
			"Successfully adjusted the player's statistics. This action has been logged for staff review.\n\n> **Player:** <@%s> (**%s**)\n> **Statistic:** `%s`\n> **Operation:** `%s`\n> **Previous Value:** `%d`\n> **New Value:** `%d`",
			user.ID, username, statLabel, operation, currentVal, newVal,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/game_partial_void.go
// ============================================================================

func GamePartialVoidNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func GamePartialVoidSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int, loserIDs []string) *discordgo.MessageEmbed {
	mentions := make([]string, len(loserIDs))
	for idx, id := range loserIDs {
		mentions[idx] = fmt.Sprintf("<@%s>", id)
	}

	return Preset("success", types.PresetOptions{
		Title:       "Game Partially Voided",
		Description: fmt.Sprintf("Game **#%d** has been partially voided. The winning team's stats are preserved. This action has been logged for staff review.\n\n> **Affected Players:** %s", gameNumber, strings.Join(mentions, " ")),
		Thumbnail:   ThumbTrashCan,
	})
}

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

func GameRescoreBotMvpEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid MVP",
		Description: "Automated bot accounts cannot be the MVP. Select a real player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func GameRescoreNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func GameRescoreInvalidStateEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid State",
		Description: fmt.Sprintf("Game **#%d** has not finished yet. Only completed or voided games can be rescored; use `/score` for games that are still in progress.", gameNumber),
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

func GameRescoreSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int, winners, losers string, mvp *discordgo.User) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Game Rescored",
		Description: fmt.Sprintf("Game **#%d** has been rescored. This action has been logged for staff review.\n\n> **Winners:** %s\n> **Losers:** %s\n> **MVP:** <@%s>", gameNumber, winners, losers, mvp.ID),
		Thumbnail:   ThumbDoubleSwords,
	})
}

// ============================================================================
// staff/game_score.go
// ============================================================================

// SplitResultMentions lists the winners and losers of a scored game as mentions.
func SplitResultMentions(results []EloResult) (winners, losers string) {
	var won, lost []string
	for _, r := range results {
		if r.Win {
			won = append(won, fmt.Sprintf("<@%s>", r.UserID))
		} else {
			lost = append(lost, fmt.Sprintf("<@%s>", r.UserID))
		}
	}
	return strings.Join(won, " "), strings.Join(lost, " ")
}

func GameScoreNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func GameScoreSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int, teamName string, results []EloResult) *discordgo.MessageEmbed {
	winners, losers := SplitResultMentions(results)

	return Preset("success", types.PresetOptions{
		Title:       "Game Scored",
		Description: fmt.Sprintf("Game **#%d** has been scored. This action has been logged for staff review.\n\n> **Winning Team:** `%s`\n> **Winners:** %s\n> **Losers:** %s", gameNumber, teamName, winners, losers),
		Thumbnail:   ThumbDoubleSwords,
	})
}

// ============================================================================
// staff/game_unvoid.go
// ============================================================================

func GameUnvoidNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func GameUnvoidNotVoidedEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Voided",
		Description: fmt.Sprintf("Game **#%d** has not been voided, so there is nothing to restore.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func GameUnvoidSuccessEmbed(i *discordgo.InteractionCreate, gameNumber, restored int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Game Restored",
		Description: fmt.Sprintf("Game **#%d** has been restored from its voided state. This action has been logged for staff review.\n\n> **Players Restored:** `%d`", gameNumber, restored),
		Thumbnail:   ThumbLoad,
	})
}

// ============================================================================
// staff/game_win.go
// ============================================================================

func UnexpectedErrorGameWinEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while granting ELO to the player. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func GameWinInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to grant ELO to. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func GameWinBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive ELO grants. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func GameWinRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register them with `/forceregister` before granting ELO.",
		Thumbnail:   ThumbFlag,
	})
}

func GameWinNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. ELO can only be granted during an active season. Please contact an administrator to configure a season.",
		Thumbnail:   ThumbFlag,
	})
}

func GameWinSuccessEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string, winCount, totalEloGain, previousElo, newElo int, oldRank, newRank string, countStats bool, winDetails []string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully granted ELO to <@%s> (**%s**). This action has been logged for staff review.\n\n> **Wins Granted:** `%d`\n> **Total ELO Gain:** `+%d`\n> **Previous ELO:** `%d`\n> **New ELO:** `%d`\n> **Previous Rank:** `%s`\n> **New Rank:** `%s`\n> **Counted Towards Stats:** `%t`",
		user.ID, username, winCount, totalEloGain, previousElo, newElo, oldRank, newRank, countStats,
	)

	// Add win breakdown if there are rank changes
	if len(winDetails) > 0 {
		description += "\n\n__**ELO Breakdown:**__\n"
		for _, detail := range winDetails {
			description += fmt.Sprintf("> %s\n", detail)
		}
	}

	return Preset("success", types.PresetOptions{
		Title:       "ELO Granted",
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/player_force_nickname.go
// ============================================================================

func UnexpectedErrorPlayerForceNicknameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while force-updating the player's nickname. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceNicknameInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to update. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceNicknameRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register them with `/forceregister` first.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceNicknameProfileNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Profile Not Found",
		Description: "That player's guild profile could not be found. Run `/forcesync` or contact staff if the issue persists.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceNicknameNoneActiveEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Nickname Set",
		Description: "That player does not have an active nickname to remove. Provide a nickname value to assign one.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceNicknameInvalidUsernameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Nickname",
		Description: "That nickname format is not valid. Nicknames must be 3-16 characters and contain only letters, numbers, and underscores.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceNicknameAlreadySetEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Nickname Already Set",
		Description: fmt.Sprintf("That player's nickname is already set to **%s**. No further action is needed.", nickname),
		Thumbnail:   MinecraftBustAvatarURL(nickname),
	})
}

func PlayerForceNicknameConflictEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Nickname Taken",
		Description: fmt.Sprintf("The nickname **%s** is already in use in this guild. Try a different nickname.", nickname),
		Thumbnail:   MinecraftBustAvatarURL(nickname),
	})
}

func PlayerForceNicknameRemovedEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Nickname Cleared",
		Description: fmt.Sprintf("Successfully cleared the player's in-game nickname.\n\n> **User:** <@%s>\n> **Username:** `%s`", user.ID, username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerForceNicknameUpdatedEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username, nickname string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Nickname Force-Updated",
		Description: fmt.Sprintf(
			"Successfully updated the player's in-game nickname. This change may take a moment to propagate across all systems.\n\n> **User:** <@%s>\n> **Username:** `%s`\n> **Nickname:** `%s`",
			user.ID, username, nickname,
		),
		Thumbnail: MinecraftBustAvatarURL(nickname),
	})
}

// ============================================================================
// staff/player_force_register.go
// ============================================================================

func UnexpectedErrorPlayerForceRegisterEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while processing the force registration. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRegisterInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a user to register. Select a Discord member from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRegisterBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be registered. Target a real Discord member instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRegisterInvalidUsernameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Username",
		Description: "That Minecraft username format is not valid. Usernames must be 3-16 characters and contain only letters, numbers, and underscores.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRegisterAlreadyRegisteredEmbed(i *discordgo.InteractionCreate, existingUsername string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Registered",
		Description: fmt.Sprintf("That user is already registered as **%s**. No further action is needed.", existingUsername),
		Thumbnail:   MinecraftBustAvatarURL(existingUsername),
	})
}

func PlayerForceRegisterNoRanksEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Ranks Configured",
		Description: "This guild has no ranks configured, so the player cannot be given a starting rank or rank role. Configure at least one rank first.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRegisterConflictEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Username Taken",
		Description: fmt.Sprintf("The username **%s** is already linked to another player in this guild.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerForceRegisterSuccessEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Force Registration Complete",
		Description: fmt.Sprintf(
			"Successfully linked the player's account. This change may take a moment to propagate across all systems.\n\n> **User:** <@%s>\n> **Username:** `%s`",
			user.ID, username,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/player_force_rename.go
// ============================================================================

func UnexpectedErrorPlayerForceRenameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while force-renaming the player. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRenameInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to rename. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRenameInvalidUsernameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Username",
		Description: "That Minecraft username format is not valid. Usernames must be 3-16 characters and contain only letters, numbers, and underscores.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRenameRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can be force-renamed.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRenameProfileNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Profile Not Found",
		Description: "That player's guild profile could not be found. Run `/forcesync` or contact staff if the issue persists.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRenameAlreadyLinkedEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Linked",
		Description: fmt.Sprintf("That account is already linked to **%s**. No further action is needed.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerForceRenameConflictEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Username Taken",
		Description: fmt.Sprintf("The username **%s** is already linked to another player in this guild. Try a different username.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerForceRenameSuccessEmbed(i *discordgo.InteractionCreate, user *discordgo.User, oldUsername, newUsername string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Username Force-Updated",
		Description: fmt.Sprintf(
			"Successfully updated the player's linked username. This change may take a moment to propagate across all systems.\n\n> **User:** <@%s>\n> **Previous Username:** `%s`\n> **New Username:** `%s`",
			user.ID, oldUsername, newUsername,
		),
		Thumbnail: MinecraftBustAvatarURL(newUsername),
	})
}

// ============================================================================
// staff/player_force_sync.go
// ============================================================================

func UnexpectedErrorPlayerForceSyncEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while force-synchronizing the player's profile. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceSyncInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to sync. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceSyncBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be force-synced. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceSyncRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register them with `/forceregister` first.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceSyncMemberNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Member Not Found",
		Description: "Could not find that player's guild membership to sync. Verify they are still a member of this guild.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceSyncUpToDateEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Profile Synchronized",
		Description: fmt.Sprintf("The profile for **%s** is already up to date. No further changes are needed.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerForceSyncSuccessEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string, actions []string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Profile Force-Synchronized",
		Description: fmt.Sprintf(
			"Successfully refreshed the player's roles and nickname. Run `/forcesync` again anytime their roles or nickname need refreshing.\n\n> **User:** <@%s>\n> **Username:** `%s`\n\n%s",
			user.ID, username, strings.Join(actions, "\n"),
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/player_force_transfer.go
// ============================================================================

func UnexpectedErrorPlayerForceTransferEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while transferring the account link. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceTransferInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify both the current linked user and the destination user.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceTransferSameUserEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "The source and destination users must be different Discord accounts.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceTransferSourceNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "The source user is not registered in this guild. Only registered accounts can be transferred.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceTransferSourceProfileNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Profile Not Found",
		Description: "The source user's guild profile could not be found. Run `/forcesync` or contact staff if the issue persists.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceTransferDestinationAlreadyRegisteredEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Registered",
		Description: fmt.Sprintf("<@%s> is already linked to **%s**. Unregister them first or choose a different target user.", user.ID, username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerForceTransferConflictEmbed(i *discordgo.InteractionCreate, user *discordgo.User) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Registered",
		Description: fmt.Sprintf("<@%s> already has a linked player record, so the transfer cannot overwrite it.", user.ID),
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceTransferSuccessEmbed(i *discordgo.InteractionCreate, oldUser, newUser *discordgo.User, username string, actions []string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully transferred the linked profile. This change may take a moment to propagate across all systems.\n\n> **From:** <@%s>\n> **To:** <@%s>\n> **Username:** `%s`",
		oldUser.ID, newUser.ID, username,
	)
	if len(actions) > 0 {
		description += fmt.Sprintf("\n\n**Actions Taken:**\n%s", strings.Join(actions, "\n"))
	}

	return Preset("success", types.PresetOptions{
		Title:       "Account Transferred",
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/player_unregister.go
// ============================================================================

func UnexpectedErrorPlayerUnregisterEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while unregistering the player. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerUnregisterInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to unregister. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerUnregisterBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be unregistered. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerUnregisterNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can be unregistered.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func PlayerUnregisterSuccessEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Player Unregistered",
		Description: fmt.Sprintf(
			"Successfully removed the player's registration. Their seasonal stats and profile data for this guild have been cleared.\n\n> **Player:** <@%s>\n> **Username:** `%s`",
			user.ID, username,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/player_unwipe.go
// ============================================================================

func UnexpectedErrorPlayerUnwipeEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while restoring the stats. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerUnwipeInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to unwipe. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerUnwipeBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have stats to restore. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerUnwipeNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can have stats restored.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func PlayerUnwipeNothingEmbed(i *discordgo.InteractionCreate, username string, wipeID int) *discordgo.MessageEmbed {
	description := fmt.Sprintf("**%s** has no wipe to undo.", username)
	if wipeID > 0 {
		description = fmt.Sprintf("**%s** has no wipe with ID **%d**.", username, wipeID)
	}
	return Preset("error", types.PresetOptions{
		Title:       "No Wipe Found",
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerUnwipeAlreadyEmbed(i *discordgo.InteractionCreate, username string, wipeID uint) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Restored",
		Description: fmt.Sprintf("Wipe **%d** of **%s** was already undone.", wipeID, username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerUnwipeSuccessEmbed(i *discordgo.InteractionCreate, username string, r *sdk.WipeResult, rank string) *discordgo.MessageEmbed {
	scope := "All stats this season"
	if r.Wipe.FromGameNumber > 0 {
		scope = fmt.Sprintf("Game **#%d** onwards (**%d** games)", r.Wipe.FromGameNumber, r.Wipe.GamesAffected)
	}
	return Preset("success", types.PresetOptions{
		Title: "Stats Restored",
		Description: fmt.Sprintf(
			"Successfully undid the wipe. This action has been logged for staff review.\n\n> **Player:** `%s`\n> **Restored:** %s\n> **ELO:** `%d`\n> **Rank:** `%s`\n> **Wipe ID:** `%d`",
			username, scope, r.Stats.Elo, rank, r.Wipe.ID,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/player_verify.go
// ============================================================================

func UnexpectedErrorPlayerVerifyEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while verifying the player. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerVerifyInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to verify. Select a Discord member from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerVerifyBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be verified. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerVerifyNotRegisteredEmbed(i *discordgo.InteractionCreate, user *discordgo.User) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: fmt.Sprintf("<@%s> is not registered in this guild, so they cannot queue. Register them with `/forceregister`.", user.ID),
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func PlayerVerifyReportEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string, ageDays int, whitelisted bool, restrictions []string, inGame bool, problems []string) *discordgo.MessageEmbed {
	preset, verdict := "success", "Eligible"
	if len(problems) > 0 {
		preset, verdict = "error", "Not Eligible"
	}

	age := fmt.Sprintf("**%d** days", ageDays)
	if whitelisted {
		age += " (whitelisted)"
	}
	active := "`None`"
	if len(restrictions) > 0 {
		active = "`" + strings.Join(restrictions, "`, `") + "`"
	}
	state := "`Available`"
	if inGame {
		state = "`In a game`"
	}

	description := fmt.Sprintf(
		"Eligibility report for <@%s>.\n\n> **Username:** `%s`\n> **Verdict:** `%s`\n> **Account Age:** %s\n> **Active Punishments:** %s\n> **Game State:** %s",
		user.ID, username, verdict, age, active, state,
	)
	if len(problems) > 0 {
		description += "\n\n**Blocking:**\n> " + strings.Join(problems, "\n> ")
	}

	return Preset(preset, types.PresetOptions{
		Title:       fmt.Sprintf("Player Verification — %s", username),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/player_wipe.go
// ============================================================================

func UnexpectedErrorPlayerWipeEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while wiping the stats. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerWipeInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to wipe. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerWipeBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot have stats wiped. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerWipeNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players have seasonal stats to wipe.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func PlayerWipeNoSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. An administrator should configure a season first.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerWipeNoStatsEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Stats Found",
		Description: fmt.Sprintf("**%s** does not have stats for the active season. The player may need to play a game first.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerWipeNoGamesEmbed(i *discordgo.InteractionCreate, username string, fromGame int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Games To Wipe",
		Description: fmt.Sprintf("**%s** has no scored games from game **#%d** onwards this season, so there is nothing to wipe.", username, fromGame),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerWipeSuccessEmbed(i *discordgo.InteractionCreate, username, season string, fromGame int, r *sdk.WipeResult, rank string) *discordgo.MessageEmbed {
	scope := "All of the player's stats this season"
	if fromGame > 0 {
		scope = fmt.Sprintf("Game **#%d** onwards (**%d** games)", fromGame, r.Wipe.GamesAffected)
	}
	return Preset("success", types.PresetOptions{
		Title: "Stats Wiped",
		Description: fmt.Sprintf(
			"Successfully wiped the player's seasonal stats. This action has been logged for staff review. Use `/unwipe` to undo it.\n\n> **Player:** `%s`\n> **Season:** `%s`\n> **Wiped:** %s\n> **ELO:** `%d` → `%d`\n> **Rank:** `%s`\n> **Wipe ID:** `%d`",
			username, season, scope, r.Wipe.EloBefore, r.Stats.Elo, rank, r.Wipe.ID,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/punishment_clear.go
// ============================================================================

func UnexpectedErrorPunishmentClearEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while clearing the punishment. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PunishmentClearInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to clear a punishment from. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PunishmentClearBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive punishments. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PunishmentClearNoneActiveEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Punishments",
		Description: "This player has no active punishments to clear. Their history can be reviewed with `/punishments`.",
		Thumbnail:   ThumbFlag,
	})
}

func PunishmentClearNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Punishment Not Found",
		Description: "No punishment with that ID exists for this player in this guild. Double-check the ID from `/punishments`.",
		Thumbnail:   ThumbFlag,
	})
}

func PunishmentClearSuccessEmbed(i *discordgo.InteractionCreate, id uint, punishType string, user *discordgo.User) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Punishment Cleared",
		Description: fmt.Sprintf(
			"Successfully removed the punishment record. This action has been logged for staff review.\n\n> **Punishment ID:** `#%d`\n> **Player:** <@%s>\n> **Type:** `%s`",
			id, user.ID, punishType,
		),
		Thumbnail: ThumbHammer,
	})
}

// ============================================================================
// staff/queue_substitutes.go
// ============================================================================

func QueueSubstitutesInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify both the player leaving and the substitute taking their place.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueSubstitutesBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot play in games. Select real players instead.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueSubstitutesSamePlayerEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "The outgoing player and the substitute must be different players.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueSubstitutesGameNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func QueueSubstitutesSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int, outgoing, incoming *discordgo.User) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Substitute Assigned",
		Description: fmt.Sprintf(
			"Successfully swapped the players. This action has been logged for staff review.\n\n> **Game:** `#%d`\n> **Leaving:** <@%s>\n> **Substitute:** <@%s>",
			gameNumber, outgoing.ID, incoming.ID,
		),
		Thumbnail: ThumbPlayers,
	})
}

// ============================================================================
// staff/server_clear.go
// ============================================================================

func UnexpectedErrorClearEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while clearing messages. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func ClearMissingPermissionsEmbed(i *discordgo.InteractionCreate, channelID string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Missing Permissions",
		Description: fmt.Sprintf("I do not have permission to manage messages in <#%s>. Grant **Manage Messages** to the bot role and try again.", channelID),
		Thumbnail:   ThumbFlag,
	})
}

func ClearNoMessagesEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Messages Found",
		Description: "No messages matched your filters in that channel. Adjust the filters or clear a different channel.",
		Thumbnail:   ThumbFlag,
	})
}

func ClearPartialEmbed(i *discordgo.InteractionCreate, channelID string, deleted, skipped int) *discordgo.MessageEmbed {
	return Preset("caution", types.PresetOptions{
		Title: "Messages Partially Cleared",
		Description: fmt.Sprintf(
			"Successfully removed **%d** message(s) from <#%s>. **%d** message(s) older than 14 days were skipped. Re-run with `override_14days: True` to delete them individually.",
			deleted, channelID, skipped,
		),
		Thumbnail: ThumbTrashCan,
	})
}

func ClearSuccessEmbed(i *discordgo.InteractionCreate, channelID string, deleted int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Messages Cleared",
		Description: fmt.Sprintf("Successfully removed **%d** message(s) from <#%s>. Deleted messages cannot be recovered.", deleted, channelID),
		Thumbnail:   ThumbTrashCan,
	})
}

// ============================================================================
// staff/server_echo.go
// ============================================================================

func UnexpectedErrorEchoEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while processing the echo command. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func EchoSuccessEmbed(i *discordgo.InteractionCreate, channelID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Message Dispatched",
		Description: fmt.Sprintf(
			"Your message was sent successfully. The message is now visible in the target channel <#%s>.",
			channelID,
		),
		Thumbnail: ThumbGuild,
	})
}

func EchoInvalidLinkEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Link",
		Description: "That does not look like a valid Discord message link. Right-click a message and choose **Copy Message Link**.",
		Thumbnail:   ThumbFlag,
	})
}

func EchoRestrictedDestinationEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Restricted Destination",
		Description: "That message link points outside this server. Only links from this guild can be used.",
		Thumbnail:   ThumbFlag,
	})
}

func EchoInvalidChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Channel",
		Description: "The selected channel could not be used. Choose a text channel the bot can access.",
		Thumbnail:   ThumbFlag,
	})
}

func EchoInsufficientPermissionsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Insufficient Permissions",
		Description: "The bot does not have permission to send messages in that channel. Grant **Send Messages** to the bot role and try again.",
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// staff/server_role.go
// ============================================================================

func UnexpectedErrorRoleEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while modifying member roles. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func RoleInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "That member could not be updated. Verify the user is in this server and try again.",
		Thumbnail:   ThumbFlag,
	})
}

func RoleMemberNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Member Not Found",
		Description: "That member is not in this server. They may have left or been removed.",
		Thumbnail:   ThumbFlag,
	})
}

func RoleNoMembersEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Members Found",
		Description: "You must provide at least one valid user mention, snowflake ID, or target role filter.",
		Thumbnail:   ThumbFlag,
	})
}

func RoleAlreadyAssignedEmbed(i *discordgo.InteractionCreate, user *discordgo.User, roleName string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Role Already Assigned",
		Description: fmt.Sprintf("<@%s> already has the **%s** role. No further action is needed.", user.ID, roleName),
		Thumbnail:   ThumbFlag,
	})
}

func RoleNotAssignedEmbed(i *discordgo.InteractionCreate, user *discordgo.User, roleName string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Role Not Assigned",
		Description: fmt.Sprintf("<@%s> does not currently have the **%s** role. Use `/role list` to view their current roles.", user.ID, roleName),
		Thumbnail:   ThumbFlag,
	})
}

func RoleUpdatedEmbed(i *discordgo.InteractionCreate, action string, user *discordgo.User, roleName string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Role Updated",
		Description: fmt.Sprintf(
			"Successfully updated the member's roles. Changes take effect immediately.\n\n> **Role:** `%s`\n> **Action:** %s\n> **Member:** <@%s>",
			roleName, action, user.ID,
		),
		Thumbnail: ThumbGuild,
	})
}

func RoleMassResultEmbed(i *discordgo.InteractionCreate, roleName, action string, success, skipped int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Bulk Role Update",
		Description: fmt.Sprintf(
			"Completed the bulk role operation. Skipped members may lack permissions or have hierarchy conflicts.\n\n> **Role:** `%s`\n> **Action:** %s\n> **Updated:** `%d` member(s)\n> **Skipped:** `%d`",
			roleName, action, success, skipped,
		),
		Thumbnail: ThumbGuild,
	})
}

func RoleListEmbed(i *discordgo.InteractionCreate, user *discordgo.User, roles string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("Roles — %s", user.Username),
		Description: roles + "\n\nUse `/role add` or `/role remove` to manage roles.",
		Thumbnail:   ThumbGuild,
	})
}

func RoleListEmptyEmbed(i *discordgo.InteractionCreate, user *discordgo.User) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       fmt.Sprintf("Roles — %s", user.Username),
		Description: "It looks like that member has no assignable roles. Roles may be managed by integrations or higher hierarchy.",
		Thumbnail:   ThumbFlag,
	})
}

func RoleSyncEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Role Synchronization Started",
		Description: "Queued background role synchronization for verified server members.",
		Thumbnail:   ThumbGuild,
	})
}

func RoleHierarchyEmbed(i *discordgo.InteractionCreate, issue RoleHierarchyIssue) *discordgo.MessageEmbed {
	description := "That role cannot be managed due to hierarchy or permissions."
	switch issue {
	case RoleHierarchyManaged:
		description = "That role is managed by an integration and cannot be assigned manually."
	case RoleHierarchyDangerous:
		description = "Administrator roles cannot be assigned through this command."
	case RoleHierarchyExecutor:
		description = "Your highest role must be above the target role."
	case RoleHierarchyBot:
		description = "My highest role must be above the target role."
	}
	return Preset("error", types.PresetOptions{
		Title:       "Role Not Manageable",
		Description: description + " Adjust the role hierarchy to resolve this.",
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// staff/server_unban.go
// ============================================================================

func UnexpectedErrorServerUnbanEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while processing the server unban. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func ServerUnbanInvalidUserIDEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid User ID",
		Description: "That is not a valid Discord user ID. Provide the 17-20 digit ID of the user to unban.",
		Thumbnail:   ThumbFlag,
	})
}

func ServerUnbanNotActiveEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Punishment Not Active",
		Description: "That player is not currently **server banned**. Verify their status with `/punishments`.",
		Thumbnail:   ThumbFlag,
	})
}

func ServerUnbanSuccessEmbed(i *discordgo.InteractionCreate, targetID, username, reason string, unbanned bool) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully processed the server unban. This action has been logged for staff review.\n\n> **User ID:** `%s`\n> **Username:** `%s`\n> **Reason:** %s",
		targetID, username, reason,
	)
	if !unbanned {
		description += "\n\nThe Discord unban could not be applied due to missing permissions."
	}
	return Preset("success", types.PresetOptions{
		Title:       "Server Unban Applied",
		Description: description,
		Thumbnail:   ThumbHammer,
	})
}

// ============================================================================
// staff/strike_issue.go
// ============================================================================

func UnexpectedErrorStrikeIssueEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while issuing the strike. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeIssueInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to strike. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeIssueBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive strikes. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeIssueReasonRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Reason Required",
		Description: "You must provide a reason when issuing a strike. Include a clear explanation in the reason field.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeIssueSuccessEmbed(i *discordgo.InteractionCreate, id uint, user *discordgo.User, username, strikeType, reason string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully issued a strike to the player. This action has been logged for staff review.\n\n> **Strike ID:** `#%d`\n> **Player:** <@%s> (**%s**)\n> **Reason:** %s",
		id, user.ID, username, reason,
	)
	if strikeType != "" {
		description += fmt.Sprintf("\n> **Type:** `%s`", strikeType)
	}
	return Preset("success", types.PresetOptions{
		Title:       "Strike Issued",
		Description: description,
		Thumbnail:   ThumbHammer,
	})
}

// ============================================================================
// staff/strike_remove.go
// ============================================================================

func UnexpectedErrorStrikeRemoveEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while voiding the strike. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeRemoveInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to remove a strike from. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeRemoveBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive strikes. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeRemovePlayerNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can receive strikes.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeRemoveNoStrikesEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Strikes Found",
		Description: "This player has no active strikes to void. Their strike history can be reviewed with `/strikes`.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeRemoveNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Strike Not Found",
		Description: "No strike with that ID exists for this player in this guild. Double-check the ID from `/strikes`.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeRemoveAlreadyVoidedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Voided",
		Description: "That strike has already been voided. No further action is needed.",
		Thumbnail:   ThumbFlag,
	})
}

func StrikeRemoveSuccessEmbed(i *discordgo.InteractionCreate, id uint, user *discordgo.User, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Strike Voided",
		Description: fmt.Sprintf(
			"Successfully voided the strike. This action has been logged for staff review.\n\n> **Strike ID:** `#%d`\n> **Player:** <@%s> (**%s**)",
			id, user.ID, username,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/ticket_manage.go
// ============================================================================

func TicketManageInvalidSubcommandEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "That is not a valid ticket action. Choose one of `panel`, `leaderboard`, `stats`, `logs`, `claim`, `close`, `add`, or `remove`.",
		Thumbnail:   ThumbFlag,
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
	LayoutQueuesPlayer      = DefineRepeatedLayout("queues.player_row", "One player of a team or the pool", "> {mention} · ELO: **{elo}**", "mention", "id", "name", "elo", "team")
	LayoutQueuesDraftTitle  = DefineLayout("queues.draft_title", "Draft turn heading", "👑 Draft Turn")
	LayoutQueuesDraftTurn   = DefineLayout("queues.draft_turn", "Draft turn text", "It is **{captain}**'s turn to pick.", "captain", "captain_mention")
	LayoutQueuesGameDetails = DefineLayout("queues.game_details", "Game stats description", "> **Mode:** `{mode}`\n> **Season:** `{season}`", "game", "mode", "season", "players")

	// /queues in a queue voice channel
	LayoutQueuesDetails       = DefineLayout("queues.queue_details", "Queue status description", "> **Channel:** {channel}\n> **Mode:** `{mode}` (`{teams}v{per_team}`)\n> **Total Slots:** `{slots}`\n> **Eligible:** `{eligible}/{present}` in VC", "channel", "mode", "teams", "per_team", "slots", "eligible", "present")
	LayoutQueuesBlockingTitle = DefineLayout("queues.blocking_title", "Not enough players heading", "⛔ Blocking Queue")
	LayoutQueuesBlockingText  = DefineLayout("queues.blocking_text", "Not enough players text", "Not enough eligible players (`{eligible}/{slots}`)", "eligible", "slots")
	LayoutQueuesBlockedTitle  = DefineLayout("queues.blocked_title", "Blocked players heading", "⛔ Blocked Players", "count")
	LayoutQueuesBlockedRow    = DefineRepeatedLayout("queues.blocked_row", "One blocked player", "> {mention} — {reason}", "mention", "id", "reason")
	LayoutQueuesReasonInGame  = DefineLayout("queues.reason_in_game", "Blocked: already in a game", "currently in a game")
	LayoutQueuesReasonNoReg   = DefineLayout("queues.reason_unregistered", "Blocked: not registered", "not registered")
	LayoutQueuesReasonBanned  = DefineLayout("queues.reason_restricted", "Blocked: has a restriction", "has active restriction")
	LayoutQueuesReadyTitle    = DefineLayout("queues.ready_title", "Ready heading", "✅ Ready to Start")
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
	LayoutLeaderboardRow = DefineRepeatedLayout("leaderboard.row", "One player's line", "> **#{position}** — `{elo}` ELO — {wins}W/{losses}L (`{win_rate}`)", "position", "elo", "rank", "peak", "wins", "losses", "games", "win_rate", "kills", "deaths", "final_kills", "beds", "mvps")
)

var (
	// /ranks
	LayoutRanksProgress    = DefineLayout("ranks.progress", "Your standing, with a rank to go", "**Your ELO:** `{elo}`\n**Your Rank:** `{rank}` **Next Rank:** `{next_rank}` (in `{next_in}` ELO)", "elo", "rank", "next_rank", "next_min_elo", "next_in")
	LayoutRanksProgressMax = DefineLayout("ranks.progress_max", "Your standing, at the top rank", "**Your ELO:** `{elo}`\n**Your Rank:** `{rank}`\nYou have achieved maximum competitive rank status!", "elo", "rank")
	LayoutRanksGuest       = DefineLayout("ranks.guest", "Shown to someone not registered", "Use `/register` to track your seasonal elo and rank progression!")
	LayoutRanksHeading     = DefineLayout("ranks.heading", "Heading above the ranks", "__**Available Ranks:**__")
	LayoutRanksLine        = DefineRepeatedLayout("ranks.line", "One rank", "{role} — ELO: `{min_elo} - {max_elo}` | Win: `+{win}` | Loss: `-{loss}`", "role", "name", "min_elo", "max_elo", "win", "loss")
	LayoutRanksNoMax       = DefineLayout("ranks.no_max", "Top of the highest rank", "∞")
	LayoutRanksUnranked    = DefineLayout("ranks.unranked", "Name when you have no rank", "Unranked")
)
