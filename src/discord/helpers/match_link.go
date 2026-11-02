package helpers

import "fmt"

// MatchURL is the public website's page for one game ("" when DASHBOARD_URL is not set). The page looks the game up by
// guildNumber, its number among every game the guild has recorded: unlike the season's game number it never repeats, and
// unlike the database ID it does not count the other servers' games.
func MatchURL(guildID string, guildNumber int) string {
	base := dashboardBase()
	if base == "" || guildID == "" || guildNumber < 1 {
		return ""
	}
	return fmt.Sprintf("%s/public/%s/games/%d", base, guildID, guildNumber)
}

// MatchLink is label as a Markdown link (with no link preview) to the game's match page, or just the label when there is no website address.
func MatchLink(guildID string, guildNumber int, label string) string {
	if url := MatchURL(guildID, guildNumber); url != "" {
		return "[" + label + "](<" + url + ">)" // <> keeps Discord from adding a link preview under the message
	}
	return label
}
