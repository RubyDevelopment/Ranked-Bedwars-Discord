package queue

import (
	"fmt"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/discord/helpers/guildlink"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================
// Voice channel status
// ============================================================================

// A queue channel shows its state in Discord's voice channel status, the line under its name:
//
//	✅ 3/8 queued · ⏳ Waiting for players   not enough queued yet to start a game (the last part is left off once there are enough)
//	✅ 3/8 queued · ✨ 1 in game            some of them are still in a game
//	⚠️ 3/8 queued · 2 ineligible           some were refused a place (not registered, banned, rank gate…)
//	⏸️ Queue paused
//	⛔ Queue disabled                      the queue is switched off on the dashboard (only while someone is in the channel)
//	📈 / 💤 estimated activity             nobody is in the channel: when games usually happen (see queue_activity.go); nothing if there is too little history
//
// It is written 3 seconds after the last change in the channel (see base.ScheduleVoiceStatus): every join or leave starts that wait over,
// so a run of members joining back to back is one update, made once things have settled.

// ScheduleVoiceStatus (re)starts the wait before channelID's status is brought up to date. Call it whenever someone joins or leaves a
// queue channel, or the lobby there changes. What the status reads is worked out when the wait is over.
func ScheduleVoiceStatus(s *discordgo.Session, guildID, channelID string) {
	base.ScheduleVoiceStatus(s, channelID, func() (string, bool) {
		snap, err := Queues.Snapshot(guildID)
		if err != nil || snap == nil {
			return "", false
		}
		bind, ok := snap.Bindings[channelID]
		if !ok {
			if snap.Disabled[channelID] {
				if len(voiceRoster(s, guildID, channelID)) == 0 {
					return "", true // nobody there to tell: no status
				}
				return "⛔ Queue disabled", true // switched off on the dashboard: said to whoever sits in the channel
			}
			return "", false // no longer a queue channel: its status is cleared if the bot had written one
		}
		return voiceStatusText(s, guildID, channelID, bind), true
	}, func(message string) { queueLogger.Warn(message) })
}

// voiceStatusText is what a queue channel's status reads right now.
func voiceStatusText(s *discordgo.Session, guildID, channelID string, bind QueueBinding) string {
	if Queues.IsPaused(guildID) {
		return "⏸️ Queue paused" // a state of the queue, so it is shown whether or not anyone is in the channel
	}
	roster := voiceRoster(s, guildID, channelID)
	if len(roster) == 0 {
		return estimatedActivity(guildID) // nobody in the channel: when games usually happen (nothing, if there is too little history)
	}
	var queued, inGame, ineligible int
	for _, userID := range roster {
		switch {
		case Queues.deps.InGame(guildID, userID):
			inGame++
		case isDenied(channelID, userID):
			ineligible++
		default:
			queued++
		}
	}
	icon := "✅"
	if ineligible > 0 {
		icon = "⚠️"
	}
	slots := RequiredSlots(bind.Mode)
	parts := []string{fmt.Sprintf("%s %d/%d queued", icon, queued, slots)}
	if inGame > 0 {
		parts = append(parts, fmt.Sprintf("✨ %d in game", inGame))
	}
	if ineligible > 0 {
		parts = append(parts, fmt.Sprintf("%d ineligible", ineligible))
	}
	if queued < slots {
		parts = append(parts, "⏳ Waiting for players")
	}
	return strings.Join(parts, " · ")
}

// voiceRoster lists the members (not bots) sitting in a voice channel. The state is read under one lock, without calling its methods
// (they lock again), so a write waiting in between cannot deadlock it.
func voiceRoster(s *discordgo.Session, guildID, channelID string) []string {
	s.State.RLock()
	defer s.State.RUnlock()
	for _, g := range s.State.Guilds {
		if g.ID != guildID {
			continue
		}
		bots := map[string]bool{}
		for _, m := range g.Members {
			if m.User != nil && m.User.Bot {
				bots[m.User.ID] = true
			}
		}
		var ids []string
		for _, vs := range g.VoiceStates {
			if vs.ChannelID == channelID && !bots[vs.UserID] {
				ids = append(ids, vs.UserID)
			}
		}
		return ids
	}
	return nil
}

// voiceStatusRefresh is how often every queue channel's status is checked against the configuration, which can change on the dashboard
// (a queue switched off or on, queues paused) without any event in Discord.
const voiceStatusRefresh = 30 * time.Second

// StartVoiceStatuses brings every queue channel's status up to date once the bot is connected and its voice states are loaded, so a
// restart does not leave an old status behind, and then keeps checking them against the configuration.
func StartVoiceStatuses(s *discordgo.Session) {
	go func() {
		time.Sleep(15 * time.Second)
		for {
			refreshVoiceStatuses(s)
			time.Sleep(voiceStatusRefresh)
		}
	}()
}

// refreshVoiceStatuses schedules the status of every queue channel, live or switched off, of every server the bot is in.
func refreshVoiceStatuses(s *discordgo.Session) {
	defer func() { // the refresh runs on its own: whatever goes wrong in one round must not end it
		if r := recover(); r != nil {
			queueLogger.Error(fmt.Sprintf("Refreshing the queue voice statuses failed: %v", r))
		}
	}()
	s.State.RLock()
	var guilds []string
	for _, g := range s.State.Guilds {
		if guildlink.IsSecondary(g.ID) {
			continue // a linked secondary server has no queue of its own
		}
		guilds = append(guilds, g.ID)
	}
	s.State.RUnlock()
	for _, guildID := range guilds {
		snap, err := Queues.Snapshot(guildID)
		if err != nil || snap == nil {
			continue
		}
		for channelID := range snap.Bindings {
			ScheduleVoiceStatus(s, guildID, channelID)
		}
		for channelID := range snap.Disabled {
			ScheduleVoiceStatus(s, guildID, channelID)
		}
	}
}
