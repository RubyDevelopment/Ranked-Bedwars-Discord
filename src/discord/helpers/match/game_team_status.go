package match

import (
	"fmt"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers/base"
	appLog "ranked-bedwars/src/utility"
)

// ============================================================================
// Team voice channel status
// ============================================================================

// While a bot-hosted match is on, each team's voice channel shows who is still in it, in the status under its name:
//
//	✅ 3 alive · 💀 1 dead
//
// A player is dead once they are out of the match (a final kill, or their team eliminated). It is written 3 seconds after the last change
// (see base.ScheduleVoiceStatus), so a run of kills is one update; it is cleared when the match is over.

// teamStatus is what a team's voice channel reads: how many of its players are alive and how many are out.
func teamStatus(g Game, team string, out map[string]bool) string {
	alive, dead := 0, 0
	for _, p := range g.TeamPlayers(team) {
		if out[p.UserID] {
			dead++
		} else {
			alive++
		}
	}
	return fmt.Sprintf("✅ %d alive · 💀 %d dead", alive, dead)
}

// scheduleTeamStatuses brings every team voice channel of the game up to date after a change. over clears them instead (the match ended).
func (d *gameDiscord) scheduleTeamStatuses(s *discordgo.Session, g Game, over bool) {
	if s == nil || len(g.Channels.Voice) == 0 {
		return
	}
	out := map[string]bool{}
	if !over { // a finished game's state may be gone already: it is not needed to clear the statuses
		st := d.state(g.ID)
		d.mu.Lock()
		for id, gone := range st.eliminated {
			out[id] = gone
		}
		d.mu.Unlock()
	}

	for team, channelID := range g.Channels.Voice {
		text := ""
		if !over {
			text = teamStatus(g, team, out) // worked out now: the game's state is gone by the time the wait is over
		}
		base.ScheduleVoiceStatus(s, channelID, func() (string, bool) { return text, true }, func(message string) {
			appLog.EngineLogger.Warn("[GameManager] " + message)
		})
	}
}
