package tickets

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

// A ticket's channel can outlive its record: the bot creates the channel first and saves the ticket last, so a failed save (the
// database being down for a moment) leaves a channel that every ticket command answers "Not A Ticket" for. When a command is
// run in such a channel the record is rebuilt from the channel itself instead of the channel being thrown away.

var (
	welcomeOwnerRe  = regexp.MustCompile(`^<@!?(\d{15,25})>`)
	channelNumberRe = regexp.MustCompile(`^(\d{1,9})-`)
	refRe           = regexp.MustCompile(`^[A-Z0-9]{6}$`)
)

// LoadOrAdoptTicketContext is LoadTicketContext, except that a channel which the bot opened as a ticket but has no record
// gets one first.
func LoadOrAdoptTicketContext(s *discordgo.Session, guildID, channelID string) (*TicketContext, error) {
	ctx, err := LoadTicketContext(guildID, channelID)
	if !errors.Is(err, errNotTicketChannel) {
		return ctx, err
	}
	if adoptErr := adoptTicketChannel(s, guildID, channelID); adoptErr != nil {
		if !errors.Is(adoptErr, errNotTicketChannel) {
			log.Printf("[Tickets] could not rebuild the record of channel %s in guild %s: %v", channelID, guildID, adoptErr)
		}
		return nil, err
	}
	return LoadTicketContext(guildID, channelID)
}

// adoptTicketChannel records a ticket for the channel. It only does so when the channel holds the welcome message this bot
// posts when it opens a ticket (it starts by pinging the owner), so an ordinary channel is never turned into a ticket.
func adoptTicketChannel(s *discordgo.Session, guildID, channelID string) error {
	if s == nil || s.State == nil || s.State.User == nil {
		return errNotTicketChannel
	}
	channel, err := s.Channel(channelID)
	if err != nil || channel.GuildID != guildID {
		return errNotTicketChannel
	}
	messages, err := s.ChannelMessages(channelID, 100, "", "0", "") // the oldest ones: the welcome message is the first
	if err != nil {
		return err
	}
	var welcome *discordgo.Message
	var ownerID string
	for _, message := range messages {
		if message.Author == nil || message.Author.ID != s.State.User.ID {
			continue
		}
		match := welcomeOwnerRe.FindStringSubmatch(message.Content)
		if match == nil {
			continue
		}
		if welcome == nil || snowflakeBefore(message.ID, welcome.ID) {
			welcome, ownerID = message, match[1]
		}
	}
	if welcome == nil {
		return errNotTicketChannel
	}

	ip, err := Tickets.LoadOpen(guildID)
	if err != nil {
		return err
	}
	panel, category := matchTicketCategory(ip, channel, welcome)
	if panel == nil || category == nil {
		return fmt.Errorf("no ticket category matches channel %s", channelID)
	}

	ownerName := ownerID
	if user, err := s.User(ownerID); err == nil && user != nil {
		ownerName = user.Username
	}
	mode := types.TicketModeChannel
	if channel.IsThread() {
		mode = types.TicketModeThread
	}
	opened := int64(0)
	if at, err := discordgo.SnowflakeTimestamp(channelID); err == nil {
		opened = at.Unix()
	}
	if opened == 0 {
		opened = time.Now().Unix()
	}

	return Tickets.Update(ownerID, guildID, func(current *types.InteractivePanels) error {
		if current.Tickets.Open[channelID] != nil {
			return nil // another command rebuilt it meanwhile
		}
		number, ref := 0, welcomeReference(welcome)
		if m := channelNumberRe.FindStringSubmatch(channel.Name); m != nil {
			number, _ = strconv.Atoi(m[1])
		}
		if number == 0 || ticketNumberTaken(current, number) {
			number, _ = NextTicketIdentity(current)
		}
		for ref == "" || ticketRefTaken(current, ref) {
			ref = randomID(refAlphabet, 6)
		}
		ticket := &types.Ticket{
			Number:           number,
			Ref:              ref,
			PanelID:          panel.ID,
			CategoryID:       category.ID,
			CategoryLabel:    category.Label,
			OwnerID:          ownerID,
			OwnerName:        ownerName,
			Status:           types.TicketStatusOpen,
			Mode:             mode,
			ThreadID:         channelID,
			ThreadName:       channel.Name,
			WelcomeMessageID: welcome.ID,
			CreatedAt:        opened,
			LastActivityAt:   time.Now().Unix(),
		}
		appendTicketLog(ticket, "opened", ownerID, "Ticket opened.", opened)
		appendTicketLog(ticket, "restored", ownerID, "The ticket's record was rebuilt from its channel.", time.Now().Unix())
		current.Tickets.Open[channelID] = ticket
		return nil
	})
}

// matchTicketCategory finds the panel and category the ticket's welcome message came from: the category whose name appears in
// the message (its title, text or fields) or in the channel's name. Of several panels with such a category, the one whose
// category puts channels under the channel's own parent wins.
func matchTicketCategory(ip *types.InteractivePanels, channel *discordgo.Channel, welcome *discordgo.Message) (*types.InteractivePanel, *types.TicketCategory) {
	var text strings.Builder
	text.WriteString(strings.ToLower(channel.Name) + "\n")
	for _, embed := range welcome.Embeds {
		text.WriteString(strings.ToLower(embed.Title+"\n"+embed.Description) + "\n")
		for _, field := range embed.Fields {
			text.WriteString(strings.ToLower(field.Name+" "+field.Value) + "\n")
		}
	}
	haystack := text.String()
	var bestPanel *types.InteractivePanel
	var bestCategory *types.TicketCategory
	for _, panel := range ip.Panels {
		if panel == nil || panel.Ticket == nil || isCommandPanel(panel) {
			continue
		}
		for _, category := range panel.Ticket.Categories {
			label := strings.ToLower(strings.TrimSpace(category.Label))
			if label == "" || !strings.Contains(haystack, label) {
				continue
			}
			if bestCategory == nil || (category.CategoryID != "" && category.CategoryID == channel.ParentID) {
				bestPanel, bestCategory = panel, category
			}
		}
	}
	return bestPanel, bestCategory
}

// welcomeReference reads the "#REF" the standard welcome message shows ("" when it does not).
func welcomeReference(welcome *discordgo.Message) string {
	for _, embed := range welcome.Embeds {
		for _, field := range embed.Fields {
			if strings.EqualFold(strings.TrimSpace(field.Name), "reference") {
				if ref := strings.ToUpper(strings.Trim(strings.TrimSpace(field.Value), "#` ")); refRe.MatchString(ref) {
					return ref
				}
			}
		}
	}
	return ""
}

// snowflakeBefore reports whether Discord id a is older than b (ids grow with time).
func snowflakeBefore(a, b string) bool {
	x, errA := strconv.ParseUint(a, 10, 64)
	y, errB := strconv.ParseUint(b, 10, 64)
	return errA == nil && errB == nil && x < y
}

func ticketNumberTaken(ip *types.InteractivePanels, number int) bool {
	for _, ticket := range ip.Tickets.Open {
		if ticket.Number == number {
			return true
		}
	}
	for _, ticket := range ip.Tickets.History {
		if ticket.Number == number {
			return true
		}
	}
	return false
}
