package general

import (
	"fmt"
	"strconv"
	"strings"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/match"
	"ranked-bedwars/src/discord/helpers/proofread"
	"ranked-bedwars/src/discord/helpers/ratings"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

// What staff see under a submitted screenshot: the result the chat shows (winners, losers, MVP) and two buttons, one to agree and score the
// game as read, one to say it is wrong and score it by hand. The buttons carry everything they need in their ids, so they still work after a
// restart; only staff can press them.

const submitReviewPrefix = "subscore|" // subscore|ok|<game id>|<winning team>|<mvp user id> and subscore|manual|<game id>

func init() { helpers.RegisterComponentHandler(handleSubmitReview) }

func SubmitResultProcessingEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("pending", types.PresetOptions{
		Title:       "Reading Your Screenshot",
		Description: "Please wait while the screenshot is checked against the game. This takes a few seconds.",
		Thumbnail:   helpers.ThumbTimer,
	})
}

// playerMentions lists the mentions of a team's players.
func playerMentions(game match.Game, team string) string {
	var out []string
	for _, p := range game.TeamPlayers(team) {
		out = append(out, "<@"+p.UserID+">")
	}
	if len(out) == 0 {
		return "—"
	}
	return strings.Join(out, " ")
}

// reviewFields is the check as a game's own people: winners, losers, MVP and what else the chat confirmed.
func reviewFields(game match.Game, res proofread.Result) []*discordgo.MessageEmbedField {
	var fields []*discordgo.MessageEmbedField
	if res.Winner != "" {
		var losers []string
		for _, team := range game.Teams {
			if !strings.EqualFold(team, res.Winner) {
				losers = append(losers, fmt.Sprintf("**%s:** %s", team, playerMentions(game, team)))
			}
		}
		if len(losers) == 0 { // a game without a stored team list: the other teams of the roster
			seen := map[string]bool{strings.ToLower(res.Winner): true}
			for _, p := range game.Players {
				if t := strings.ToLower(p.Team); !seen[t] {
					seen[t] = true
					losers = append(losers, fmt.Sprintf("**%s:** %s", p.Team, playerMentions(game, p.Team)))
				}
			}
		}
		fields = append(fields,
			&discordgo.MessageEmbedField{Name: "🏆 Winners in chat", Value: fmt.Sprintf("**%s** (%s)\n%s", res.Winner, res.WinnerBy, playerMentions(game, res.Winner))},
			&discordgo.MessageEmbedField{Name: "Losers", Value: strings.Join(losers, "\n")},
		)
	} else {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Winners in chat", Value: "Not clear from the screenshot. Staff should score the game by hand."})
	}
	if mvp := mvpOf(game, res); mvp != "" {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "⭐ MVP", Value: fmt.Sprintf("<@%s> · %s", mvp, res.MVPBy)})
	}
	var notes []string
	if res.Held != "" {
		notes = append(notes, "🛑 Not scored automatically: "+res.Held+". Staff must confirm it.")
	}
	if res.Mismatch != "" {
		notes = append(notes, "⚠️ "+res.Mismatch)
	}
	if len(res.BedBreaks) > 0 {
		c := res.BedBreaks[0]
		notes = append(notes, fmt.Sprintf("Bed broken by **%s** (%s)", c.Name, c.Team))
	}
	if len(res.Seen) > 0 {
		notes = append(notes, fmt.Sprintf("**%d of %d** players seen in chat", len(res.Seen), len(game.Players)))
	}
	if len(notes) > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Screenshot check", Value: strings.Join(notes, "\n")})
	}
	return fields
}

// autoScoreBlocker is why a reading is not clear enough to score a game without staff ("" when it is): the chat has to name the winner, agree
// with the team that submitted, and name enough of the players for the reading to be trusted.
func autoScoreBlocker(game match.Game, res proofread.Result, claimed string, minPlayersPercent int) string {
	switch {
	case res.Winner == "":
		return "the winner could not be read from the screenshot"
	case res.Mismatch != "" || !strings.EqualFold(res.Winner, claimed):
		return "the chat shows " + res.Winner + " won, but " + claimed + " submitted"
	case len(game.Players) > 0 && len(res.Seen)*100 < minPlayersPercent*len(game.Players):
		return fmt.Sprintf("only %d of %d players were seen in the chat", len(res.Seen), len(game.Players))
	}
	return ""
}

// autoScoredEmbed is the reply when a game was scored from the screenshot alone.
func autoScoredEmbed(game match.Game, submitter, team string, results []ratings.EloResult, res proofread.Result, proofURL string) *discordgo.MessageEmbed {
	winners, losers := match.SplitResultMentions(results)
	e := helpers.Preset("success", types.PresetOptions{
		Title:       "Game Scored Automatically",
		Description: fmt.Sprintf("<@%s>'s screenshot for game **#%d** was clear, so the game was scored from it.\n\n> **Winning Team:** `%s`\n> **Winners:** %s\n> **Losers:** %s\n> **Proof:** %s", submitter, game.Number, team, winners, losers, proofURL),
		Thumbnail:   helpers.ThumbDoubleSwords,
	})
	if mvp := mvpOf(game, res); mvp != "" {
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "⭐ MVP", Value: fmt.Sprintf("<@%s> · %s", mvp, res.MVPBy)})
	}
	return e
}

// mvpOf is the Discord id of the MVP the chat showed: a winner of the game, or "".
func mvpOf(game match.Game, res proofread.Result) string {
	if res.MVP.Name == "" || res.Winner == "" {
		return ""
	}
	for _, p := range game.TeamPlayers(res.Winner) {
		if p.UserID == res.MVP.ID || res.MVP.ID == "" && strings.EqualFold(proofread.PlainName(p.Username), res.MVP.Name) {
			return p.UserID
		}
	}
	return ""
}

// reviewButtons are the two buttons under the result. Without a winner there is nothing to agree with, so only the manual hint stays.
func reviewButtons(game match.Game, res proofread.Result) []discordgo.MessageComponent {
	id := strconv.FormatUint(uint64(game.ID), 10)
	buttons := []discordgo.MessageComponent{}
	if res.Winner != "" {
		buttons = append(buttons, &discordgo.Button{Label: "Confirm & score", Style: discordgo.SuccessButton,
			CustomID: submitReviewPrefix + "ok|" + id + "|" + res.Winner + "|" + mvpOf(game, res)})
	}
	buttons = append(buttons, &discordgo.Button{Label: "Wrong · score manually", Style: discordgo.SecondaryButton, CustomID: submitReviewPrefix + "manual|" + id})
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: buttons}}
}

// isStaff reports whether the member may score games: the guild's staff role, or an administrator.
func isStaff(i *discordgo.InteractionCreate) bool {
	if i.Member == nil {
		return false
	}
	if i.Member.Permissions&discordgo.PermissionAdministrator != 0 {
		return true
	}
	role, err := helpers.RequirementSlotID(i.GuildID, "score", types.ReqRole, types.StaffRoleKey)
	if err != nil || role == "" {
		return false
	}
	for _, r := range i.Member.Roles {
		if r == role {
			return true
		}
	}
	return false
}

func handleSubmitReview(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, submitReviewPrefix) {
		return false
	}
	parts := strings.Split(customID, "|")
	if len(parts) < 3 {
		return false
	}
	raw, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return false
	}
	say := func(text string) {
		_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral, Content: text},
		})
	}
	update := func(embed *discordgo.MessageEmbed) {
		_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Components: []discordgo.MessageComponent{}},
		})
	}
	if !isStaff(i) {
		say("Only staff can score a game from its screenshot.")
		return true
	}
	game, ok := match.Games.Get(uint(raw))
	if !ok || game.GuildID != i.GuildID {
		update(helpers.SimpleErrorEmbed("This game is already over, or no longer exists."))
		return true
	}
	actor := helpers.InteractionUserID(i)
	if parts[1] == "manual" || len(parts) < 5 {
		update(helpers.Preset("info", types.PresetOptions{
			Title:       "Score It By Hand",
			Description: fmt.Sprintf("<@%s> did not accept the screenshot's reading. Score game **#%d** with `/score`, naming the winning team and the MVP.", actor, game.Number),
			Thumbnail:   helpers.ThumbFlag,
		}))
		return true
	}
	result, err := match.Games.Score(actor, i.GuildID, game.ID, parts[3], match.ScoreOptions{MVP: parts[4]})
	if err != nil {
		say("The game could not be scored: " + err.Error())
		return true
	}
	winners, losers := match.SplitResultMentions(result.Results)
	update(helpers.Preset("success", types.PresetOptions{
		Title:       "Game Scored",
		Description: fmt.Sprintf("<@%s> confirmed the screenshot and game **#%d** was scored.\n\n> **Winning Team:** `%s`\n> **Winners:** %s\n> **Losers:** %s", actor, game.Number, parts[3], winners, losers),
		Thumbnail:   helpers.ThumbDoubleSwords,
	}))
	return true
}
