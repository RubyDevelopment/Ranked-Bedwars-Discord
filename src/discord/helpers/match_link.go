package helpers

import "fmt"

// MatchURL is the public website's page for one game ("" when DASHBOARD_URL is not set). The game's ID, not its number, is what the page
// looks it up by: a number repeats every season, the ID never does.
func MatchURL(guildID string, gameID uint) string {
	base := dashboardBase()
	if base == "" || guildID == "" || gameID == 0 {
		return ""
	}
	return fmt.Sprintf("%s/public/%s/games/%d", base, guildID, gameID)
}

// MatchLink is label as a Markdown link (with no link preview) to the game's match page, or just the label when there is no website address.
func MatchLink(guildID string, gameID uint, label string) string {
	if url := MatchURL(guildID, gameID); url != "" {
		return "[" + label + "](<" + url + ">)" // <> keeps Discord from adding a link preview under the message
	}
	return label
}
