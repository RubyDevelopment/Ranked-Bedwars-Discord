package helpers

import (
	"bytes"
	"ranked-bedwars/src/discord/helpers/guildlink"
	"ranked-bedwars/src/types"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================

// IsPrefixInteraction checks if an interaction is from a prefix command.
func IsPrefixInteraction(i *discordgo.InteractionCreate) bool {
	return i != nil && i.Interaction != nil && strings.HasPrefix(i.Interaction.ID, "prefix:")
}

// PrefixSourceMessageID extracts the original message ID from a prefix interaction.
func PrefixSourceMessageID(i *discordgo.InteractionCreate) string {
	if i == nil || i.Interaction == nil || !IsPrefixInteraction(i) {
		return ""
	}
	parts := strings.SplitN(i.Interaction.ID, ":", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// MarkDeferredSlashInteraction marks a slash interaction as deferred.
func MarkDeferredSlashInteraction(i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil {
		return
	}
	deferredSlashInteractions.Store(i.Interaction.ID, struct{}{})
}

// ClearDeferredSlashInteraction clears the deferred state of a slash interaction.
func ClearDeferredSlashInteraction(i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil {
		return
	}
	deferredSlashInteractions.Delete(i.Interaction.ID)
}

// IsDeferredSlashInteraction checks if a slash interaction is deferred.
func IsDeferredSlashInteraction(i *discordgo.InteractionCreate) bool {
	if i == nil || i.Interaction == nil {
		return false
	}
	_, ok := deferredSlashInteractions.Load(i.Interaction.ID)
	return ok
}

// chainedPrefixReplies holds, for prefix commands that asked for it, the reply sent so far (nil until the first one), so the next reply
// edits it instead of posting another, as a slash command's deferred response is edited.
var chainedPrefixReplies sync.Map // interaction id -> *discordgo.Message

// ChainPrefixReplies makes every reply of this prefix command after the first edit the first one. Slash commands already work that way.
func ChainPrefixReplies(i *discordgo.InteractionCreate) {
	if !IsPrefixInteraction(i) {
		return
	}
	id := i.Interaction.ID
	chainedPrefixReplies.Store(id, (*discordgo.Message)(nil))
	time.AfterFunc(10*time.Minute, func() { chainedPrefixReplies.Delete(id) })
}

// sendPrefixReply sends a reply to a prefix command message.
func sendPrefixReply(s *discordgo.Session, i *discordgo.InteractionCreate, content string, embeds []*discordgo.MessageEmbed, components ...discordgo.MessageComponent) error {
	messageID := PrefixSourceMessageID(i)
	if messageID == "" {
		return nil
	}
	content = strings.TrimSpace(content)
	trimEmbeds(embeds)
	if v, chained := chainedPrefixReplies.Load(i.Interaction.ID); chained {
		if first, _ := v.(*discordgo.Message); first != nil {
			if components == nil {
				components = []discordgo.MessageComponent{} // an edit keeps old buttons unless told there are none
			}
			_, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
				ID: first.ID, Channel: first.ChannelID, Content: &content, Embeds: &embeds, Components: &components,
			})
			return err
		}
		sent, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
			Content: content, Embeds: embeds, Components: components,
			Reference: &discordgo.MessageReference{MessageID: messageID, ChannelID: i.ChannelID, GuildID: guildlink.ReplyGuild(i.ID, i.GuildID)},
		})
		if err == nil {
			chainedPrefixReplies.Store(i.Interaction.ID, sent)
		}
		return err
	}
	_, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
		Content:    content,
		Embeds:     embeds,
		Components: components,
		Reference: &discordgo.MessageReference{
			MessageID: messageID,
			ChannelID: i.ChannelID,
			GuildID:   guildlink.ReplyGuild(i.ID, i.GuildID),
		},
	})
	return err
}

// EditInteractionFile answers with a PNG attachment (and optional text), for slash and prefix commands alike.
func EditInteractionFile(s *discordgo.Session, i *discordgo.InteractionCreate, content, name string, data []byte, components ...discordgo.MessageComponent) error {
	content = strings.TrimSpace(content)
	file := &discordgo.File{Name: name, ContentType: "image/png", Reader: bytes.NewReader(data)}
	if IsPrefixInteraction(i) {
		messageID := PrefixSourceMessageID(i)
		if messageID == "" {
			return nil
		}
		_, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
			Content:    content,
			Files:      []*discordgo.File{file},
			Components: components,
			Reference:  &discordgo.MessageReference{MessageID: messageID, ChannelID: i.ChannelID, GuildID: guildlink.ReplyGuild(i.ID, i.GuildID)},
		})
		return err
	}
	if IsDeferredSlashInteraction(i) {
		_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content, Files: []*discordgo.File{file}, Components: &components})
		return err
	}
	return Respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content, Files: []*discordgo.File{file}, Components: components},
	})
}

// ============================================================================
// Prefix Command Building
// ============================================================================

// BuildPrefixInteraction converts a message to an interaction format.
func BuildPrefixInteraction(s *discordgo.Session, message *discordgo.Message, cmd *types.Command, route *types.PrefixCommandRoute) *discordgo.InteractionCreate {
	data := discordgo.ApplicationCommandInteractionData{Name: cmd.Name}
	args := []string{}
	if route != nil {
		args = append(args, route.RemainingArgs...)
	}

	if cmd != nil && cmd.Data != nil {
		if route != nil && route.SubcommandPath != "" {
			data.Options = buildPrefixSubcommandOptions(message.GuildID, cmd.Data.Options, route.SubcommandPath, args)
		} else {
			data.Options = buildPrefixLeafOptions(message.GuildID, cmd.Data.Options, args)
		}
	}

	resolvePrefixUserNames(message.GuildID, data.Options)
	addPrefixAttachments(&data, cmd, route, message.Attachments)
	addPrefixGameDefault(&data, cmd, route, message.ChannelID)
	user := message.Author
	if user == nil {
		user = &discordgo.User{}
	}
	member := prefixMessageMember(s, message, user)
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "prefix:" + message.ID + ":" + cmd.Name,
			Type:      discordgo.InteractionApplicationCommand,
			GuildID:   message.GuildID,
			ChannelID: message.ChannelID,
			Member:    member,
			User:      user,
			Data:      data,
		},
	}
}

func buildPrefixSubcommandOptions(guildID string, options []*discordgo.ApplicationCommandOption, subPath string, args []string) []*discordgo.ApplicationCommandInteractionDataOption {
	parts := strings.Split(subPath, ".")
	if len(parts) == 1 {
		return []*discordgo.ApplicationCommandInteractionDataOption{{
			Type:    discordgo.ApplicationCommandOptionSubCommand,
			Name:    parts[0],
			Options: buildPrefixLeafOptions(guildID, findPrefixSubcommandOptions(options, parts[0]), args),
		}}
	}
	if len(parts) == 2 {
		return []*discordgo.ApplicationCommandInteractionDataOption{{
			Type: discordgo.ApplicationCommandOptionSubCommandGroup,
			Name: parts[0],
			Options: []*discordgo.ApplicationCommandInteractionDataOption{{
				Type:    discordgo.ApplicationCommandOptionSubCommand,
				Name:    parts[1],
				Options: buildPrefixLeafOptions(guildID, findPrefixGroupedSubcommandOptions(options, parts[0], parts[1]), args),
			}},
		}}
	}
	return nil
}

func findPrefixSubcommandOptions(options []*discordgo.ApplicationCommandOption, name string) []*discordgo.ApplicationCommandOption {
	for _, opt := range options {
		if opt.Type == discordgo.ApplicationCommandOptionSubCommand && opt.Name == name {
			return opt.Options
		}
	}
	return nil
}

func findPrefixGroupedSubcommandOptions(options []*discordgo.ApplicationCommandOption, groupName, subName string) []*discordgo.ApplicationCommandOption {
	for _, opt := range options {
		if opt.Type != discordgo.ApplicationCommandOptionSubCommandGroup || opt.Name != groupName {
			continue
		}
		for _, sub := range opt.Options {
			if sub.Type == discordgo.ApplicationCommandOptionSubCommand && sub.Name == subName {
				return sub.Options
			}
		}
	}
	return nil
}

// buildPrefixLeafOptions maps space-split prefix args onto slash options. The options that can be told apart by what
// they look like (a mention or ID for the user, 3d / perm for the duration, a ladder level's name) are taken wherever
// they sit, so "=mute <user> 3d <level> <reason>" and "=mute <reason> 3d <user>" both work; the rest go in order.
func buildPrefixLeafOptions(guildID string, options []*discordgo.ApplicationCommandOption, args []string) []*discordgo.ApplicationCommandInteractionDataOption {
	leaf := make([]*discordgo.ApplicationCommandOption, 0, len(options))
	for _, opt := range options {
		if opt == nil {
			continue
		}
		// Attachments come from the message itself (see addPrefixAttachments), never from a typed word.
		if opt.Type == discordgo.ApplicationCommandOptionSubCommand || opt.Type == discordgo.ApplicationCommandOptionSubCommandGroup || opt.Type == discordgo.ApplicationCommandOptionAttachment {
			continue
		}
		leaf = append(leaf, opt)
	}
	if len(leaf) == 0 || len(args) == 0 {
		return nil
	}
	return assignPrefixOptions(guildID, leaf, args)
}

func prefixOptionValue(opt *discordgo.ApplicationCommandOption, value string) *discordgo.ApplicationCommandInteractionDataOption {
	parsed := &discordgo.ApplicationCommandInteractionDataOption{
		Name:  opt.Name,
		Type:  opt.Type,
		Value: value,
	}
	switch opt.Type {
	case discordgo.ApplicationCommandOptionUser:
		parsed.Value = strings.Trim(value, "<@!>")
	case discordgo.ApplicationCommandOptionChannel:
		parsed.Value = strings.Trim(value, "<#>")
	case discordgo.ApplicationCommandOptionRole:
		parsed.Value = strings.Trim(value, "<@&>")
	case discordgo.ApplicationCommandOptionInteger:
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			parsed.Value = float64(n)
		}
	case discordgo.ApplicationCommandOptionNumber:
		if n, err := strconv.ParseFloat(value, 64); err == nil {
			parsed.Value = n
		}
	case discordgo.ApplicationCommandOptionBoolean:
		switch strings.ToLower(value) {
		case "true", "yes", "1", "on", "enable", "enabled":
			parsed.Value = true
		default:
			parsed.Value = false
		}
	case discordgo.ApplicationCommandOptionString:
		if len(opt.Choices) > 0 {
			for _, choice := range opt.Choices {
				if strings.EqualFold(choice.Name, value) {
					parsed.Value = choice.Value
					break
				}
				if str, ok := choice.Value.(string); ok && strings.EqualFold(str, value) {
					parsed.Value = choice.Value
					break
				}
			}
		}
	}
	return parsed
}

func prefixMessageMember(s *discordgo.Session, message *discordgo.Message, user *discordgo.User) *discordgo.Member {
	if message == nil || user == nil {
		return nil
	}
	if message.Member != nil {
		member := *message.Member
		if member.User == nil {
			member.User = user
		}
		return &member
	}
	if s != nil && message.GuildID != "" {
		if member, err := s.GuildMember(message.GuildID, user.ID); err == nil && member != nil {
			if member.User == nil {
				member.User = user
			}
			return member
		}
	}
	return &discordgo.Member{User: user, Roles: []string{}}
}

// ============================================================================
// Rank Utility Functions
// resolvePrefixUserNames turns a user option typed as a player's Minecraft username or nickname (=call nexus720) into
// their Discord ID, in every command and subcommand alike. Mentions and IDs are left as they are; a name that matches
// no registered player is left too, so the command reports no such player.
func resolvePrefixUserNames(guildID string, options []*discordgo.ApplicationCommandInteractionDataOption) {
	for _, opt := range options {
		if opt.Type == discordgo.ApplicationCommandOptionUser {
			if raw, ok := opt.Value.(string); ok && !prefixSnowflakePattern.MatchString(raw) {
				if user := userByPlayerName(guildID, raw); user != nil {
					opt.Value = user.ID
				}
			}
		}
		resolvePrefixUserNames(guildID, opt.Options)
	}
}

// OptionUserUnresolved reports whether a prefix command named a player (a Minecraft username or nickname) that no
// registered player of the guild has, so the option holds no user.
func OptionUserUnresolved(i *discordgo.InteractionCreate, name string) bool {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionUser {
			raw, ok := opt.Value.(string)
			return ok && !prefixSnowflakePattern.MatchString(raw)
		}
	}
	return false
}

// addPrefixAttachments gives a prefix command the files attached to its message, so "=submit" with a screenshot works like
// /submit with the screenshot option: each attachment option takes the next attached file, in the order they were sent.
func addPrefixAttachments(data *discordgo.ApplicationCommandInteractionData, cmd *types.Command, route *types.PrefixCommandRoute, files []*discordgo.MessageAttachment) {
	if cmd == nil || cmd.Data == nil || len(files) == 0 {
		return
	}
	declared, target, ok := prefixLeaf(data, cmd, route)
	if !ok {
		return
	}
	for _, opt := range declared {
		if opt == nil || opt.Type != discordgo.ApplicationCommandOptionAttachment {
			continue
		}
		if len(files) == 0 {
			break
		}
		file := files[0]
		files = files[1:]
		if data.Resolved == nil {
			data.Resolved = &discordgo.ApplicationCommandInteractionDataResolved{}
		}
		if data.Resolved.Attachments == nil {
			data.Resolved.Attachments = map[string]*discordgo.MessageAttachment{}
		}
		data.Resolved.Attachments[file.ID] = file
		*target = append(*target, &discordgo.ApplicationCommandInteractionDataOption{Name: opt.Name, Type: opt.Type, Value: file.ID})
	}
}

// prefixLeaf finds, in a built prefix interaction, the options the command was given (inside its subcommand, if any) and the
// options it declares there.
func prefixLeaf(data *discordgo.ApplicationCommandInteractionData, cmd *types.Command, route *types.PrefixCommandRoute) ([]*discordgo.ApplicationCommandOption, *[]*discordgo.ApplicationCommandInteractionDataOption, bool) {
	declared := cmd.Data.Options
	target := &data.Options
	if route != nil && route.SubcommandPath != "" {
		for _, part := range strings.Split(route.SubcommandPath, ".") {
			var next *discordgo.ApplicationCommandOption
			for _, opt := range declared {
				if opt != nil && opt.Name == part {
					next = opt
					break
				}
			}
			var shown *discordgo.ApplicationCommandInteractionDataOption
			for _, opt := range *target {
				if opt.Name == part {
					shown = opt
					break
				}
			}
			if next == nil || shown == nil {
				return nil, nil, false
			}
			declared, target = next.Options, &shown.Options
		}
	}
	return declared, target, true
}

// addPrefixGameDefault lets "=score B" and "=vg" mean the game of the channel they are typed in: a game option nobody filled
// takes the number of the game that owns this channel.
func addPrefixGameDefault(data *discordgo.ApplicationCommandInteractionData, cmd *types.Command, route *types.PrefixCommandRoute, channelID string) {
	if cmd == nil || cmd.Data == nil {
		return
	}
	declared, target, ok := prefixLeaf(data, cmd, route)
	if !ok {
		return
	}
	for _, opt := range declared {
		if opt == nil || opt.Type != discordgo.ApplicationCommandOptionInteger || opt.Name != "game" {
			continue
		}
		if slices.ContainsFunc(*target, func(o *discordgo.ApplicationCommandInteractionDataOption) bool { return o.Name == "game" }) {
			return
		}
		if number, ok := GameNumberInChannel(channelID); ok {
			*target = append(*target, &discordgo.ApplicationCommandInteractionDataOption{Name: "game", Type: opt.Type, Value: float64(number)})
		}
		return
	}
}

// GameNumberInChannel is the number of the game running in a channel; the game code sets it when it loads.
var GameNumberInChannel = func(channelID string) (int, bool) { return 0, false }
