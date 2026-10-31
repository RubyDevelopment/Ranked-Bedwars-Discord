package helpers

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
)

// The host buttons under the party and team embeds: Party Warp, Party Invite, Game Warp and PG Kick. They let the players of a
// bot-hosted game nudge the bot (warp the party in, invite themselves again, warp to the game, remove a stranger from the
// private game) without a staff member. Which command each one sends, and how long they rest, is the server profile's
// `hosting.buttons`; the engine does the work. An embed can switch them off with `"buttons": false`.

const hostKickModalPrefix = "rbw_pg_kick_modal:"

var hostButtonID = regexp.MustCompile(`^rbw_(party_warp|party_invite|game_warp|pg_kick):(.+)$`)

// hostButtonEmbeds are the embeds that carry the buttons.
var hostButtonEmbeds = map[string]bool{"partyInvitation": true, "teamSelection": true, "matchJoined": true}

var hostButtonLabels = []struct{ id, label string }{
	{"party_warp", "Party Warp"},
	{"party_invite", "Party Invite"},
	{"game_warp", "Game Warp"},
	{"pg_kick", "PG Kick"},
}

// hostButtons is the button row for an embed of a bot-hosted game, or nil.
func hostButtons(g Game, key string, t embedTemplate) []discordgo.MessageComponent {
	if !hostButtonEmbeds[key] || g.Bot == "" || (t.Buttons != nil && !*t.Buttons) {
		return nil
	}
	row := &discordgo.ActionsRow{}
	for _, b := range hostButtonLabels {
		row.Components = append(row.Components, &discordgo.Button{Label: firstNonEmpty(strings.TrimSpace(flowOf(g.GuildID).ButtonLabels[b.id]), b.label), Style: discordgo.SecondaryButton, CustomID: "rbw_" + b.id + ":" + g.Bot})
	}
	return []discordgo.MessageComponent{row}
}

func hostButtonReply(s *discordgo.Session, i *discordgo.InteractionCreate, text string) {
	_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: text, Flags: discordgo.MessageFlagsEphemeral},
	})
}

// hostButtonGame finds the game the button belongs to and checks the presser may use it; otherwise it answers and reports false.
func hostButtonGame(s *discordgo.Session, i *discordgo.InteractionCreate, bot string) (Game, bool) {
	g, ok := Games.ByChannel(i.ChannelID)
	switch {
	case !ok:
		hostButtonReply(s, i, "❌ There is no active match in this channel.")
	case !strings.EqualFold(g.Bot, bot):
		hostButtonReply(s, i, "❌ This button belongs to another host bot session.")
	default:
		if _, isPlayer := g.Player(InteractionUserID(i)); isPlayer {
			return g, true
		}
		hostButtonReply(s, i, "❌ Only the players of this match can use these buttons.")
	}
	return Game{}, false
}

// runHostAction sends the button to the engine and tells the presser what came of it.
func runHostAction(s *discordgo.Session, i *discordgo.InteractionCreate, g Game, action, mcName, target string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := Engine.HostAction(ctx, g.ID, action, mcName, target)
	switch {
	case err != nil:
		hostButtonReply(s, i, "❌ "+err.Error())
	case !res.OK:
		hostButtonReply(s, i, "❌ "+firstNonEmpty(res.Error, "The host bot could not do that."))
	default:
		hostButtonReply(s, i, fmt.Sprintf("✅ Sent `%s` as the host bot.", res.Command))
	}
}

// HandleHostButton answers a press of a host button. It reports false for any other component.
func HandleHostButton(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	m := hostButtonID.FindStringSubmatch(customID)
	if m == nil {
		return false
	}
	action, bot := m[1], m[2]
	g, ok := hostButtonGame(s, i, bot)
	if !ok {
		return true
	}
	if action == "pg_kick" {
		_ = RespondModal(s, i, hostKickModalPrefix+bot, "PG Kick Player", &discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			&discordgo.TextInput{CustomID: "kick_username", Label: "Minecraft Username", Style: discordgo.TextInputShort, Placeholder: "Player to remove from the private game", Required: true},
		}})
		return true
	}
	mcName := ""
	if action == "party_invite" {
		view, err := database.Must().Player.Get(g.GuildID, InteractionUserID(i))
		if err != nil || view.Config == nil || strings.TrimSpace(view.Config.Username) == "" {
			hostButtonReply(s, i, "❌ You have no linked Minecraft username.")
			return true
		}
		mcName = strings.TrimSpace(view.Config.Username)
	}
	_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})
	runHostAction(s, i, g, action, mcName, "")
	return true
}

// HandleHostButtonModal answers the PG Kick form. It reports false for any other modal.
func HandleHostButtonModal(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	data := i.ModalSubmitData()
	if !strings.HasPrefix(data.CustomID, hostKickModalPrefix) {
		return false
	}
	g, ok := hostButtonGame(s, i, strings.TrimPrefix(data.CustomID, hostKickModalPrefix))
	if !ok {
		return true
	}
	target := ""
	for _, row := range data.Components {
		if r, ok := row.(*discordgo.ActionsRow); ok {
			for _, c := range r.Components {
				if in, ok := c.(*discordgo.TextInput); ok && in.CustomID == "kick_username" {
					target = strings.TrimSpace(in.Value)
				}
			}
		}
	}
	if target == "" {
		hostButtonReply(s, i, "❌ Enter a Minecraft username.")
		return true
	}
	_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})
	runHostAction(s, i, g, "pg_kick", "", target)
	return true
}
