package tickets

import (
	"crypto/rand"
	"fmt"
	"log"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================

// SortedTicketPanels returns the guild's ticket panels, oldest first.
func SortedTicketPanels(ip *types.InteractivePanels) []*types.InteractivePanel {
	panels := make([]*types.InteractivePanel, 0, len(ip.Panels))
	for _, panel := range ip.Panels {
		if panel != nil && panel.Type == types.PanelTypeTicket {
			panels = append(panels, panel)
		}
	}
	sort.Slice(panels, func(a, b int) bool {
		if panels[a].CreatedAt != panels[b].CreatedAt {
			return panels[a].CreatedAt < panels[b].CreatedAt
		}
		return panels[a].ID < panels[b].ID
	})
	return panels
}

// FindTicketCategory returns the category with the given ID, or nil.
func FindTicketCategory(panel *types.InteractivePanel, categoryID string) *types.TicketCategory {
	if panel == nil || panel.Ticket == nil {
		return nil
	}
	for _, category := range panel.Ticket.Categories {
		if category.ID == categoryID {
			return category
		}
	}
	return nil
}

func OpenTicketsFor(ip *types.InteractivePanels, userID string) []*types.Ticket {
	var owned []*types.Ticket
	for _, ticket := range ip.Tickets.Open {
		if ticket.OwnerID == userID {
			owned = append(owned, ticket)
		}
	}
	return owned
}

// TicketBlacklistEntryFor returns an active blacklist record for userID.
func TicketBlacklistEntryFor(ip *types.InteractivePanels, userID string) *types.TicketBlacklistEntry {
	now := time.Now().Unix()
	for _, entry := range ip.Tickets.Blacklist {
		if entry != nil && entry.UserID == userID && (entry.ExpiresAt == 0 || entry.ExpiresAt > now) {
			return entry
		}
	}
	return nil
}

// StaffStatsFor returns (creating if needed) a staff member's leaderboard row.
func StaffStatsFor(ip *types.InteractivePanels, staffID string) *types.TicketStaffStats {
	stats, ok := ip.Tickets.Stats[staffID]
	if !ok {
		stats = &types.TicketStaffStats{}
		ip.Tickets.Stats[staffID] = stats
	}
	return stats
}

// TicketLeaderboardRow is defined in package types (tickets.go).
type TicketLeaderboardRow = types.TicketLeaderboardRow

// ArchiveTicket moves a closed ticket out of Open and into the history. Nothing is trimmed: the ticket tables keep
// every closed ticket, and only the most recent MaxTicketHistory are loaded.
func ArchiveTicket(ip *types.InteractivePanels, ticket *types.Ticket) {
	delete(ip.Tickets.Open, ticket.ThreadID)
	ip.Tickets.History = append(ip.Tickets.History, ticket)
}

// NextTicketIdentity reserves the next ticket number and a unique reference.
func NextTicketIdentity(ip *types.InteractivePanels) (number int, ref string) {
	ip.Tickets.Counter++
	number = ip.Tickets.Counter
	for {
		ref = randomID(refAlphabet, 6)
		if !ticketRefTaken(ip, ref) {
			return number, ref
		}
	}
}

func ticketRefTaken(ip *types.InteractivePanels, ref string) bool {
	for _, ticket := range ip.Tickets.Open {
		if ticket.Ref == ref {
			return true
		}
	}
	for _, ticket := range ip.Tickets.History {
		if ticket.Ref == ref {
			return true
		}
	}
	return false
}

// randomID returns n random characters drawn from alphabet.
func randomID(alphabet string, n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%0*d", n, time.Now().UnixNano()%1_000_000)[:n]
	}
	var out strings.Builder
	for _, b := range buf {
		out.WriteByte(alphabet[int(b)%len(alphabet)])
	}
	return out.String()
}

// ============================================================================
// Ticket Lifecycle: Open
// ============================================================================

func TicketPublishPanelScreen(panels []*types.InteractivePanel) []discordgo.MessageComponent {
	inner := []discordgo.MessageComponent{
		v2Text("## Publish a Ticket Panel\nCreate and customize ticket panels in the Ranked Bedwars Dashboard. This Discord command only publishes an existing panel; to make changes, use the dashboard."),
		v2Divider(true, false),
	}
	if len(panels) == 0 {
		inner = append(inner, v2Text("No ticket panels are configured yet. Create one in the dashboard, then use `/ticket panel` to publish it."))
		return []discordgo.MessageComponent{v2Container(helpers.ColorDefault, inner...)}
	}

	options := make([]discordgo.SelectMenuOption, 0, len(panels))
	for _, panel := range panels {
		options = append(options, discordgo.SelectMenuOption{
			Label:       truncate(panel.Name, 100),
			Value:       panel.ID,
			Description: fmt.Sprintf("%d categories", len(panel.Ticket.Categories)),
		})
	}
	inner = append(inner,
		v2Text("**1. Choose the panel to publish**"),
		v2Row(discordgo.SelectMenu{
			CustomID:    "tkt:publish:panel",
			Placeholder: "Select a dashboard ticket panel",
			Options:     options,
		}),
	)
	return []discordgo.MessageComponent{v2Container(helpers.ColorDefault, inner...)}
}

// TicketPublishChannelScreen asks where to send the selected dashboard panel.
func TicketPublishChannelScreen(panel *types.InteractivePanel) []discordgo.MessageComponent {
	one := 1
	inner := []discordgo.MessageComponent{
		v2Text(fmt.Sprintf("## Publish %s\nCreate and customize ticket panels in the Ranked Bedwars Dashboard. This command only publishes panels; make future changes in the dashboard.", panel.Name)),
		v2Divider(true, false),
		v2Text("**1. Panel:** " + panel.Name + "\n**2. Choose the channel to send it to**"),
		v2Row(discordgo.SelectMenu{
			MenuType:    discordgo.ChannelSelectMenu,
			CustomID:    "tkt:publish:channel:" + panel.ID,
			Placeholder: "Select a channel",
			MinValues:   &one,
			MaxValues:   1,
			ChannelTypes: []discordgo.ChannelType{
				discordgo.ChannelTypeGuildText,
				discordgo.ChannelTypeGuildNews,
			},
		}),
	}
	return []discordgo.MessageComponent{v2Container(helpers.ColorDefault, inner...)}
}

func TicketPublishChannelErrorScreen(panel *types.InteractivePanel, notice string) []discordgo.MessageComponent {
	components := TicketPublishChannelScreen(panel)
	container, ok := components[0].(discordgo.Container)
	if !ok {
		return components
	}
	container.Components = append([]discordgo.MessageComponent{v2Text(notice)}, container.Components...)
	return []discordgo.MessageComponent{container}
}

// HandleTicketComponent routes every `tkt:` button and select menu.
func HandleTicketComponent(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, TicketIDPrefix) {
		return false
	}
	parts := strings.Split(customID, ":")
	if len(parts) < 2 {
		return true
	}

	switch parts[1] {
	case "open":
		if len(parts) == 4 {
			handleTicketOpen(s, i, parts[2], parts[3])
		}
	case "publish":
		handleTicketPublish(s, i, parts)
	case "claim":
		handleTicketClaim(s, i, true)
	case "unclaim":
		handleTicketClaim(s, i, false)
	case "close":
		if len(parts) == 2 {
			handleTicketCloseAsk(s, i)
		} else {
			handleTicketCloseAnswer(s, i, parts[2] == "yes")
		}
	}
	return true
}

func handleTicketPublish(s *discordgo.Session, i *discordgo.InteractionCreate, parts []string) {
	if !InteractionIsAdmin(i) {
		_ = RespondEmbed(s, i, TicketPermissionDeniedEmbed("Only administrators can publish ticket panels. Create and edit panels in the dashboard."))
		return
	}
	if len(parts) == 3 && parts[2] == "panel" {
		ip, err := Tickets.Load(i.GuildID)
		if err != nil {
			_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
			return
		}
		panel := ip.Panels[firstValue(i)]
		if panel == nil || panel.Ticket == nil {
			_ = UpdateV2(s, i, TicketPublishPanelScreen(SortedTicketPanels(ip))...)
			return
		}
		_ = UpdateV2(s, i, TicketPublishChannelScreen(panel)...)
		return
	}
	if len(parts) != 4 || parts[2] != "channel" {
		return
	}
	panelID := parts[3]
	channelID := firstValue(i)
	if channelID == "" {
		_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
		return
	}

	ip, err := Tickets.Load(i.GuildID)
	if err != nil {
		_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
		return
	}
	panel := ip.Panels[panelID]
	if panel == nil || panel.Ticket == nil {
		_ = UpdateV2(s, i, TicketPublishPanelScreen(SortedTicketPanels(ip))...)
		return
	}
	if len(panel.Ticket.Categories) == 0 {
		_ = UpdateV2(s, i, TicketPublishChannelErrorScreen(panel, "This panel has no ticket categories. Add categories in the dashboard before publishing.")...)
		return
	}

	payload, err := ticketPanelPayload(panel)
	if err != nil {
		_ = UpdateV2(s, i, TicketPublishChannelErrorScreen(panel, "Could not build the ticket panel message. Please review its configuration and try again.")...)
		return
	}
	messageID, err := helpers.SendPayload(s, channelID, payload)
	if err != nil {
		_ = UpdateV2(s, i, TicketPublishChannelErrorScreen(panel, "Could not publish the panel there. Check that I can view and send messages in the selected channel.")...)
		return
	}
	oldChannelID, oldMessageID := panel.ChannelID, panel.MessageID
	err = Tickets.Update(helpers.InteractionUserID(i), i.GuildID, func(current *types.InteractivePanels) error {
		stored := current.Panels[panelID]
		if stored == nil || stored.Ticket == nil {
			return ErrTicketPanelNotFound
		}
		stored.ChannelID = channelID
		stored.MessageID = messageID
		stored.UpdatedAt = time.Now().Unix()
		return nil
	})
	if err != nil {
		if deleteErr := s.ChannelMessageDelete(channelID, messageID); deleteErr != nil {
			log.Printf("[Tickets] could not remove unpublished panel message %s in channel %s after config save failed: %v", messageID, channelID, deleteErr)
		}
		_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
		return
	}
	if oldChannelID != "" && oldMessageID != "" && (oldChannelID != channelID || oldMessageID != messageID) {
		if err := s.ChannelMessageDelete(oldChannelID, oldMessageID); err != nil {
			log.Printf("[Tickets] could not remove previous published panel message %s in channel %s: %v", oldMessageID, oldChannelID, err)
		}
	}
	_ = UpdateV2(s, i, []discordgo.MessageComponent{
		v2Container(helpers.ColorSuccess,
			v2Text(fmt.Sprintf("## Panel Published\n**%s** was sent to <#%s>.\n\nTo change the panel’s content, categories, appearance, or settings, make those changes in the Ranked Bedwars Dashboard. `/ticket panel` is only for publishing an existing panel.", panel.Name, channelID)),
		),
	}...)
}

// handleTicketOpen creates a ticket from a panel button.
func handleTicketOpen(s *discordgo.Session, i *discordgo.InteractionCreate, panelID, categoryID string) {
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	owner := i.Member.User
	if owner.Bot {
		helpers.EditComponentEmbed(s, i, TicketBotTargetEmbed())
		return
	}

	ticket, err := OpenTicket(s, i.GuildID, panelID, categoryID, owner, i.ChannelID)
	if err != nil {
		helpers.EditComponentEmbed(s, i, ticketOpenErrorEmbed(err))
		return
	}
	helpers.EditComponentEmbed(s, i, ticketCreatedEmbed(ticket.ThreadID))
}

// handleTicketClaim claims or releases the ticket the button is in.
func handleTicketClaim(s *discordgo.Session, i *discordgo.InteractionCreate, claim bool) {
	ctx, err := LoadTicketContext(i.GuildID, i.ChannelID)
	if err != nil {
		_ = RespondEmbed(s, i, TicketContextErrorEmbed(err))
		return
	}
	if !IsTicketStaff(i.Member, ctx.Panel, ctx.Category) {
		_ = RespondEmbed(s, i, TicketPermissionDeniedEmbed("Only support staff can claim tickets."))
		return
	}

	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})

	staffID := helpers.InteractionUserID(i)
	if claim {
		_, err = ClaimTicket(s, i.GuildID, i.ChannelID, staffID)
	} else {
		_, err = UnclaimTicket(s, i.GuildID, i.ChannelID, staffID, InteractionIsAdmin(i))
	}
	if err != nil {
		helpers.FollowupEmbed(s, i, TicketActionErrorEmbed(err))
		return
	}

	title, description := "Ticket Claimed", fmt.Sprintf("<@%s> claimed this ticket and will be assisting you.", staffID)
	presetType := "success"
	if !claim {
		title, description = "Ticket Unclaimed", fmt.Sprintf("<@%s> released this ticket. Another staff member will pick it up.", staffID)
		presetType = "caution"
	}
	_, _ = s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{helpers.Preset(presetType, types.PresetOptions{
			Title:       title,
			Description: description,
		})},
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}},
	})
}

// handleTicketCloseAsk shows the "are you sure" prompt only the clicker can see.
func handleTicketCloseAsk(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx, err := LoadTicketContext(i.GuildID, i.ChannelID)
	if err != nil {
		_ = RespondEmbed(s, i, TicketContextErrorEmbed(err))
		return
	}
	if !CanCloseTicket(i, ctx) {
		_ = RespondEmbed(s, i, TicketPermissionDeniedEmbed("Only support staff can close this ticket."))
		return
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:      discordgo.MessageFlagsEphemeral,
			Embeds:     []*discordgo.MessageEmbed{TicketCloseConfirmMessage(ctx.Panel.Ticket.Settings.DeleteOnClose)},
			Components: TicketCloseConfirmButtons(),
		},
	})
}

// handleTicketCloseAnswer finishes or cancels a close the clicker was asked about.
func handleTicketCloseAnswer(s *discordgo.Session, i *discordgo.InteractionCreate, confirmed bool) {
	if !confirmed {
		_ = RespondEmbed(s, i, helpers.Preset("info", types.PresetOptions{
			Title:       "Close Cancelled",
			Description: "The ticket stays open.",
			Thumbnail:   helpers.ThumbGuild,
		}))
		return
	}

	ctx, err := LoadTicketContext(i.GuildID, i.ChannelID)
	if err != nil {
		_ = RespondEmbed(s, i, TicketContextErrorEmbed(err))
		return
	}
	if !CanCloseTicket(i, ctx) {
		_ = RespondEmbed(s, i, TicketPermissionDeniedEmbed("Only support staff can close this ticket."))
		return
	}

	_ = RespondEmbed(s, i, helpers.Preset("info", types.PresetOptions{
		Title:       "Closing Ticket",
		Description: "Saving the transcript. This only takes a moment.",
		Thumbnail:   helpers.ThumbGuild,
	}))

	closerIsStaff := IsTicketStaff(i.Member, ctx.Panel, ctx.Category)
	if _, err := CloseTicket(s, i.GuildID, i.ChannelID, helpers.InteractionUserID(i), closerIsStaff); err != nil {
		_ = RespondEmbed(s, i, helpers.Preset("error", types.PresetOptions{
			Title:       "Unexpected Error",
			Description: "The ticket could not be closed. Please try again later or contact support.",
			Thumbnail:   helpers.ThumbFlag,
		}))
		return
	}
	_ = RespondEmbed(s, i, helpers.Preset("success", types.PresetOptions{
		Title:       "Ticket Closed",
		Description: "The transcript has been saved and this ticket is being closed.",
		Thumbnail:   helpers.ThumbGuild,
	}))
}

func selectedValues(i *discordgo.InteractionCreate) []string {
	return i.MessageComponentData().Values
}

// firstValue returns the first selected value, or "" when the menu was cleared.
func firstValue(i *discordgo.InteractionCreate) string {
	if values := selectedValues(i); len(values) > 0 {
		return values[0]
	}
	return ""
}

// ============================================================================
// Embed Builders
