package general

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/match"
	"ranked-bedwars/src/discord/helpers/partymgr"
	"ranked-bedwars/src/discord/helpers/punish"
	"ranked-bedwars/src/discord/helpers/queue"
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
	if game, ok := match.Games.ByChannel(i.ChannelID); ok {
		return showGameStats(s, i, i.GuildID, game)
	}

	// 2. Voice channel: show the queue status
	if voiceState, err := s.State.VoiceState(i.GuildID, actor); err == nil && voiceState != nil && voiceState.ChannelID != "" {
		return showQueueStatus(s, i, i.GuildID, voiceState.ChannelID)
	}

	// 3. Neither a game nor a queue channel
	return helpers.EditInteractionEmbed(s, i, QueueStatusNoGameOrQueueEmbed(i))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// showGameStats displays the game's stats and roster
func showGameStats(s *discordgo.Session, i *discordgo.InteractionCreate, guildID string, game match.Game) error {
	// Get season info
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(guildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorQueueStatusEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, QueueStatusNoActiveSeasonEmbed(i))
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

			for _, p := range teamPlayers {
				assignedPlayerIDs[p.UserID] = true
			}
			fields = append(fields, queueRosterField(guildID, seasonID, helpers.LayoutQueuesTeamTitle, teamName, teamPlayers))
		}
	}

	// Show unassigned players (draft pool)
	var unassigned []match.GamePlayer
	for _, p := range game.Players {
		if !assignedPlayerIDs[p.UserID] {
			unassigned = append(unassigned, p)
		}
	}

	if len(unassigned) > 0 {
		fields = append(fields, queueRosterField(guildID, seasonID, helpers.LayoutQueuesPoolTitle, "", unassigned))
	}

	// Check for active draft
	draftView, err := match.Games.Draft(game.ID)
	if err == nil && draftView.Turn >= 0 {
		captainName := ""
		for _, p := range game.Players {
			if p.UserID == draftView.Captains[draftView.Turn] {
				captainName = p.Username
				break
			}
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  helpers.FillLayout(guildID, helpers.LayoutQueuesDraftTitle, nil),
			Value: helpers.FillLayout(guildID, helpers.LayoutQueuesDraftTurn, map[string]string{"captain": captainName, "captain_mention": "<@" + draftView.Captains[draftView.Turn] + ">"}),
		})
	}

	return helpers.EditInteractionEmbed(s, i, QueueStatusGameStatsEmbed(i, game, seasonKey, fields))
}

// queueRosterField is a team's (or the draft pool's) field: its heading and a row for each player, from the layouts.
func queueRosterField(guildID string, seasonID uint, titleLayout, team string, players []match.GamePlayer) *discordgo.MessageEmbedField {
	// Each player's wins/losses and kills/deaths for the season as they are (5/2, not 2.50), looked up side by side (a player with no stats yet reads 0/0).
	wlr, kdr := make([]string, len(players)), make([]string, len(players))
	var wg sync.WaitGroup
	for n, p := range players {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wlr[n], kdr[n] = "0/0", "0/0"
			if st, err := database.Must().Stats.Get(guildID, p.UserID, seasonID); err == nil {
				wlr[n], kdr[n] = fmt.Sprintf("%d/%d", st.Wins, st.Losses), fmt.Sprintf("%d/%d", st.Kills, st.Deaths)
			}
		}()
	}
	wg.Wait()

	rows := make([]string, 0, len(players))
	total := 0
	for n, p := range players {
		total += p.EloBefore
		rows = append(rows, helpers.FillLayout(guildID, helpers.LayoutQueuesPlayer, map[string]string{
			"mention": "<@" + p.UserID + ">", "id": p.UserID, "name": p.Username, "elo": strconv.Itoa(p.EloBefore), "team": team,
			"wlr": wlr[n], "kdr": kdr[n],
		}))
	}
	avg := 0
	if len(players) > 0 {
		avg = total / len(players)
	}
	return &discordgo.MessageEmbedField{
		Name:  helpers.FillLayout(guildID, titleLayout, map[string]string{"team": team, "avg_elo": strconv.Itoa(avg), "players": strconv.Itoa(len(players))}),
		Value: strings.Join(rows, "\n"),
	}
}

// showQueueStatus displays the queue status for a voice channel
func showQueueStatus(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, voiceChannelID string) error {
	// Get the queue configuration binding of the channel
	snapshot, err := queue.Queues.Snapshot(guildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorQueueStatusEmbed(i))
	}
	binding, ok := snapshot.Bindings[voiceChannelID]
	if !ok {
		return helpers.EditInteractionEmbed(s, i, QueueStatusNotQueueChannelEmbed(i))
	}

	// Check if queues are paused
	if queue.Queues.IsPaused(guildID) {
		return helpers.EditInteractionEmbed(s, i, QueueStatusClosedEmbed(i))
	}

	// Get the members of the voice channel
	guild, err := s.State.Guild(guildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorQueueStatusEmbed(i))
	}
	var vcMembers []string
	for _, vs := range guild.VoiceStates {
		if vs.ChannelID == voiceChannelID {
			vcMembers = append(vcMembers, vs.UserID)
		}
	}

	// Check eligibility of each member. The two lookups per member run side by side across members; the lines keep
	// the channel's order.
	verdicts := make([]string, len(vcMembers)) // "" = eligible, otherwise the line explaining why not
	blockedRow := func(userID, reason string, vars map[string]string) string {
		return helpers.FillLayout(guildID, helpers.LayoutQueuesBlockedRow, map[string]string{"mention": "<@" + userID + ">", "id": userID, "reason": helpers.FillLayout(guildID, reason, vars)})
	}
	var wg sync.WaitGroup
	for n, userID := range vcMembers {
		if match.Games.InGame(guildID, userID) {
			verdicts[n] = blockedRow(userID, helpers.LayoutQueuesReasonInGame, nil)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			player, err := database.Must().Player.Get(guildID, userID)
			if err != nil {
				verdicts[n] = blockedRow(userID, helpers.LayoutQueuesReasonNoReg, nil)
				return
			}
			if hasQueueRestriction(guildID, sdk.UintToString(player.Player.ID)) {
				verdicts[n] = blockedRow(userID, helpers.LayoutQueuesReasonBanned, nil)
			}
		}()
	}
	wg.Wait()

	// Party members whose party isn't fully in the channel are held back by the queue, so flag them too.
	if cfg := queue.Queues.PartyConfig(guildID, binding.Mode); binding.Mode.IsPartyQueueEnabled && partymgr.PartiesEnabled(cfg) && cfg.RequireAllMembersInQueue {
		inVC := make(map[string]bool, len(vcMembers))
		for _, id := range vcMembers {
			inVC[id] = true
		}
		for n, userID := range vcMembers {
			if verdicts[n] != "" {
				continue
			}
			party := partymgr.Parties.PartyOf(guildID, userID)
			if party == nil || !party.IsEffective() {
				continue
			}
			here := 0
			for _, id := range party.Members {
				if inVC[id] {
					here++
				}
			}
			if here < len(party.Members) {
				verdicts[n] = blockedRow(userID, helpers.LayoutQueuesReasonPartyIncomplete, map[string]string{"present": strconv.Itoa(here), "total": strconv.Itoa(len(party.Members))})
			}
		}
	}

	var eligible, blocked []string
	for n, userID := range vcMembers {
		if verdicts[n] != "" {
			blocked = append(blocked, verdicts[n])
		} else {
			eligible = append(eligible, userID)
		}
	}

	// Calculate required slots
	totalSlots := queue.RequiredSlots(binding.Mode)

	counts := map[string]string{"eligible": strconv.Itoa(len(eligible)), "slots": strconv.Itoa(totalSlots), "present": strconv.Itoa(len(vcMembers))}
	var fields []*discordgo.MessageEmbedField
	if len(eligible) < totalSlots {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  helpers.FillLayout(guildID, helpers.LayoutQueuesBlockingTitle, counts),
			Value: helpers.FillLayout(guildID, helpers.LayoutQueuesBlockingText, counts),
		})
	}

	if len(blocked) > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  helpers.FillLayout(guildID, helpers.LayoutQueuesBlockedTitle, map[string]string{"count": strconv.Itoa(len(blocked))}),
			Value: strings.Join(blocked, "\n"),
		})
	}

	if len(fields) == 0 && len(eligible) >= totalSlots {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  helpers.FillLayout(guildID, helpers.LayoutQueuesReadyTitle, counts),
			Value: helpers.FillLayout(guildID, helpers.LayoutQueuesReadyText, counts),
		})
	}

	description := helpers.FillLayout(guildID, helpers.LayoutQueuesDetails, map[string]string{
		"channel": "<#" + voiceChannelID + ">", "mode": binding.Mode.Name, "teams": strconv.Itoa(binding.Mode.TeamCount),
		"per_team": strconv.Itoa(binding.Mode.PlayersPerTeam), "slots": counts["slots"], "eligible": counts["eligible"], "present": counts["present"],
	})

	return helpers.EditInteractionEmbed(s, i, QueueStatusQueueEmbed(i, binding.Mode.Name, description, fields))
}

// hasQueueRestriction reports whether the player has an active queue or server ban.
func hasQueueRestriction(guildID, playerID string) bool {
	active, err := database.Must().Punishments.ByPlayer(guildID, playerID, true, "", sdk.Page{})
	if err != nil || active == nil {
		return false
	}
	for _, row := range active.Data {
		if punish.PunishmentTypeIn(row.Type, []string{"ban", "server_ban"}) {
			return true
		}
	}
	return false
}

// ============================================================================
// Helper functions
// ============================================================================

func UnexpectedErrorQueueStatusEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the game or queue information. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func QueueStatusNoGameOrQueueEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Game or Queue",
		Description: "Use this command in a game text channel to view game stats, or join a queue voice channel to check the queue status.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func QueueStatusNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. Please contact an administrator to configure a season.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func QueueStatusNotQueueChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not a Queue Channel",
		Description: "This voice channel is not configured as a queue channel. Join a queue voice channel to check its status.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func QueueStatusClosedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Queues Closed",
		Description: "Ranked queues are currently **closed** by staff.\n-# Games will not start until queues are opened again.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func QueueStatusGameStatsEmbed(i *discordgo.InteractionCreate, game match.Game, season string, fields []*discordgo.MessageEmbedField) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Game Stats · #%d", game.Number),
		Description: fmt.Sprintf("> **Mode:** `%s`\n> **Season:** `%s`", game.Mode.Name, season),
		Fields:      fields,
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func QueueStatusQueueEmbed(i *discordgo.InteractionCreate, modeName, description string, fields []*discordgo.MessageEmbedField) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Queue Status · %s", modeName),
		Description: description,
		Fields:      fields,
		Thumbnail:   helpers.ThumbPlayers,
	})
}
