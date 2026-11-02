package helpers

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers/banner"
	apputil "ranked-bedwars/src/utility"
)

// ============================================================================
// Types & Configuration
// ============================================================================

var (
	prefixDurationPattern   = regexp.MustCompile(`(?i)^(\d+[smhdw]|perm|permanent|permanentban)$`)
	prefixGameNumberPattern = regexp.MustCompile(`^\d{3,9}$`) // a game number among the words of =sr @player 27439 afk
	prefixSnowflakePattern  = regexp.MustCompile(`^\d{17,20}$`)
	timeFilterPattern       = regexp.MustCompile(`^(\d+)([smhdw])$`)
)

var (
	deferredSlashInteractions sync.Map
)

// ============================================================================
// Discord Interaction Management
// ============================================================================

func trimMessageData(data *discordgo.InteractionResponseData) {
	if data == nil {
		return
	}
	data.Content = strings.TrimSpace(data.Content)
	trimEmbeds(data.Embeds)
}

func trimWebhookEdit(edit *discordgo.WebhookEdit) {
	if edit == nil {
		return
	}
	if edit.Content != nil {
		content := strings.TrimSpace(*edit.Content)
		edit.Content = &content
	}
	if edit.Embeds != nil {
		trimEmbeds(*edit.Embeds)
	}
}

func trimEmbeds(embeds []*discordgo.MessageEmbed) {
	for _, embed := range embeds {
		if embed == nil {
			continue
		}
		embed.Title = strings.TrimSpace(embed.Title)
		embed.Description = strings.TrimSpace(embed.Description)
		if embed.Author != nil {
			embed.Author.Name = strings.TrimSpace(embed.Author.Name)
		}
		if embed.Footer != nil {
			embed.Footer.Text = strings.TrimSpace(embed.Footer.Text)
		}
		for _, field := range embed.Fields {
			if field != nil {
				field.Name = strings.TrimSpace(field.Name)
				field.Value = strings.TrimSpace(field.Value)
			}
		}
	}
}

func trimMessageSend(message *discordgo.MessageSend) {
	if message == nil {
		return
	}
	message.Content = strings.TrimSpace(message.Content)
	trimEmbeds(message.Embeds)
}

func trimPayloadStrings(value any) {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			if text, ok := item.(string); ok {
				value[key] = strings.TrimSpace(text)
			} else {
				trimPayloadStrings(item)
			}
		}
	case []any:
		for index, item := range value {
			if text, ok := item.(string); ok {
				value[index] = strings.TrimSpace(text)
			} else {
				trimPayloadStrings(item)
			}
		}
	case []map[string]any:
		for _, item := range value {
			trimPayloadStrings(item)
		}
	}
}

// Respond handles both prefix and slash command responses.
func Respond(s *discordgo.Session, i *discordgo.InteractionCreate, resp *discordgo.InteractionResponse) error {
	if resp != nil && resp.Data != nil {
		trimMessageData(resp.Data)
		WithGuildFooterIcon(s, i, resp.Data.Embeds...)
	}
	if IsPrefixInteraction(i) {
		if resp == nil || resp.Data == nil {
			return nil
		}
		content := resp.Data.Content
		var embeds []*discordgo.MessageEmbed
		if len(resp.Data.Embeds) > 0 {
			embeds = resp.Data.Embeds
		}
		return sendPrefixReply(s, i, content, embeds, resp.Data.Components...)
	}
	return InteractionRespond(s, i.Interaction, resp)
}

// EditInteractionEmbed edits an interaction response with an embed.
func EditInteractionEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, emb *discordgo.MessageEmbed) error {
	WithGuildFooterIcon(s, i, emb)
	// A guild can answer with one of its saved messages instead (see response_override.go); any failure keeps the built-in embed.
	if sent, err := sendCustomResponse(s, i, emb); sent {
		return nil
	} else if err != nil {
		apputil.DiscordLogger.Warn(fmt.Sprintf("custom response for %q in %s failed, using the built-in embed: %v", emb.Title, i.GuildID, err))
	}
	if IsPrefixInteraction(i) {
		return sendPrefixReply(s, i, "", []*discordgo.MessageEmbed{emb})
	}
	if IsDeferredSlashInteraction(i) {
		_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{emb}})
		return err
	}
	return Respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{emb}},
	})
}

// EditInteractionResponse edits an interaction response.
func EditInteractionResponse(s *discordgo.Session, i *discordgo.InteractionCreate, edit *discordgo.WebhookEdit) error {
	trimWebhookEdit(edit)
	if edit != nil && edit.Embeds != nil {
		WithGuildFooterIcon(s, i, *edit.Embeds...)
	}
	if IsPrefixInteraction(i) {
		var embeds []*discordgo.MessageEmbed
		if edit != nil && edit.Embeds != nil {
			embeds = *edit.Embeds
		}
		content := ""
		if edit != nil && edit.Content != nil {
			content = *edit.Content
		}
		var components []discordgo.MessageComponent
		if edit != nil && edit.Components != nil {
			components = *edit.Components
		}
		return sendPrefixReply(s, i, content, embeds, components...)
	}
	_, err := s.InteractionResponseEdit(i.Interaction, edit)
	return err
}

// EditInteractionComponents edits an interaction response with embed and components.
func EditInteractionComponents(s *discordgo.Session, i *discordgo.InteractionCreate, emb *discordgo.MessageEmbed, components []discordgo.MessageComponent) error {
	WithGuildFooterIcon(s, i, emb)
	if sent, err := sendCustomResponse(s, i, emb, components...); sent {
		return nil
	} else if err != nil {
		apputil.DiscordLogger.Warn(fmt.Sprintf("custom response for %q in %s failed, using the built-in embed: %v", emb.Title, i.GuildID, err))
	}
	if IsPrefixInteraction(i) {
		return sendPrefixReply(s, i, "", []*discordgo.MessageEmbed{emb}, components...)
	}
	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds:     &[]*discordgo.MessageEmbed{emb},
		Components: &components,
	})
	return err
}

// DeferSlashInteraction acknowledges a slash command to prevent timeout.
func DeferSlashInteraction(s *discordgo.Session, i *discordgo.InteractionCreate, ephemeral bool) error {
	if i == nil || IsPrefixInteraction(i) || IsDeferredSlashInteraction(i) {
		return nil
	}
	deferResponse := &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}
	if ephemeral {
		deferResponse.Data = &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		}
	}
	if err := InteractionRespond(s, i.Interaction, deferResponse); err != nil {
		return err
	}
	MarkDeferredSlashInteraction(i)
	return nil
}

// ============================================================================
// Prefix Interaction Helpers
// ============================================================================
// Punishment Types and Configurations
// ============================================================================
// Punishment Helper Functions
// ============================================================================

// ParseTimeFilter parses duration filters such as 1d, 7d, or 2w.
func ParseTimeFilter(input string) (time.Duration, bool) {
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return 0, true
	}
	matches := timeFilterPattern.FindStringSubmatch(input)
	if len(matches) != 3 {
		return 0, false
	}
	value := 0
	if _, err := fmt.Sscanf(matches[1], "%d", &value); err != nil || value <= 0 {
		return 0, false
	}
	switch matches[2] {
	case "s":
		return time.Duration(value) * time.Second, true
	case "m":
		return time.Duration(value) * time.Minute, true
	case "h":
		return time.Duration(value) * time.Hour, true
	case "d":
		return time.Duration(value) * 24 * time.Hour, true
	case "w":
		return time.Duration(value) * 7 * 24 * time.Hour, true
	default:
		return 0, false
	}
}

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
	if resp != nil && resp.Data != nil {
		trimMessageData(resp.Data)
	}
	// Every answer to an interaction, commands and events alike, carries the server's icon in its footer.
	if resp != nil && resp.Data != nil && it != nil {
		WithGuildFooterIconForGuild(s, it.GuildID, resp.Data.Embeds...)
	}
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
func EditComponentEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) {
	WithGuildFooterIcon(s, i, embed)
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}})
}

// FollowupEmbed sends an ephemeral embed after the click was already acknowledged.
func FollowupEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) {
	WithGuildFooterIcon(s, i, embed)
	_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Embeds: []*discordgo.MessageEmbed{embed},
		Flags:  discordgo.MessageFlagsEphemeral,
	})
}
func updateRequirementFixMessage(s *discordgo.Session, i *discordgo.InteractionCreate, emb *discordgo.MessageEmbed, components []discordgo.MessageComponent) {
	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	WithGuildFooterIcon(s, i, emb)
	embeds := []*discordgo.MessageEmbed{emb}
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &embeds, Components: &components})
}

// RespondModal opens a modal in answer to a component click.
func RespondModal(s *discordgo.Session, i *discordgo.InteractionCreate, customID, title string, rows ...discordgo.MessageComponent) error {
	return InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{CustomID: customID, Title: title, Components: rows},
	})
}

// ============================================================================
// Banner style option
// ============================================================================

// StyleOption is the slash option both commands share.
func StyleOption() *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Name:         "style",
		Description:  "Pick the banner style to use for yourself (remembered for next time)",
		Type:         discordgo.ApplicationCommandOptionString,
		Required:     false,
		Autocomplete: true,
	}
}

// StyleAutocomplete answers with the styles of command the invoker may use.
func StyleAutocomplete(command string) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		typed := ""
		for _, opt := range i.ApplicationCommandData().Options {
			if opt.Focused {
				typed = strings.ToLower(opt.StringValue())
			}
		}
		var choices []*discordgo.ApplicationCommandOptionChoice
		for _, b := range banner.LoadBanners(i.GuildID).Eligible(command, banner.BannerViewer(InteractionUserID(i), i.Member)) {
			if typed == "" || strings.Contains(strings.ToLower(b.Name), typed) {
				choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: banner.TruncateRunes(b.Name, 100), Value: b.ID})
			}
			if len(choices) == 25 {
				break
			}
		}
		_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionApplicationCommandAutocompleteResult,
			Data: &discordgo.InteractionResponseData{Choices: choices},
		})
	}
}
