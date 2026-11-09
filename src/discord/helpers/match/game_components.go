package match

import (
	"errors"
	"ranked-bedwars/src/discord/helpers"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================

func (d *gameDiscord) handleComponent(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	switch {
	case strings.HasPrefix(customID, mapVoteIDPrefix):
		d.handleMapVote(s, i, strings.Split(strings.TrimPrefix(customID, mapVoteIDPrefix), "|"))
		return true
	case strings.HasPrefix(customID, draftIDPrefix):
		d.handleDraftPick(s, i, strings.TrimPrefix(customID, draftIDPrefix))
		return true
	}
	return false
}

func ephemeral(s *discordgo.Session, i *discordgo.InteractionCreate, text string) {
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: text, Flags: discordgo.MessageFlagsEphemeral},
	})
}

func (d *gameDiscord) handleMapVote(s *discordgo.Session, i *discordgo.InteractionCreate, parts []string) {
	if len(parts) != 2 {
		return
	}
	gameID, _ := strconv.ParseUint(parts[0], 10, 64)
	idx, err := strconv.Atoi(parts[1])
	user := helpers.InteractionUserID(i)

	d.mu.Lock()
	v := d.votes[uint(gameID)]
	if v == nil || err != nil || idx < 0 || idx >= len(v.maps) {
		d.mu.Unlock()
		ephemeral(s, i, "This vote is over.")
		return
	}
	if _, in := v.game.Player(user); !in {
		d.mu.Unlock()
		ephemeral(s, i, "Only the players of this game can vote.")
		return
	}
	v.votes[user] = idx
	all := v.settled()
	d.mu.Unlock()
	embed, _ := d.voteEmbed(v, "")
	helpers.WithGuildFooterIconForGuild(s, v.game.GuildID, embed)

	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Components: v.components(uint(gameID), false, -1)},
	})
	if all {
		v.closeOnce.Do(func() { close(v.done) })
	}
}

func (d *gameDiscord) handleDraftPick(s *discordgo.Session, i *discordgo.InteractionCreate, gameIDText string) {
	gameID, _ := strconv.ParseUint(gameIDText, 10, 64)
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}
	user := helpers.InteractionUserID(i)

	d.mu.Lock()
	ui := d.drafts[uint(gameID)]
	d.mu.Unlock()
	if ui == nil {
		ephemeral(s, i, "This draft is over.")
		return
	}
	teamName := ""
	if before, err := Games.Draft(uint(gameID)); err == nil && before.Turn >= 0 {
		teamName = TeamName(before.Turn)
	}
	if err := Games.DraftPick(uint(gameID), user, values[0]); err != nil {
		ephemeral(s, i, draftErrorText(err))
		return
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})
	d.afterPick(s, uint(gameID), ui, user, values[0], teamName)
}

// afterPick is what follows a pick, however it was made: the line in the game chat, then the draft message and its timer for the next turn.
func (d *gameDiscord) afterPick(s *discordgo.Session, gameID uint, ui *draftUI, captainID, pickedID, teamName string) {
	// Kill the previous turn's timer now: until refreshDraft re-arms, it could still fire, auto-pick for the next captain and
	// post a second picking message alongside this one.
	d.mu.Lock()
	ui.turnKey = ""
	d.mu.Unlock()
	d.say(s, ui.channelID, "<@"+captainID+"> has picked <@"+pickedID+"> for **"+teamName+"**")
	d.refreshDraft(s, gameID, ui)
}

// gameUI is the Discord side of games, kept so the =pick command can reach the draft message it has to update.
var gameUI atomic.Pointer[gameDiscord]

// PickForCaptain makes a captain's pick for the =pick command and updates what the menu would: the chat line, the picking
// message (reposted for the next captain) and the pick timer. Without it the old timer would run on into the next turn.
func PickForCaptain(gameID uint, captainID, pickedID string) error {
	teamName := ""
	if before, err := Games.Draft(gameID); err == nil && before.Turn >= 0 {
		teamName = TeamName(before.Turn)
	}
	if err := Games.DraftPick(gameID, captainID, pickedID); err != nil {
		return err
	}
	if d := gameUI.Load(); d != nil {
		if s := d.session(); s != nil {
			d.mu.Lock()
			ui := d.drafts[gameID]
			d.mu.Unlock()
			if ui != nil {
				d.afterPick(s, gameID, ui, captainID, pickedID, teamName)
			}
		}
	}
	return nil
}

// say posts a plain line in the game chat.
func (d *gameDiscord) say(s *discordgo.Session, channelID, text string) {
	_, _ = s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Content: text, AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{discordgo.AllowedMentionTypeUsers}}})
}

func draftErrorText(err error) string {
	switch {
	case errors.Is(err, ErrNotYourTurn):
		return "It isn't your turn to pick."
	case errors.Is(err, ErrNotACaptain):
		return "Only the captains pick."
	case errors.Is(err, ErrDraftOver):
		return "The draft is already complete."
	}
	return "That pick wasn't allowed: " + err.Error()
}

// ============================================================================
// Configurable game embeds
