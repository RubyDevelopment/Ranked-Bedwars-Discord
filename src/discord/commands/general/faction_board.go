package general

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/pagination"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.RegisterComponentHandler(handleFactionBoard)
}

const (
	// factionBoardPrefix starts the custom id of a leaderboard button: fclb|<page>|<board> turns a page, fclb|b<n>|<board> switches
	// board; an inert button is fclb|off-<slot>.
	factionBoardPrefix = "fclb|"
	factionPerPage     = 10
)

var factionBoards = []struct{ key, label string }{{"lifetime", "Lifetime"}, {"seasonal", "Seasonal"}}

// ============================================================================
// Command Execution
// ============================================================================

func factionLeaderboard(c *factionCtx) error {
	embed, components := buildFactionLeaderboard(c.guildID, c.str("board"), max(1, c.integer("page")))
	return c.send(embed, components...)
}

// buildFactionLeaderboard loads one page of a faction leaderboard and writes it, with its board and page buttons.
func buildFactionLeaderboard(guildID, board string, page int) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	if board != "seasonal" {
		board = "lifetime"
	}
	rows, total, err := database.FactionLeaderboard(guildID, board, factionPerPage, (page-1)*factionPerPage)
	if err != nil {
		log.Printf("[Faction] leaderboard in %s: %v", guildID, err)
		return UnexpectedErrorFactionEmbed(), nil
	}
	if len(rows) == 0 {
		if page > 1 {
			return FactionLeaderboardEmptyPageEmbed(page), nil
		}
		return FactionLeaderboardEmptyEmbed(board), nil
	}
	totalPages := max(1, (int(total)+factionPerPage-1)/factionPerPage)

	switcher := &discordgo.ActionsRow{}
	for _, b := range factionBoards {
		btn := &discordgo.Button{CustomID: fmt.Sprintf("%sb%d|%s", factionBoardPrefix, page, b.key), Label: b.label, Style: discordgo.SecondaryButton}
		if b.key == board {
			btn.Style, btn.Disabled = discordgo.PrimaryButton, true
		}
		switcher.Components = append(switcher.Components, btn)
	}
	components := append([]discordgo.MessageComponent{switcher}, pagination.BuildPageRow(page, totalPages,
		func(target int) string { return fmt.Sprintf("%s%d|%s", factionBoardPrefix, target, board) },
		func(slot string) string { return factionBoardPrefix + "off-" + slot })...)
	return FactionLeaderboardEmbed(rows, board, page, totalPages), components
}

// ============================================================================
// Buttons
// ============================================================================

// handleFactionBoard turns the page of a faction leaderboard, or switches between the lifetime and seasonal boards. Only the player
// who ran the command can.
func handleFactionBoard(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	rest, ok := strings.CutPrefix(customID, factionBoardPrefix)
	if !ok {
		return false
	}
	target, board, found := strings.Cut(rest, "|")
	page, err := strconv.Atoi(strings.TrimPrefix(target, "b")) // "b<n>" is the board switch, which starts over at page 1
	if !found || err != nil || page < 1 {
		return true // an inert button: nothing to do
	}
	if strings.HasPrefix(target, "b") {
		page = 1
	}
	if factionOwnerOnly(s, i, "/faction leaderboard") {
		return true
	}
	embed, components := buildFactionLeaderboard(i.GuildID, board, page)
	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Components: components},
	})
	return true
}
