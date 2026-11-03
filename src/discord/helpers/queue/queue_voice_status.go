package queue

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================
// Voice channel status
// ============================================================================

// A queue channel shows its state in Discord's voice channel status, the line under its name:
//
//	✅ 3/8 queued                          everyone in the channel is queued
//	✅ 3/8 queued · ✨ 1 in game            some of them are still in a game
//	⚠️ 3/8 queued · 2 ineligible           some were refused a place (not registered, banned, rank gate…)
//	⏸️ Queue paused
//	(nothing)                              nobody is in the channel: the status is cleared
//
// It is written 3 seconds after the last change in the channel: every join or leave starts that wait over, so a run of members joining
// back to back is one update, made once things have settled.

// voiceStatusWindow is how long a channel has to stay unchanged before its status is written.
const voiceStatusWindow = 3 * time.Second

// voiceStatusRetry is how long a channel the bot may not set a status on (no permission) is left alone.
const voiceStatusRetry = 10 * time.Minute

var voiceStatus = struct {
	mu      sync.Mutex
	timers  map[string]*time.Timer // channel -> the wait before its status is written
	written map[string]string      // channel -> the status last written
	blocked map[string]time.Time   // channel -> when the bot may try again after Discord refused
}{timers: map[string]*time.Timer{}, written: map[string]string{}, blocked: map[string]time.Time{}}

// ScheduleVoiceStatus (re)starts the wait before channelID's status is brought up to date. Call it whenever someone joins or leaves a
// queue channel, or the lobby there changes.
func ScheduleVoiceStatus(s *discordgo.Session, guildID, channelID string) {
	if s == nil || channelID == "" {
		return
	}
	voiceStatus.mu.Lock()
	defer voiceStatus.mu.Unlock()
	if t := voiceStatus.timers[channelID]; t != nil {
		t.Stop()
	}
	voiceStatus.timers[channelID] = time.AfterFunc(voiceStatusWindow, func() {
		voiceStatus.mu.Lock()
		delete(voiceStatus.timers, channelID)
		voiceStatus.mu.Unlock()
		defer func() { // a status is not worth the bot: whatever goes wrong here stays here
			if r := recover(); r != nil {
				queueLogger.Error(fmt.Sprintf("Voice status of queue channel %s failed: %v", channelID, r))
			}
		}()
		writeVoiceStatus(s, guildID, channelID)
	})
}

// writeVoiceStatus works out what channelID's status should read and sets it when it differs from what was last written.
func writeVoiceStatus(s *discordgo.Session, guildID, channelID string) {
	snap, err := Queues.Snapshot(guildID)
	if err != nil || snap == nil {
		return
	}
	// A channel that is no longer a queue (removed or disabled) gets its status cleared, once, if the bot had written one.
	bind, ok := snap.Bindings[channelID]
	text := ""
	if ok {
		text = voiceStatusText(s, guildID, channelID, bind)
	} else {
		voiceStatus.mu.Lock()
		previous := voiceStatus.written[channelID]
		voiceStatus.mu.Unlock()
		if previous == "" {
			return
		}
	}

	voiceStatus.mu.Lock()
	if until, wait := voiceStatus.blocked[channelID]; wait && time.Now().Before(until) {
		voiceStatus.mu.Unlock()
		return
	}
	// The first write after the bot starts is always sent, even an empty one, so a status left from before is cleared.
	if last, known := voiceStatus.written[channelID]; known && last == text {
		voiceStatus.mu.Unlock()
		return
	}
	voiceStatus.mu.Unlock()

	url := discordgo.EndpointChannel(channelID) + "/voice-status"
	if _, err := s.RequestWithBucketID("PUT", url, map[string]string{"status": text}, url); err != nil {
		voiceStatus.mu.Lock()
		voiceStatus.blocked[channelID] = time.Now().Add(voiceStatusRetry)
		voiceStatus.mu.Unlock()
		queueLogger.Warn(fmt.Sprintf("Could not set the voice status of queue channel %s (the bot needs the Set Voice Channel Status permission there): %v", channelID, err))
		return
	}
	voiceStatus.mu.Lock()
	voiceStatus.written[channelID] = text
	delete(voiceStatus.blocked, channelID)
	voiceStatus.mu.Unlock()
}

// voiceStatusText is what a queue channel's status reads right now.
func voiceStatusText(s *discordgo.Session, guildID, channelID string, bind QueueBinding) string {
	roster := voiceRoster(s, guildID, channelID)
	if len(roster) == 0 {
		return "" // nobody in the channel: no status
	}
	if Queues.IsPaused(guildID) {
		return "⏸️ Queue paused"
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
	parts := []string{fmt.Sprintf("%s %d/%d queued", icon, queued, RequiredSlots(bind.Mode))}
	if inGame > 0 {
		parts = append(parts, fmt.Sprintf("✨ %d in game", inGame))
	}
	if ineligible > 0 {
		parts = append(parts, fmt.Sprintf("%d ineligible", ineligible))
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

// StartVoiceStatuses brings every queue channel's status up to date once the bot is connected and its voice states are loaded, so a
// restart does not leave an old status behind.
func StartVoiceStatuses(s *discordgo.Session) {
	go func() {
		time.Sleep(15 * time.Second)
		s.State.RLock()
		var guilds []string
		for _, g := range s.State.Guilds {
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
		}
	}()
}
