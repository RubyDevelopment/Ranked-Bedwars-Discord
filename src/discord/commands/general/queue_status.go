package general

import (
	"fmt"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(QueueStatusCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// QueueStatusCommand shows the match stats and draft status of a game channel, or the queue status of the voice channel the invoker is in.
var QueueStatusCommand = &types.Command{
	Name:        "queues",
	Description: "Views game stats, draft status, or the status of the queue you are in.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data:        &discordgo.ApplicationCommand{},
	Execute:     executeQueueStatusCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeQueueStatusCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	actor := helpers.InteractionUserID(i)

	// 1. Game text channel: show the match stats
	if game, ok := helpers.Games.ByChannel(i.ChannelID); ok {
		return showGameStats(s, i, i.GuildID, game)
	}

	// 2. Voice channel: show the queue status
	if voiceState, err := s.State.VoiceState(i.GuildID, actor); err == nil && voiceState != nil && voiceState.ChannelID != "" {
		return showQueueStatus(s, i, i.GuildID, voiceState.ChannelID)
	}

	// 3. Neither a game nor a queue channel
	return helpers.EditInteractionEmbed(s, i, queueStatusNoGameOrQueueEmbed())
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// showGameStats displays the game's stats and roster
func showGameStats(s *discordgo.Session, i *discordgo.InteractionCreate, guildID string, game helpers.Game) error {
	// Get season info
	seasonKey, _, err := helpers.ActiveSeasonKey(guildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorQueueStatusEmbed())
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, queueStatusNoActiveSeasonEmbed())
	}

	// Build fields for teams
	var fields []*discordgo.MessageEmbedField
	assignedPlayerIDs := make(map[string]bool)

	if len(game.Teams) > 0 {
		for _, teamName := range game.Teams {
			teamPlayers := game.TeamPlayers(teamName)
			if len(teamPlayers) == 0 {
				continue
			}

			var playerLines []string
			totalElo := 0
			for _, p := range teamPlayers {
				assignedPlayerIDs[p.UserID] = true
				playerLines = append(playerLines, fmt.Sprintf("> <@%s> · ELO: **%d**", p.UserID, p.EloBefore))
				totalElo += p.EloBefore
			}

			avgElo := 0
			if len(teamPlayers) > 0 {
				avgElo = totalElo / len(teamPlayers)
			}

			fields = append(fields, &discordgo.MessageEmbedField{
				Name:   fmt.Sprintf("👥 %s · Avg ELO %d", teamName, avgElo),
				Value:  strings.Join(playerLines, "\n"),
				Inline: false,
			})
		}
	}

	// Show unassigned players (draft pool)
	var unassigned []helpers.GamePlayer
	for _, p := range game.Players {
		if !assignedPlayerIDs[p.UserID] {
			unassigned = append(unassigned, p)
		}
	}

	if len(unassigned) > 0 {
		var poolLines []string
		totalElo := 0
		for _, p := range unassigned {
			poolLines = append(poolLines, fmt.Sprintf("> <@%s> · ELO: **%d**", p.UserID, p.EloBefore))
			totalElo += p.EloBefore
		}

		avgElo := 0
		if len(unassigned) > 0 {
			avgElo = totalElo / len(unassigned)
		}

		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("📦 Draft Pool · Avg ELO %d", avgElo),
			Value:  strings.Join(poolLines, "\n"),
			Inline: false,
		})
	}

	// Check for active draft
	draftView, err := helpers.Games.Draft(game.ID)
	if err == nil && draftView.Turn >= 0 {
		captainName := ""
		for _, p := range game.Players {
			if p.UserID == draftView.Captains[draftView.Turn] {
				captainName = p.Username
				break
			}
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   "👑 Draft Turn",
			Value:  fmt.Sprintf("It is **%s**'s turn to pick.", captainName),
			Inline: false,
		})
	}

	return helpers.EditInteractionEmbed(s, i, queueStatusGameStatsEmbed(game, seasonKey, fields))
}

// showQueueStatus displays the queue status for a voice channel
func showQueueStatus(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, voiceChannelID string) error {
	// Get the queue configuration binding of the channel
	snapshot, err := helpers.Queues.Snapshot(guildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorQueueStatusEmbed())
	}
	binding, ok := snapshot.Bindings[voiceChannelID]
	if !ok {
		return helpers.EditInteractionEmbed(s, i, queueStatusNotQueueChannelEmbed())
	}

	// Check if queues are paused
	if helpers.Queues.IsPaused(guildID) {
		return helpers.EditInteractionEmbed(s, i, queueStatusClosedEmbed())
	}

	// Get the members of the voice channel
	guild, err := s.State.Guild(guildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorQueueStatusEmbed())
	}
	var vcMembers []string
	for _, vs := range guild.VoiceStates {
		if vs.ChannelID == voiceChannelID {
			vcMembers = append(vcMembers, vs.UserID)
		}
	}

	// Check eligibility of each member
	var eligible, blocked []string
	for _, userID := range vcMembers {
		if helpers.Games.InGame(guildID, userID) {
			blocked = append(blocked, fmt.Sprintf("> <@%s> — currently in a game", userID))
			continue
		}

		// Check if registered
		player, err := database.Must().Player.Get(guildID, userID)
		if err != nil {
			blocked = append(blocked, fmt.Sprintf("> <@%s> — not registered", userID))
			continue
		}

		// Check for queue restrictions
		if hasQueueRestriction(guildID, sdk.UintToString(player.Player.ID)) {
			blocked = append(blocked, fmt.Sprintf("> <@%s> — has active restriction", userID))
			continue
		}

		eligible = append(eligible, userID)
	}

	// Calculate required slots
	totalSlots := helpers.RequiredSlots(binding.Mode)

	var fields []*discordgo.MessageEmbedField
	if len(eligible) < totalSlots {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  "⛔ Blocking Queue",
			Value: fmt.Sprintf("Not enough eligible players (`%d/%d`)", len(eligible), totalSlots),
		})
	}

	if len(blocked) > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  "⛔ Blocked Players",
			Value: strings.Join(blocked, "\n"),
		})
	}

	if len(fields) == 0 && len(eligible) >= totalSlots {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  "✅ Ready to Start",
			Value: fmt.Sprintf("`%d/%d` eligible players present", len(eligible), totalSlots),
		})
	}

	description := fmt.Sprintf(
		"> **Channel:** <#%s>\n> **Mode:** `%s` (`%dv%d`)\n> **Total Slots:** `%d`\n> **Eligible:** `%d/%d` in VC",
		voiceChannelID, binding.Mode.Name, binding.Mode.TeamCount, binding.Mode.PlayersPerTeam, totalSlots, len(eligible), len(vcMembers),
	)

	return helpers.EditInteractionEmbed(s, i, queueStatusQueueEmbed(binding.Mode.Name, description, fields))
}

// hasQueueRestriction reports whether the player has an active queue or server ban.
func hasQueueRestriction(guildID, playerID string) bool {
	active, err := database.Must().Punishments.ByPlayer(guildID, playerID, true, "", sdk.Page{})
	if err != nil || active == nil {
		return false
	}
	for _, row := range active.Data {
		if helpers.PunishmentTypeIn(row.Type, []string{"ban", "server_ban"}) {
			return true
		}
	}
	return false
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorQueueStatusEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the game or queue information. Please contact an administrator for assistance or try again later.",
	})
}

func queueStatusNoGameOrQueueEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Game or Queue",
		Description: "Use this command in a game text channel to view game stats, or join a queue voice channel to check the queue status.",
	})
}

func queueStatusNoActiveSeasonEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. Please contact an administrator to configure a season.",
	})
}

func queueStatusNotQueueChannelEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not a Queue Channel",
		Description: "This voice channel is not configured as a queue channel. Join a queue voice channel to check its status.",
	})
}

func queueStatusClosedEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Queues Closed",
		Description: "Ranked queues are currently **closed** by staff.\n-# Games will not start until queues are opened again.",
	})
}

func queueStatusGameStatsEmbed(game helpers.Game, season string, fields []*discordgo.MessageEmbedField) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Game Stats · #%d", game.Number),
		Description: fmt.Sprintf("> **Mode:** `%s`\n> **Season:** `%s`", game.Mode.Name, season),
		Fields:      fields,
	})
}

func queueStatusQueueEmbed(modeName, description string, fields []*discordgo.MessageEmbedField) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Queue Status · %s", modeName),
		Description: description,
		Fields:      fields,
	})
}
