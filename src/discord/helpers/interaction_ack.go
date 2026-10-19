package helpers

import (
	"errors"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Discord drops an interaction that is not answered within 3 seconds, and a guild's database calls can take most of
// that. InteractionRespond is the one way the bot answers an interaction: the first answer is sent as is, and once the
// interaction has been acknowledged (by the watchdog below, or an earlier deferral) a later answer is turned into the
// matching edit or follow-up, so a handler never has to know whether it was deferred.

// interactionAckDelay is how long a handler gets before WatchInteraction defers on its behalf.
const interactionAckDelay = 2500 * time.Millisecond

// ErrModalAfterAck means a handler tried to open a modal on an interaction that was already acknowledged.
var ErrModalAfterAck = errors.New("a modal can only be the first response to an interaction")

// acked holds the interactions that have had their first response, by ID: true when the deferral was a
// "thinking" channel message (a slash command), false for a component/modal acknowledgement.
var acked sync.Map

func markAcked(id string, channelMessage bool) bool {
	if _, loaded := acked.LoadOrStore(id, channelMessage); loaded {
		return false
	}
	time.AfterFunc(20*time.Minute, func() { acked.Delete(id) })
	return true
}

// InteractionRespond answers an interaction, converting the response when the interaction was already acknowledged.
func InteractionRespond(s *discordgo.Session, it *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
	channelMessage := resp.Type == discordgo.InteractionResponseDeferredChannelMessageWithSource
	if markAcked(it.ID, channelMessage) {
		return s.InteractionRespond(it, resp)
	}

	data := resp.Data
	if data == nil {
		data = &discordgo.InteractionResponseData{}
	}
	switch resp.Type {
	case discordgo.InteractionResponseUpdateMessage:
		_, err := s.InteractionResponseEdit(it, &discordgo.WebhookEdit{
			Content: &data.Content, Embeds: &data.Embeds, Components: &data.Components, Files: data.Files,
		})
		return err
	case discordgo.InteractionResponseChannelMessageWithSource:
		if wasChannel, _ := acked.Load(it.ID); wasChannel == true {
			_, err := s.InteractionResponseEdit(it, &discordgo.WebhookEdit{
				Content: &data.Content, Embeds: &data.Embeds, Components: &data.Components, Files: data.Files,
			})
			return err
		}
		_, err := s.FollowupMessageCreate(it, true, &discordgo.WebhookParams{
			Content: data.Content, Embeds: data.Embeds, Components: data.Components, Files: data.Files, Flags: data.Flags,
		})
		return err
	case discordgo.InteractionResponseModal:
		return ErrModalAfterAck
	}
	return nil // a deferral of something already acknowledged
}

// WatchInteraction acknowledges a button, menu or modal that nothing has answered yet after interactionAckDelay, so a
// slow handler cannot make Discord show "didn't respond in time". Whatever the handler sends later still arrives.
func WatchInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil || IsPrefixInteraction(i) {
		return
	}
	var deferral *discordgo.InteractionResponse
	switch i.Type {
	case discordgo.InteractionMessageComponent:
		deferral = &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate}
	case discordgo.InteractionModalSubmit:
		if i.Message != nil {
			deferral = &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate}
		} else {
			deferral = &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
			}
		}
	default:
		return // slash commands defer themselves (DeferSlashInteraction); autocomplete must answer with choices
	}
	id := i.Interaction.ID
	time.AfterFunc(interactionAckDelay, func() {
		if _, done := acked.Load(id); done {
			return
		}
		_ = InteractionRespond(s, i.Interaction, deferral)
	})
}
