package base

import (
	"fmt"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================
// Voice channel status
// ============================================================================

// A voice channel's status is the line under its name. It is written once a channel has been left alone for a short while: every call
// to ScheduleVoiceStatus starts that wait over, so a run of changes back to back is one update made after the last of them.

// VoiceStatusWindow is how long a channel has to stay unchanged before its status is written.
const VoiceStatusWindow = 3 * time.Second

// voiceStatusRetry is how long a channel the bot may not set a status on (no permission) is left alone.
const voiceStatusRetry = 10 * time.Minute

var voiceStatus = struct {
	mu      sync.Mutex
	timers  map[string]*time.Timer // channel -> the wait before its status is written
	written map[string]string      // channel -> the status last written
	blocked map[string]time.Time   // channel -> when the bot may try again after Discord refused
}{timers: map[string]*time.Timer{}, written: map[string]string{}, blocked: map[string]time.Time{}}

// ScheduleVoiceStatus (re)starts the wait before channelID's status is written. text is asked for when the wait is over, so it reads the
// state of that moment: it returns the status (empty clears it) and whether the channel is one it is about to say something about; when
// it is not, an empty status is only written if the bot had written one before. logf reports what goes wrong.
func ScheduleVoiceStatus(s *discordgo.Session, channelID string, text func() (status string, ours bool), logf func(string)) {
	if s == nil || channelID == "" {
		return
	}
	voiceStatus.mu.Lock()
	defer voiceStatus.mu.Unlock()
	if t := voiceStatus.timers[channelID]; t != nil {
		t.Stop()
	}
	voiceStatus.timers[channelID] = time.AfterFunc(VoiceStatusWindow, func() {
		voiceStatus.mu.Lock()
		delete(voiceStatus.timers, channelID)
		voiceStatus.mu.Unlock()
		defer func() { // a status is not worth the bot: whatever goes wrong here stays here
			if r := recover(); r != nil {
				logf(fmt.Sprintf("Voice status of channel %s failed: %v", channelID, r))
			}
		}()
		status, ours := text()
		writeVoiceStatus(s, channelID, status, ours, logf)
	})
}

// writeVoiceStatus sets channelID's status when it differs from what was last written.
func writeVoiceStatus(s *discordgo.Session, channelID, status string, ours bool, logf func(string)) {
	voiceStatus.mu.Lock()
	if until, wait := voiceStatus.blocked[channelID]; wait && time.Now().Before(until) {
		voiceStatus.mu.Unlock()
		return
	}
	last, known := voiceStatus.written[channelID]
	if !ours && last == "" {
		voiceStatus.mu.Unlock()
		return
	}
	// The first write after the bot starts is always sent, even an empty one, so a status left from before is cleared.
	if known && last == status {
		voiceStatus.mu.Unlock()
		return
	}
	voiceStatus.mu.Unlock()

	url := discordgo.EndpointChannel(channelID) + "/voice-status"
	if _, err := s.RequestWithBucketID("PUT", url, map[string]string{"status": status}, url); err != nil {
		voiceStatus.mu.Lock()
		voiceStatus.blocked[channelID] = time.Now().Add(voiceStatusRetry)
		voiceStatus.mu.Unlock()
		logf(fmt.Sprintf("Could not set the voice status of channel %s (the bot needs the Set Voice Channel Status permission there): %v", channelID, err))
		return
	}
	voiceStatus.mu.Lock()
	voiceStatus.written[channelID] = status
	delete(voiceStatus.blocked, channelID)
	voiceStatus.mu.Unlock()
}

// ForgetVoiceStatus drops what is remembered of a channel (it was deleted).
func ForgetVoiceStatus(channelID string) {
	voiceStatus.mu.Lock()
	defer voiceStatus.mu.Unlock()
	if t := voiceStatus.timers[channelID]; t != nil {
		t.Stop()
	}
	delete(voiceStatus.timers, channelID)
	delete(voiceStatus.written, channelID)
	delete(voiceStatus.blocked, channelID)
}
