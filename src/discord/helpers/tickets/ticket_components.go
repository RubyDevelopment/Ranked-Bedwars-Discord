package tickets

import (
	"fmt"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================

var customEmojiPattern = regexp.MustCompile(`^<(a?):([A-Za-z0-9_]+):(\d{17,20})>$`)

// v2Text returns a markdown text block.
func v2Text(content string) discordgo.TextDisplay {
	return discordgo.TextDisplay{Content: truncate(content, 3900)}
}

// v2Divider returns a separator line with the given spacing.
func v2Divider(divider bool, large bool) discordgo.Separator {
	spacing := discordgo.SeparatorSpacingSizeSmall
	if large {
		spacing = discordgo.SeparatorSpacingSizeLarge
	}
	return discordgo.Separator{Divider: &divider, Spacing: &spacing}
}

// v2Container wraps components in a coloured container.
func v2Container(color int, components ...discordgo.MessageComponent) discordgo.Container {
	accent := color
	return discordgo.Container{AccentColor: &accent, Components: components}
}

// v2Row wraps up to five buttons (or one select menu) in an action row.
func v2Row(components ...discordgo.MessageComponent) discordgo.ActionsRow {
	return discordgo.ActionsRow{Components: components}
}

// v2Button builds a button with an optional emoji.
func v2Button(customID, label, emoji string, style discordgo.ButtonStyle) discordgo.Button {
	return discordgo.Button{
		CustomID: customID,
		Label:    label,
		Style:    style,
		Emoji:    parseComponentEmoji(emoji),
	}
}

// v2ButtonRows lays buttons out five to a row.
func v2ButtonRows(buttons []discordgo.MessageComponent) []discordgo.MessageComponent {
	var rows []discordgo.MessageComponent
	for start := 0; start < len(buttons); start += 5 {
		end := start + 5
		if end > len(buttons) {
			end = len(buttons)
		}
		rows = append(rows, v2Row(buttons[start:end]...))
	}
	return rows
}

// parseComponentEmoji reads a unicode emoji or a `<:name:id>` custom emoji.
func parseComponentEmoji(raw string) *discordgo.ComponentEmoji {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if match := customEmojiPattern.FindStringSubmatch(raw); match != nil {
		return &discordgo.ComponentEmoji{Name: match[2], ID: match[3], Animated: match[1] == "a"}
	}
	return &discordgo.ComponentEmoji{Name: raw}
}

// emojiPrefix returns the category emoji followed by a space, for inline text.
func emojiPrefix(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return raw + " "
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

// UpdateV2 rewrites the message a component (or component-born modal) came from.
func UpdateV2(s *discordgo.Session, i *discordgo.InteractionCreate, components ...discordgo.MessageComponent) error {
	return helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsIsComponentsV2, Components: components},
	})
}

// RespondEmbed answers a component or modal interaction with an embed only the clicker sees.
func RespondEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) error {
	return helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:  discordgo.MessageFlagsEphemeral,
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	})
}

// HexColor formats a colour for display and for pre-filling modals.
func HexColor(color int) string {
	return fmt.Sprintf("#%06X", color)
}

// ModalValues flattens a modal submission into custom_id -> value.
func ModalValues(i *discordgo.InteractionCreate) map[string]string {
	values := map[string]string{}
	for _, component := range i.ModalSubmitData().Components {
		row, ok := component.(*discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, inner := range row.Components {
			if input, ok := inner.(*discordgo.TextInput); ok {
				values[input.CustomID] = strings.TrimSpace(input.Value)
			}
		}
	}
	return values
}

// ModalTextInput builds one single-component modal row.
func ModalTextInput(customID, label, value, placeholder string, paragraph, required bool, maxLength int) discordgo.ActionsRow {
	style := discordgo.TextInputShort
	if paragraph {
		style = discordgo.TextInputParagraph
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.TextInput{
			CustomID:    customID,
			Label:       label,
			Style:       style,
			Value:       value,
			Placeholder: placeholder,
			Required:    required,
			MaxLength:   maxLength,
		},
	}}
}

// ============================================================================
// Access Checks
// ============================================================================

// InteractionIsAdmin reports whether the interacting member can manage the guild.
func InteractionIsAdmin(i *discordgo.InteractionCreate) bool {
	if i == nil || i.Member == nil {
		return false
	}
	return i.Member.Permissions&(discordgo.PermissionAdministrator|discordgo.PermissionManageGuild) != 0
}

// IsTicketStaff reports whether the member may work tickets of the category:
// an administrator, or anyone holding a panel-wide or category staff role.
func IsTicketStaff(member *discordgo.Member, panel *types.InteractivePanel, category *types.TicketCategory) bool {
	if member == nil {
		return false
	}
	if member.Permissions&(discordgo.PermissionAdministrator|discordgo.PermissionManageGuild) != 0 {
		return true
	}
	for _, roleID := range staffRoleIDs(panel, category) {
		if helpers.MemberHasRole(member, roleID) {
			return true
		}
	}
	return false
}

// ticketMode is how tickets of this category are created: the category's own mode, else the panel's, else thread.
func ticketMode(panel *types.InteractivePanel, category *types.TicketCategory) string {
	if category != nil && (category.Mode == types.TicketModeChannel || category.Mode == types.TicketModeThread) {
		return category.Mode
	}
	if panel != nil && panel.Ticket != nil && panel.Ticket.Settings.Mode == types.TicketModeChannel {
		return types.TicketModeChannel
	}
	return types.TicketModeThread
}

// pingRoleIDs are the roles a ticket in this category mentions: only the category's own list. The panel-wide staff
// roles can open every ticket but are not pinged unless the category lists them too.
func pingRoleIDs(category *types.TicketCategory) []string {
	seen := map[string]bool{}
	var roles []string
	if category != nil {
		for _, id := range category.StaffRoleIDs {
			if id != "" && !seen[id] {
				seen[id] = true
				roles = append(roles, id)
			}
		}
	}
	return roles
}

// staffRoleIDs merges the panel-wide and category staff roles without duplicates: everyone who may see the ticket.
func staffRoleIDs(panel *types.InteractivePanel, category *types.TicketCategory) []string {
	seen := map[string]bool{}
	var roles []string
	add := func(ids []string) {
		for _, id := range ids {
			if id != "" && !seen[id] {
				seen[id] = true
				roles = append(roles, id)
			}
		}
	}
	if panel != nil && panel.Ticket != nil {
		add(panel.Ticket.Settings.StaffRoleIDs)
	}
	if category != nil {
		add(category.StaffRoleIDs)
		add(category.ViewRoleIDs)
	}
	return roles
}

// ============================================================================
// Ticket Views
