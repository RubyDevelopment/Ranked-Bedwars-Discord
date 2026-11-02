package tickets

import (
	"encoding/json"
	"fmt"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================

// TicketPlaceholders expands {user}, {username}, {category}, {reference}, {number}, {staff}, {server},
// {created} (when the ticket was opened, as a Discord relative time) and {status} (where it stands) in configurable ticket text.
func TicketPlaceholders(text string, ticket *types.Ticket, panel *types.InteractivePanel, category *types.TicketCategory, guildName string) string {
	staff := "the support team"
	if roles := pingRoleIDs(category); len(roles) > 0 {
		mentions := make([]string, len(roles))
		for idx, id := range roles {
			mentions[idx] = "<@&" + id + ">"
		}
		staff = strings.Join(mentions, " ")
	}
	return strings.NewReplacer(
		"{user}", "<@"+ticket.OwnerID+">",
		"{username}", ticket.OwnerName,
		"{category}", ticket.CategoryLabel,
		"{reference}", "#"+ticket.Ref,
		"{number}", fmt.Sprintf("%d", ticket.Number),
		"{staff}", staff,
		"{server}", guildName,
		"{created}", fmt.Sprintf("<t:%d:R>", ticket.CreatedAt),
		"{status}", TicketStatusLabel(ticket),
	).Replace(text)
}

// TicketPanelMessage renders the published panel members open tickets from.
//
// The panel is a list of blocks (look.Layout): the built-in ones show the panel's fields, the others are free text,
// lines, galleries, sections and link buttons. A panel without a layout has the standard one.
func TicketPanelMessage(panel *types.InteractivePanel) []discordgo.MessageComponent {
	look := panel.Appearance
	color := look.Color
	if color == 0 {
		color = defaultPanelColor
	}

	layout := ticketPanelLayout(panel)
	var inner []discordgo.MessageComponent
	for _, block := range layout {
		inner = append(inner, panelBlockComponents(panel, block)...)
	}
	if look.NoColor { // a container without an accent has no bar
		return []discordgo.MessageComponent{discordgo.Container{Components: inner}}
	}
	return []discordgo.MessageComponent{v2Container(color, inner...)}
}

func ticketPanelLayout(panel *types.InteractivePanel) []types.PanelBlock {
	layout := panel.Appearance.Layout
	if len(layout) == 0 {
		return StandardPanelLayout(panel)
	}
	if len(panel.Ticket.Categories) > 0 && !layoutHas(layout, "buttons") && !ticketCategoryButtonsInline(panel) {
		layout = append(append([]types.PanelBlock{}, layout...), types.PanelBlock{Type: "buttons"})
	}
	return layout
}

func ticketPanelSend(panel *types.InteractivePanel) *discordgo.MessageSend {
	look := panel.Appearance
	switch look.Format {
	case "message":
		var parts []string
		if look.Title != "" {
			parts = append(parts, "## "+look.Title)
		}
		if look.Description != "" {
			parts = append(parts, look.Description)
		}
		if !look.HideCategoryList {
			for _, category := range panel.Ticket.Categories {
				text := fmt.Sprintf("**%s%s**", emojiPrefix(category.Emoji), category.Label)
				if category.Description != "" {
					text += "\n" + category.Description
				}
				parts = append(parts, text)
			}
		}
		if look.Footer != "" {
			parts = append(parts, look.Footer)
		}
		return &discordgo.MessageSend{
			Content:         truncate(strings.Join(parts, "\n\n"), 2000),
			Components:      ticketPanelButtons(panel),
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		}
	case "embed":
		embed := &discordgo.MessageEmbed{
			Title:       look.Title,
			Description: look.Description,
			Color:       look.Color,
		}
		if look.NoColor {
			embed.Color = helpers.EmbedBlendColor
		} else if embed.Color == 0 {
			embed.Color = defaultPanelColor
		}
		if look.Footer != "" {
			embed.Footer = &discordgo.MessageEmbedFooter{Text: look.Footer}
		}
		if look.BannerURL != "" {
			embed.Image = &discordgo.MessageEmbedImage{URL: look.BannerURL}
		}
		if !look.HideCategoryList {
			for _, category := range panel.Ticket.Categories {
				value := category.Description
				if value == "" {
					value = "Select the button below to open this ticket."
				}
				embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
					Name:   truncate(emojiPrefix(category.Emoji)+category.Label, 256),
					Value:  truncate(value, 1024),
					Inline: false,
				})
			}
		}
		if embed.Title == "" && embed.Description == "" && embed.Footer == nil && embed.Image == nil && len(embed.Fields) == 0 {
			embed.Description = "Choose a category below to open a ticket."
		}
		return &discordgo.MessageSend{
			Embeds:          []*discordgo.MessageEmbed{embed},
			Components:      ticketPanelButtons(panel),
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		}
	default:
		return &discordgo.MessageSend{
			Flags:           discordgo.MessageFlagsIsComponentsV2,
			Components:      TicketPanelMessage(panel),
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		}
	}
}

func ticketPanelPayload(panel *types.InteractivePanel) (map[string]any, error) {
	serialized, err := json.Marshal(ticketPanelSend(panel))
	if err != nil {
		return nil, fmt.Errorf("serialize ticket panel message: %w", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(serialized, &payload); err != nil {
		return nil, fmt.Errorf("decode ticket panel message: %w", err)
	}
	payload["allowed_mentions"] = ticketPanelAllowedMentions(panel.Appearance.AllowedMentions)
	flags := 0
	if panel.Appearance.SuppressEmbeds {
		flags |= 1 << 2
	}
	if panel.Appearance.SuppressNotifications {
		flags |= 1 << 12
	}
	if panel.Appearance.Format == "component" || panel.Appearance.Format == "" {
		flags |= helpers.V2Flag
		applyTicketPanelComponentIDs(panel, payload)
	}
	if flags != 0 {
		payload["flags"] = flags
	}
	return payload, nil
}

func ticketPanelAllowedMentions(config *types.PanelAllowedMentions) map[string]any {
	if config == nil {
		return map[string]any{"parse": []string{}}
	}
	users := helpers.ValidMentionIDs(config.Users)
	roles := helpers.ValidMentionIDs(config.Roles)
	parse := make([]string, 0, 3)
	seen := map[string]bool{}
	for _, kind := range config.Parse {
		if kind != "everyone" && kind != "users" && kind != "roles" || seen[kind] {
			continue
		}
		if kind == "users" && len(users) > 0 || kind == "roles" && len(roles) > 0 {
			continue
		}
		seen[kind] = true
		parse = append(parse, kind)
	}
	out := map[string]any{"parse": parse, "replied_user": config.RepliedUser}
	if len(users) > 0 {
		out["users"] = users
	}
	if len(roles) > 0 {
		out["roles"] = roles
	}
	return out
}

func applyTicketPanelComponentIDs(panel *types.InteractivePanel, payload map[string]any) {
	components, ok := payload["components"].([]any)
	if !ok || len(components) == 0 {
		return
	}
	container, ok := components[0].(map[string]any)
	if !ok {
		return
	}
	setDiscordComponentID(container, panel.Appearance.DiscordID)
	children, ok := container["components"].([]any)
	if !ok {
		return
	}
	layout := ticketPanelLayout(panel)
	position := 0
	for _, block := range layout {
		rendered := panelBlockComponents(panel, block)
		for range rendered {
			if position >= len(children) {
				return
			}
			child, ok := children[position].(map[string]any)
			if !ok {
				position++
				continue
			}
			if block.Type != "categories" && block.Type != "buttons" {
				setDiscordComponentID(child, block.DiscordID)
			}
			switch block.Type {
			case "gallery":
				if items, ok := child["items"].([]any); ok {
					for idx, item := range block.Items {
						if idx < len(items) {
							if itemPayload, ok := items[idx].(map[string]any); ok {
								setDiscordComponentID(itemPayload, item.DiscordID)
							}
						}
					}
				}
			case "section":
				if block.AccessoryType == "thumbnail" {
					if accessory, ok := child["accessory"].(map[string]any); ok {
						setDiscordComponentID(accessory, block.ThumbnailDiscordID)
					}
				}
			case "links":
				if rows, ok := child["components"].([]any); ok {
					for idx, link := range block.Links {
						if idx < len(rows) {
							if button, ok := rows[idx].(map[string]any); ok {
								setDiscordComponentID(button, link.DiscordID)
							}
						}
					}
				}
			}
			position++
		}
	}
}

func setDiscordComponentID(payload map[string]any, id uint32) {
	if id != 0 {
		payload["id"] = id
	}
}

func ticketPanelButtons(panel *types.InteractivePanel) []discordgo.MessageComponent {
	buttons := make([]discordgo.MessageComponent, 0, len(panel.Ticket.Categories))
	for _, category := range panel.Ticket.Categories {
		label := category.ButtonLabel
		if label == "" {
			label = category.Label
		}
		button := v2Button(
			fmt.Sprintf("%s:%s:%s", TicketIDOpen, panel.ID, category.ID),
			label,
			category.Emoji,
			discordgo.ButtonStyle(category.ButtonStyle),
		)
		buttons = append(buttons, button)
	}
	return v2ButtonRows(buttons)
}

func layoutHas(layout []types.PanelBlock, blockType string) bool {
	for _, b := range layout {
		if b.Type == blockType {
			return true
		}
	}
	return false
}

func ticketCategoryButtonsInline(panel *types.InteractivePanel) bool {
	if panel == nil || panel.Ticket == nil || !panel.Ticket.Settings.InlineCategoryButtons || panel.Appearance.HideCategoryList {
		return false
	}
	layout := panel.Appearance.Layout
	if len(layout) == 0 {
		layout = StandardPanelLayout(panel)
	}
	return layoutHas(layout, "categories")
}

// StandardPanelLayout is the layout of a panel that has no custom one: banner, title, description and a line, the
// category list, the footer, a line and the buttons. The line under the description is left out when there is no
// description, and the one above the buttons when there are no categories.
func StandardPanelLayout(panel *types.InteractivePanel) []types.PanelBlock {
	look := panel.Appearance
	categories := panel.Ticket.Categories
	line := types.PanelBlock{Type: "separator"}
	var out []types.PanelBlock
	if look.BannerURL != "" {
		out = append(out, types.PanelBlock{Type: "banner"})
	}
	if look.Title != "" {
		out = append(out, types.PanelBlock{Type: "title"})
	}
	if look.Description != "" {
		out = append(out, types.PanelBlock{Type: "description"}, line)
	}
	if !look.HideCategoryList && len(categories) > 0 {
		out = append(out, types.PanelBlock{Type: "categories"})
	}
	if look.Footer != "" {
		out = append(out, types.PanelBlock{Type: "footer"})
	}
	if len(categories) > 0 {
		out = append(out, line)
	}
	return append(out, types.PanelBlock{Type: "buttons"})
}

// panelBlockComponents renders one block; a block with nothing to show (an empty title, an unknown type) renders nothing.
func panelBlockComponents(panel *types.InteractivePanel, b types.PanelBlock) []discordgo.MessageComponent {
	look := panel.Appearance
	categories := panel.Ticket.Categories
	one := func(c discordgo.MessageComponent) []discordgo.MessageComponent {
		return []discordgo.MessageComponent{c}
	}

	switch b.Type {
	case "banner":
		if look.BannerURL != "" {
			return one(discordgo.MediaGallery{Items: []discordgo.MediaGalleryItem{{Media: discordgo.UnfurledMediaItem{URL: look.BannerURL}}}})
		}
	case "title":
		if look.Title != "" {
			return one(v2Text("## " + look.Title))
		}
	case "description":
		if look.Description != "" {
			return one(v2Text(look.Description))
		}
	case "categories":
		var out []discordgo.MessageComponent
		if !look.HideCategoryList {
			for _, category := range categories {
				text := fmt.Sprintf("**%s%s**", emojiPrefix(category.Emoji), category.Label)
				if category.Description != "" {
					text += "\n-# " + category.Description
				}
				if ticketCategoryButtonsInline(panel) {
					buttonLabel := category.ButtonLabel
					if buttonLabel == "" {
						buttonLabel = category.Label
					}
					button := v2Button(
						fmt.Sprintf("%s:%s:%s", TicketIDOpen, panel.ID, category.ID),
						buttonLabel,
						category.Emoji,
						discordgo.ButtonStyle(category.ButtonStyle),
					)
					out = append(out, discordgo.Section{
						Components: []discordgo.MessageComponent{v2Text(text)},
						Accessory:  button,
					})
				} else {
					out = append(out, v2Text(text))
				}
			}
		}
		return out
	case "footer":
		if look.Footer != "" {
			return one(v2Text(look.Footer))
		}
	case "buttons":
		if ticketCategoryButtonsInline(panel) {
			return nil
		}
		if len(categories) == 0 {
			return one(v2Text("-# This panel has no categories yet."))
		}
		buttons := make([]discordgo.MessageComponent, 0, len(categories))
		for _, category := range categories {
			buttonLabel := category.ButtonLabel
			if buttonLabel == "" {
				buttonLabel = category.Label
			}
			button := v2Button(fmt.Sprintf("%s:%s:%s", TicketIDOpen, panel.ID, category.ID), buttonLabel, category.Emoji, discordgo.ButtonStyle(category.ButtonStyle))
			buttons = append(buttons, button)
		}
		return v2ButtonRows(buttons)
	case "text":
		if strings.TrimSpace(b.Content) != "" {
			return one(v2Text(b.Content))
		}
	case "separator":
		divider := b.Divider == nil || *b.Divider
		return one(v2Divider(divider, b.Large))
	case "gallery":
		var items []discordgo.MediaGalleryItem
		for _, it := range b.Items {
			if it.URL == "" || len(items) == 10 {
				continue
			}
			item := discordgo.MediaGalleryItem{Media: discordgo.UnfurledMediaItem{URL: it.URL}}
			if it.Description != "" {
				description := it.Description
				item.Description = &description
			}
			items = append(items, item)
		}
		if len(items) > 0 {
			return one(discordgo.MediaGallery{Items: items})
		}
	case "section":
		if strings.TrimSpace(b.Text) == "" {
			return nil
		}
		if b.AccessoryType == "ticket_button" {
			category := FindTicketCategory(panel, b.AccessoryCategoryID)
			if category == nil {
				return one(v2Text(b.Text))
			}
			buttonLabel := category.ButtonLabel
			if buttonLabel == "" {
				buttonLabel = category.Label
			}
			button := v2Button(
				fmt.Sprintf("%s:%s:%s", TicketIDOpen, panel.ID, category.ID),
				buttonLabel,
				category.Emoji,
				discordgo.ButtonStyle(category.ButtonStyle),
			)
			return one(discordgo.Section{
				Components: []discordgo.MessageComponent{v2Text(b.Text)},
				Accessory:  button,
			})
		}
		if b.ThumbnailURL == "" {
			return one(v2Text(b.Text))
		}
		return one(discordgo.Section{
			Components: []discordgo.MessageComponent{v2Text(b.Text)},
			Accessory:  discordgo.Thumbnail{Media: discordgo.UnfurledMediaItem{URL: b.ThumbnailURL}},
		})
	case "links":
		var buttons []discordgo.MessageComponent
		for _, l := range b.Links {
			if l.URL != "" && l.Label != "" && len(buttons) < 5 {
				buttons = append(buttons, discordgo.Button{Style: discordgo.LinkButton, Label: l.Label, URL: l.URL})
			}
		}
		if len(buttons) > 0 {
			return one(v2Row(buttons...))
		}
	}
	return nil
}

// ticketDetailFields lays the ticket details out as inline columns:
// Reference / Category / Owner on the first row, Created At / Status below.
func ticketDetailFields(ticket *types.Ticket) []*discordgo.MessageEmbedField {
	return []*discordgo.MessageEmbedField{
		{Name: "Reference", Value: "#" + ticket.Ref, Inline: true},
		{Name: "Category", Value: ticket.CategoryLabel, Inline: true},
		{Name: "Owner", Value: "<@" + ticket.OwnerID + ">", Inline: true},
		{Name: "Created At", Value: fmt.Sprintf("<t:%d:R>", ticket.CreatedAt), Inline: true},
		{Name: "Status", Value: TicketStatusLabel(ticket), Inline: true},
	}
}

// TicketStatusLabel describes where a ticket currently stands.
func TicketStatusLabel(ticket *types.Ticket) string {
	label := helpers.DotYellow + " Awaiting Staff"
	switch ticket.Status {
	case types.TicketStatusClaimed:
		label = fmt.Sprintf("%s Claimed by <@%s>", helpers.DotGreen, ticket.ClaimedBy)
	case types.TicketStatusClosed:
		return helpers.DotRed + " Closed"
	}
	if ticket.Locked {
		label += " · Locked"
	}
	return label
}

func ticketWelcomeButtons(ticket *types.Ticket, panel *types.InteractivePanel) []discordgo.MessageComponent {
	closeButton := discordgo.Button{
		Label:    "Close Ticket",
		Style:    discordgo.DangerButton,
		Emoji:    &discordgo.ComponentEmoji{Name: "🔒"},
		CustomID: TicketIDClose,
	}
	buttons := []discordgo.MessageComponent{closeButton}

	if panel != nil && panel.Ticket != nil && panel.Ticket.Settings.ClaimEnabled {
		claim := discordgo.Button{
			Label:    "Claim Ticket",
			Style:    discordgo.SecondaryButton,
			Emoji:    &discordgo.ComponentEmoji{Name: "✋"},
			CustomID: TicketIDClaim,
		}
		if ticket.Status == types.TicketStatusClaimed {
			claim = discordgo.Button{
				Label:    "Unclaim",
				Style:    discordgo.SecondaryButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "↩️"},
				CustomID: TicketIDUnclaim,
			}
		}
		buttons = append(buttons, claim)
	}
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: buttons}}
}

// TicketCloseConfirmMessage asks the clicker to confirm closing the ticket.
func TicketCloseConfirmMessage(deleteOnClose bool) *discordgo.MessageEmbed {
	outcome := "locked and archived"
	if deleteOnClose {
		outcome = "deleted"
	}
	return &discordgo.MessageEmbed{
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: helpers.ThumbGuild},
		Title:       "Close Ticket",
		Description: fmt.Sprintf("Are you sure you want to close this ticket? A transcript of the conversation will be saved and this thread will be %s.", outcome),
		Color:       helpers.ColorCaution,
	}
}

// TicketCloseConfirmButtons are the Yes / Cancel buttons under the close prompt.
func TicketCloseConfirmButtons() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Yes, Close",
				Style:    discordgo.DangerButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "🔒"},
				CustomID: TicketIDCloseYes,
			},
			discordgo.Button{
				Label:    "Cancel",
				Style:    discordgo.SecondaryButton,
				CustomID: TicketIDCloseNo,
			},
		}},
	}
}

// TicketTranscriptMessage is the V2 card for a closed ticket (the title carries its own markdown heading): the summary
// lines plus the transcript file attached inside the same container.
func TicketTranscriptMessage(color int, title, description string, lines []string, transcriptName string) []discordgo.MessageComponent {
	inner := []discordgo.MessageComponent{v2Text(title + "\n" + description)}
	if len(lines) > 0 {
		inner = append(inner, v2Text(strings.Join(lines, "\n")))
	}
	inner = append(inner,
		v2Text("You can download the transcript of this ticket below."),
		discordgo.FileComponent{File: discordgo.UnfurledMediaItem{URL: "attachment://" + transcriptName}},
	)
	return []discordgo.MessageComponent{v2Container(color, inner...)}
}

// TicketClosedLines is the summary shared by the closed-ticket log and the owner's DM.
func TicketClosedLines(ticket *types.Ticket, guildName string) []string {
	closedBy := "<@" + ticket.ClosedBy + ">"
	if ticket.ClosedBy == inactivityCloser {
		closedBy = "Inactivity policy"
	}
	lines := []string{
		"- **Thread:** `" + ticket.ThreadName + "`",
		"- **Server:** `" + guildName + "`",
		"- **Closed By:** " + closedBy,
	}
	return lines
}

// ============================================================================
// Panel Publishing
