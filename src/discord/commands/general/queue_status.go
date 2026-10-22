package general

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
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
	return helpers.EditInteractionEmbed(s, i, helpers.QueueStatusNoGameOrQueueEmbed(i))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// showGameStats displays the game's stats and roster
func showGameStats(s *discordgo.Session, i *discordgo.InteractionCreate, guildID string, game helpers.Game) error {
	// Get season info
	seasonKey, _, err := helpers.ActiveSeasonKey(guildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorQueueStatusEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.QueueStatusNoActiveSeasonEmbed(i))
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
			fields = append(fields, queueRosterField(guildID, helpers.LayoutQueuesTeamTitle, teamName, teamPlayers))
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
		fields = append(fields, queueRosterField(guildID, helpers.LayoutQueuesPoolTitle, "", unassigned))
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
			Name:  helpers.FillLayout(guildID, helpers.LayoutQueuesDraftTitle, nil),
			Value: helpers.FillLayout(guildID, helpers.LayoutQueuesDraftTurn, map[string]string{"captain": captainName, "captain_mention": "<@" + draftView.Captains[draftView.Turn] + ">"}),
		})
	}

	return helpers.EditInteractionEmbed(s, i, helpers.QueueStatusGameStatsEmbed(i, game, seasonKey, fields))
}

// queueRosterField is a team's (or the draft pool's) field: its heading and a row for each player, from the layouts.
func queueRosterField(guildID, titleLayout, team string, players []helpers.GamePlayer) *discordgo.MessageEmbedField {
	rows := make([]string, 0, len(players))
	total := 0
	for _, p := range players {
		total += p.EloBefore
		rows = append(rows, helpers.FillLayout(guildID, helpers.LayoutQueuesPlayer, map[string]string{
			"mention": "<@" + p.UserID + ">", "id": p.UserID, "name": p.Username, "elo": strconv.Itoa(p.EloBefore), "team": team,
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
	snapshot, err := helpers.Queues.Snapshot(guildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorQueueStatusEmbed(i))
	}
	binding, ok := snapshot.Bindings[voiceChannelID]
	if !ok {
		return helpers.EditInteractionEmbed(s, i, helpers.QueueStatusNotQueueChannelEmbed(i))
	}

	// Check if queues are paused
	if helpers.Queues.IsPaused(guildID) {
		return helpers.EditInteractionEmbed(s, i, helpers.QueueStatusClosedEmbed(i))
	}

	// Get the members of the voice channel
	guild, err := s.State.Guild(guildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorQueueStatusEmbed(i))
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
	blockedRow := func(userID, reason string) string {
		return helpers.FillLayout(guildID, helpers.LayoutQueuesBlockedRow, map[string]string{"mention": "<@" + userID + ">", "id": userID, "reason": helpers.FillLayout(guildID, reason, nil)})
	}
	var wg sync.WaitGroup
	for n, userID := range vcMembers {
		if helpers.Games.InGame(guildID, userID) {
			verdicts[n] = blockedRow(userID, helpers.LayoutQueuesReasonInGame)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			player, err := database.Must().Player.Get(guildID, userID)
			if err != nil {
				verdicts[n] = blockedRow(userID, helpers.LayoutQueuesReasonNoReg)
				return
			}
			if hasQueueRestriction(guildID, sdk.UintToString(player.Player.ID)) {
				verdicts[n] = blockedRow(userID, helpers.LayoutQueuesReasonBanned)
			}
		}()
	}
	wg.Wait()
	var eligible, blocked []string
	for n, userID := range vcMembers {
		if verdicts[n] != "" {
			blocked = append(blocked, verdicts[n])
		} else {
			eligible = append(eligible, userID)
		}
	}

	// Calculate required slots
	totalSlots := helpers.RequiredSlots(binding.Mode)

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

	return helpers.EditInteractionEmbed(s, i, helpers.QueueStatusQueueEmbed(i, binding.Mode.Name, description, fields))
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
