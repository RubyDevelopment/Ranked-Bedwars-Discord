package helpers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
	apputil "ranked-bedwars/src/utility"
)

// ============================================================================
// Types & Configuration
// ============================================================================

var (
	prefixDurationPattern  = regexp.MustCompile(`(?i)^(\d+[smhdw]|perm|permanent|permanentban)$`)
	prefixSnowflakePattern = regexp.MustCompile(`^\d{17,20}$`)
	timeFilterPattern      = regexp.MustCompile(`^(\d+)([smhdw])$`)
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
		return sendPrefixReply(s, i, content, embeds)
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
		return sendPrefixReply(s, i, content, embeds)
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
		return sendPrefixReply(s, i, "", []*discordgo.MessageEmbed{emb})
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

// sendPrefixReply sends a reply to a prefix command message.
func sendPrefixReply(s *discordgo.Session, i *discordgo.InteractionCreate, content string, embeds []*discordgo.MessageEmbed) error {
	messageID := PrefixSourceMessageID(i)
	if messageID == "" {
		return nil
	}
	content = strings.TrimSpace(content)
	trimEmbeds(embeds)
	_, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
		Content: content,
		Embeds:  embeds,
		Reference: &discordgo.MessageReference{
			MessageID: messageID,
			ChannelID: i.ChannelID,
			GuildID:   i.GuildID,
		},
	})
	return err
}

// EditInteractionFile answers with a PNG attachment (and optional text), for slash and prefix commands alike.
func EditInteractionFile(s *discordgo.Session, i *discordgo.InteractionCreate, content, name string, data []byte) error {
	content = strings.TrimSpace(content)
	file := &discordgo.File{Name: name, ContentType: "image/png", Reader: bytes.NewReader(data)}
	if IsPrefixInteraction(i) {
		messageID := PrefixSourceMessageID(i)
		if messageID == "" {
			return nil
		}
		_, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
			Content:   content,
			Files:     []*discordgo.File{file},
			Reference: &discordgo.MessageReference{MessageID: messageID, ChannelID: i.ChannelID, GuildID: i.GuildID},
		})
		return err
	}
	if IsDeferredSlashInteraction(i) {
		_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content, Files: []*discordgo.File{file}})
		return err
	}
	return Respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content, Files: []*discordgo.File{file}},
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
			data.Options = buildPrefixSubcommandOptions(cmd.Data.Options, route.SubcommandPath, args)
		} else {
			data.Options = buildPrefixLeafOptions(cmd.Data.Options, args)
		}
	}

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

func buildPrefixSubcommandOptions(options []*discordgo.ApplicationCommandOption, subPath string, args []string) []*discordgo.ApplicationCommandInteractionDataOption {
	parts := strings.Split(subPath, ".")
	if len(parts) == 1 {
		return []*discordgo.ApplicationCommandInteractionDataOption{{
			Type:    discordgo.ApplicationCommandOptionSubCommand,
			Name:    parts[0],
			Options: buildPrefixLeafOptions(findPrefixSubcommandOptions(options, parts[0]), args),
		}}
	}
	if len(parts) == 2 {
		return []*discordgo.ApplicationCommandInteractionDataOption{{
			Type: discordgo.ApplicationCommandOptionSubCommandGroup,
			Name: parts[0],
			Options: []*discordgo.ApplicationCommandInteractionDataOption{{
				Type:    discordgo.ApplicationCommandOptionSubCommand,
				Name:    parts[1],
				Options: buildPrefixLeafOptions(findPrefixGroupedSubcommandOptions(options, parts[0], parts[1]), args),
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

// buildPrefixLeafOptions maps space-split prefix args onto slash options.
func buildPrefixLeafOptions(options []*discordgo.ApplicationCommandOption, args []string) []*discordgo.ApplicationCommandInteractionDataOption {
	leaf := make([]*discordgo.ApplicationCommandOption, 0, len(options))
	for _, opt := range options {
		if opt == nil {
			continue
		}
		if opt.Type == discordgo.ApplicationCommandOptionSubCommand || opt.Type == discordgo.ApplicationCommandOptionSubCommandGroup {
			continue
		}
		leaf = append(leaf, opt)
	}
	if len(leaf) == 0 || len(args) == 0 {
		return nil
	}

	out := make([]*discordgo.ApplicationCommandInteractionDataOption, 0, len(leaf))
	argIndex := 0

	for i, opt := range leaf {
		if argIndex >= len(args) {
			break
		}

		if isPrefixGreedyStringOption(opt) {
			reserve := countPrefixTrailingReservable(leaf[i+1:], args[argIndex:])
			take := len(args) - argIndex - reserve
			if take < 1 {
				if !opt.Required {
					continue
				}
				take = 1
			}
			value := strings.Join(args[argIndex:argIndex+take], " ")
			argIndex += take
			out = append(out, prefixOptionValue(opt, value))
			continue
		}

		token := args[argIndex]
		if matchesPrefixTypedOption(opt, token) {
			out = append(out, prefixOptionValue(opt, token))
			argIndex++
			continue
		}
		if opt.Required {
			out = append(out, prefixOptionValue(opt, token))
			argIndex++
		}
	}

	return out
}

func isPrefixGreedyStringOption(opt *discordgo.ApplicationCommandOption) bool {
	if opt == nil || opt.Type != discordgo.ApplicationCommandOptionString || len(opt.Choices) > 0 {
		return false
	}
	switch strings.ToLower(opt.Name) {
	case "reason", "content", "message", "description", "note", "nickname":
		return true
	default:
		return false
	}
}

func countPrefixTrailingReservable(trailing []*discordgo.ApplicationCommandOption, tokens []string) int {
	if len(trailing) == 0 || len(tokens) == 0 {
		return 0
	}

	reserve := 0
	ti := len(tokens) - 1
	for oi := len(trailing) - 1; oi >= 0 && ti >= 0; oi-- {
		opt := trailing[oi]
		if isPrefixGreedyStringOption(opt) {
			if opt.Required {
				reserve++
				ti--
			}
			continue
		}

		tok := tokens[ti]
		if matchesPrefixTypedOption(opt, tok) {
			reserve++
			ti--
			continue
		}
		if opt.Required {
			reserve++
			ti--
		}
	}
	return reserve
}

func matchesPrefixTypedOption(opt *discordgo.ApplicationCommandOption, token string) bool {
	if opt == nil || token == "" {
		return false
	}
	lower := strings.ToLower(token)

	switch opt.Type {
	case discordgo.ApplicationCommandOptionInteger, discordgo.ApplicationCommandOptionNumber:
		_, err := strconv.ParseFloat(token, 64)
		return err == nil
	case discordgo.ApplicationCommandOptionBoolean:
		switch lower {
		case "true", "false", "yes", "no", "1", "0":
			return true
		}
		return false
	case discordgo.ApplicationCommandOptionUser:
		if strings.HasPrefix(token, "<@") && strings.HasSuffix(token, ">") {
			return true
		}
		clean := strings.Trim(token, "<@!>")
		if prefixSnowflakePattern.MatchString(clean) {
			return true
		}
		if prefixDurationPattern.MatchString(token) {
			return false
		}
		if _, err := strconv.ParseFloat(token, 64); err == nil {
			return false
		}
		return len(token) >= 3 && len(token) <= 16
	case discordgo.ApplicationCommandOptionChannel:
		if strings.HasPrefix(token, "<#") && strings.HasSuffix(token, ">") {
			return true
		}
		return prefixSnowflakePattern.MatchString(strings.Trim(token, "<#>"))
	case discordgo.ApplicationCommandOptionRole:
		if strings.HasPrefix(token, "<@&") && strings.HasSuffix(token, ">") {
			return true
		}
		return prefixSnowflakePattern.MatchString(strings.Trim(token, "<@&>"))
	case discordgo.ApplicationCommandOptionString:
		if len(opt.Choices) > 0 {
			for _, choice := range opt.Choices {
				if strings.EqualFold(choice.Name, token) {
					return true
				}
				if str, ok := choice.Value.(string); ok && strings.EqualFold(str, token) {
					return true
				}
			}
			return false
		}
		if strings.EqualFold(opt.Name, "duration") {
			return prefixDurationPattern.MatchString(token)
		}
		return true
	default:
		return true
	}
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
		case "true", "yes", "1":
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
// ============================================================================

// SortedRanks returns rank tiers ordered by minimum ELO ascending.
func SortedRanks(ranks []types.Rank) []types.Rank {
	out := append([]types.Rank(nil), ranks...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].MinElo == out[j].MinElo {
			return out[i].RankName < out[j].RankName
		}
		return out[i].MinElo < out[j].MinElo
	})
	return out
}

// RankForElo returns the rank tier matching the given ELO.
func RankForElo(ranks []types.Rank, elo int) *types.Rank {
	sorted := SortedRanks(ranks)
	var current *types.Rank
	for i := range sorted {
		if elo >= sorted[i].MinElo {
			current = &sorted[i]
		}
	}
	return current
}

// ActiveSeasonKey retrieves the active season key for a guild. An unavailable
// database or a guild with no active season both return ("", 0, nil).
func ActiveSeasonKey(guildID string) (string, uint, error) {
	svc := database.Get()
	if svc == nil {
		return "", 0, nil
	}

	season, err := svc.Season.Active(guildID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return "", 0, nil
		}
		return "", 0, err
	}
	return season.Name, season.ID, nil
}

// ManageRolesPermission returns the permission bit for managing roles.
func ManageRolesPermission() *int64 {
	v := int64(discordgo.PermissionManageRoles)
	return &v
}

// AdministratorPermission returns the permission bit for administrators.
func AdministratorPermission() *int64 {
	v := int64(discordgo.PermissionAdministrator)
	return &v
}

// ManageMessagesPermission returns the permission bit for managing messages.
func ManageMessagesPermission() *int64 {
	v := int64(discordgo.PermissionManageMessages)
	return &v
}

// SubOptionString reads a subcommand string option.
func SubOptionString(options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionString {
			return strings.TrimSpace(opt.StringValue())
		}
	}
	return ""
}

// SubOptionChannelID reads a subcommand channel option ID.
func SubOptionChannelID(options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionChannel {
			return opt.ChannelValue(nil).ID
		}
	}
	return ""
}

// SubOptionInt reads a subcommand integer option.
func SubOptionInt(options []*discordgo.ApplicationCommandInteractionDataOption, name string) int {
	for _, opt := range options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionInteger {
			return int(opt.IntValue())
		}
	}
	return 0
}

// SubOptionBool reads a subcommand boolean option, defaulting to false when absent.
func SubOptionBool(options []*discordgo.ApplicationCommandInteractionDataOption, name string) bool {
	for _, opt := range options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionBoolean {
			return opt.BoolValue()
		}
	}
	return false
}

// SubOptionUser reads a subcommand user option.
func SubOptionUser(s *discordgo.Session, options []*discordgo.ApplicationCommandInteractionDataOption, name string) *discordgo.User {
	for _, opt := range options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionUser {
			return opt.UserValue(s)
		}
	}
	return nil
}

// SubOptionRoleID reads a subcommand role option ID.
func SubOptionRoleID(s *discordgo.Session, options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionRole {
			role := opt.RoleValue(s, "")
			if role != nil {
				return role.ID
			}
		}
	}
	return ""
}

// FetchGuildMember loads a guild member, preferring cache when available.
func FetchGuildMember(s *discordgo.Session, guildID, userID string) (*discordgo.Member, error) {
	if s.State != nil {
		if member, err := s.State.Member(guildID, userID); err == nil && member != nil {
			return member, nil
		}
	}
	return s.GuildMember(guildID, userID)
}

// AddMemberRole assigns a role to a member.
func AddMemberRole(s *discordgo.Session, guildID, userID, roleID string) error {
	if strings.TrimSpace(roleID) == "" {
		return nil
	}
	return s.GuildMemberRoleAdd(guildID, userID, roleID)
}

// RemoveMemberRole removes a role from a member.
func RemoveMemberRole(s *discordgo.Session, guildID, userID, roleID string) error {
	if strings.TrimSpace(roleID) == "" {
		return nil
	}
	return s.GuildMemberRoleRemove(guildID, userID, roleID)
}

// MemberHasRole reports whether a member currently has the given role.
func MemberHasRole(member *discordgo.Member, roleID string) bool {
	if member == nil || roleID == "" {
		return false
	}
	for _, id := range member.Roles {
		if id == roleID {
			return true
		}
	}
	return false
}

// RoleHierarchyIssue is defined in package types (interactions.go).
type RoleHierarchyIssue = types.RoleHierarchyIssue

const (
	RoleHierarchyOK        RoleHierarchyIssue = ""
	RoleHierarchyManaged   RoleHierarchyIssue = "managed"
	RoleHierarchyDangerous RoleHierarchyIssue = "dangerous"
	RoleHierarchyExecutor  RoleHierarchyIssue = "executor"
	RoleHierarchyBot       RoleHierarchyIssue = "bot"
)

// GetRoleManageabilityIssue validates whether executor and bot can manage the target role.
func GetRoleManageabilityIssue(s *discordgo.Session, guildID string, role *discordgo.Role, executor, bot *discordgo.Member) RoleHierarchyIssue {
	if role == nil {
		return RoleHierarchyBot
	}
	if role.Managed {
		return RoleHierarchyManaged
	}
	if role.Permissions&discordgo.PermissionAdministrator != 0 {
		return RoleHierarchyDangerous
	}
	execPos := memberHighestRolePosition(s, guildID, executor)
	botPos := memberHighestRolePosition(s, guildID, bot)
	if executor != nil && !executorIsAdministrator(executor) && execPos <= role.Position {
		return RoleHierarchyExecutor
	}
	if bot != nil && botPos <= role.Position {
		return RoleHierarchyBot
	}
	return RoleHierarchyOK
}

// MemberManageabilityIssue validates whether the bot can moderate the target member.
func MemberManageabilityIssue(s *discordgo.Session, guildID string, target, bot *discordgo.Member) RoleHierarchyIssue {
	if target == nil || bot == nil {
		return RoleHierarchyOK
	}
	if target.User != nil && target.User.Bot {
		return RoleHierarchyManaged
	}
	targetPos := memberHighestRolePosition(s, guildID, target)
	botPos := memberHighestRolePosition(s, guildID, bot)
	if targetPos >= botPos {
		return RoleHierarchyBot
	}
	return RoleHierarchyOK
}

func memberHighestRolePosition(s *discordgo.Session, guildID string, member *discordgo.Member) int {
	if member == nil || len(member.Roles) == 0 {
		return 0
	}
	maxPos := 0
	for _, roleID := range member.Roles {
		role, err := s.State.Role(guildID, roleID)
		if err != nil || role == nil {
			continue
		}
		if role.Position > maxPos {
			maxPos = role.Position
		}
	}
	return maxPos
}

func executorIsAdministrator(member *discordgo.Member) bool {
	if member == nil {
		return false
	}
	return member.Permissions&discordgo.PermissionAdministrator != 0
}

// FormatRoleMention returns a Discord role mention string.
func FormatRoleMention(roleID string) string {
	if roleID == "" {
		return "unknown role"
	}
	return fmt.Sprintf("<@&%s>", roleID)
}

// OptionString reads a top-level slash string option.
func OptionString(i *discordgo.InteractionCreate, name string) string {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionString {
			return strings.TrimSpace(opt.StringValue())
		}
	}
	return ""
}

// OptionInt reads a top-level slash integer option.
func OptionInt(i *discordgo.InteractionCreate, name string) int {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionInteger {
			return int(opt.IntValue())
		}
	}
	return 0
}

// OptionBool reads a top-level slash boolean option.
func OptionBool(i *discordgo.InteractionCreate, name string) (bool, bool) {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionBoolean {
			return opt.BoolValue(), true
		}
	}
	return false, false
}

// OptionUser reads a top-level slash user option.
func OptionUser(s *discordgo.Session, i *discordgo.InteractionCreate, name string) *discordgo.User {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionUser {
			return opt.UserValue(s)
		}
	}
	return nil
}

// ResolveUserOption reads a slash user option and falls back to the invoking user when
// the option is absent, so commands can target a user without requiring one.
func ResolveUserOption(s *discordgo.Session, i *discordgo.InteractionCreate, name string) *discordgo.User {
	if user := OptionUser(s, i, name); user != nil {
		return user
	}
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User
	}
	return i.User
}

// OptionChannelID reads a top-level slash channel option ID.
func OptionChannelID(i *discordgo.InteractionCreate, name string) string {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionChannel {
			return opt.ChannelValue(nil).ID
		}
	}
	return ""
}

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

// V2Flag marks a message as Components V2.
const V2Flag = 1 << 15

// LinkButton is defined in package types (interactions.go).
type LinkButton = types.LinkButton

// Embed is defined in package types (interactions.go).
type Embed = types.Embed

// V2Item is defined in package types (interactions.go).
type V2Item = types.V2Item

// FieldsLayout is how a command response's fields are written into {{response.fields}}: Template is repeated for every
// field ({{field.name}}, {{field.value}}, {{field.index}}) and the repeats are joined by Separator (blank, tight or dot).
type FieldsLayout struct {
	Template  string `json:"template"`
	Separator string `json:"separator"`
}

// Render writes the fields; the zero layout is the classic "**name**\nvalue" blocks with a blank line between them.
func (l *FieldsLayout) Render(names, values []string) string {
	tpl, sep := "**{{field.name}}**\n{{field.value}}", "\n\n"
	if l != nil {
		if strings.TrimSpace(l.Template) != "" {
			tpl = l.Template
		}
		switch l.Separator {
		case "tight":
			sep = "\n"
		case "dot":
			sep = "  ·  "
		}
	}
	out := make([]string, 0, len(names))
	for i := range names {
		out = append(out, FillVars(tpl, map[string]string{"field.name": names[i], "field.value": values[i], "field.index": strconv.Itoa(i + 1)}))
	}
	return strings.Join(out, sep)
}

// MessageDoc is a saved message.
type MessageDoc struct {
	ID                    string           `json:"id"`
	Name                  string           `json:"name"`
	AllowMentions         bool             `json:"allow_mentions,omitempty"`
	AllowedMentions       *AllowedMentions `json:"allowed_mentions,omitempty"`
	SuppressEmbeds        bool             `json:"suppress_embeds,omitempty"`
	SuppressNotifications bool             `json:"suppress_notifications,omitempty"`
	Mode                  string           `json:"mode"`
	Content               string           `json:"content"`
	Embeds                []Embed          `json:"embeds"`
	Buttons               []LinkButton     `json:"buttons"`
	V2                    []V2Item         `json:"v2"`
	ResponseFields        *FieldsLayout    `json:"response_fields"`
}

type AllowedMentions struct {
	Parse       []string `json:"parse"`
	Users       []string `json:"users"`
	Roles       []string `json:"roles"`
	RepliedUser bool     `json:"replied_user"`
}

// ParseMessageDoc reads a stored message document. A document that does not parse is an error; missing fields are empty.
func ParseMessageDoc(raw json.RawMessage) (*MessageDoc, error) {
	var d MessageDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// varPattern matches {path}, and the older {{path}} that messages saved before single braces still hold.
var varPattern = regexp.MustCompile(`\{\{\s*([\w.]+)\s*\}\}|\{\s*([\w.]+)\s*\}`)

// FillVars replaces every {path} (or {{path}}) with vars[path]; unknown paths stay as written.
func FillVars(text string, vars map[string]string) string {
	if !strings.Contains(text, "{") {
		return text
	}
	return varPattern.ReplaceAllStringFunc(text, func(whole string) string {
		m := varPattern.FindStringSubmatch(whole)
		if v, ok := vars[m[1]+m[2]]; ok {
			return v
		}
		return whole
	})
}

// VarFiller returns FillVars bound to vars.
func VarFiller(vars map[string]string) func(string) string {
	return func(t string) string { return FillVars(t, vars) }
}

func colorInt(hex string) (int, bool) {
	if len(hex) != 7 || hex[0] != '#' {
		return 0, false
	}
	n, err := strconv.ParseInt(hex[1:], 16, 32)
	return int(n), err == nil
}

func link(b LinkButton, f func(string) string) map[string]any {
	out := map[string]any{"type": 2, "style": 5, "label": f(b.Label), "url": f(b.URL)}
	if b.DiscordID != 0 {
		out["id"] = b.DiscordID
	}
	if b.Emoji != "" {
		out["emoji"] = map[string]any{"name": b.Emoji}
	}
	if b.Disabled {
		out["disabled"] = true
	}
	return out
}

func componentID(payload map[string]any, id uint32) map[string]any {
	if id != 0 {
		payload["id"] = id
	}
	return payload
}

func leaf(l V2Item, f func(string) string) map[string]any {
	switch l.Type {
	case "section":
		if l.Accessory.Kind == "thumbnail" || l.Accessory.Kind == "button" {
			texts := make([]map[string]any, 0, len(l.Texts))
			for _, t := range l.Texts {
				texts = append(texts, map[string]any{"type": 10, "content": f(t)})
			}
			acc := link(LinkButton{Label: l.Accessory.Label, URL: l.Accessory.URL, DiscordID: l.Accessory.DiscordID, Emoji: l.Accessory.Emoji, Disabled: l.Accessory.Disabled}, f)
			if l.Accessory.Kind == "thumbnail" {
				acc = componentID(map[string]any{"type": 11, "media": map[string]any{"url": f(l.Accessory.URL)}}, l.Accessory.DiscordID)
				if l.Accessory.Description != "" {
					acc["description"] = f(l.Accessory.Description)
				}
				if l.Accessory.Spoiler {
					acc["spoiler"] = true
				}
			}
			return componentID(map[string]any{"type": 9, "components": texts, "accessory": acc}, l.DiscordID)
		}
		// Discord requires an accessory on a section: without one it is plain text.
		parts := make([]string, 0, len(l.Texts))
		for _, t := range l.Texts {
			parts = append(parts, f(t))
		}
		return componentID(map[string]any{"type": 10, "content": strings.Join(parts, "\n")}, l.DiscordID)
	case "gallery":
		items := make([]map[string]any, 0, len(l.Items))
		for _, it := range l.Items {
			item := map[string]any{"media": map[string]any{"url": f(it.URL)}}
			if it.DiscordID != 0 {
				item["id"] = it.DiscordID
			}
			if it.Description != "" {
				item["description"] = f(it.Description)
			}
			if it.Spoiler {
				item["spoiler"] = true
			}
			items = append(items, item)
		}
		return componentID(map[string]any{"type": 12, "items": items}, l.DiscordID)
	case "separator":
		spacing := 1
		if l.Spacing == 2 {
			spacing = 2
		}
		return componentID(map[string]any{"type": 14, "divider": l.Divider == nil || *l.Divider, "spacing": spacing}, l.DiscordID)
	case "buttons":
		row := make([]map[string]any, 0, len(l.Buttons))
		for _, b := range l.Buttons {
			row = append(row, link(b, f))
		}
		return componentID(map[string]any{"type": 1, "components": row}, l.DiscordID)
	default:
		return componentID(map[string]any{"type": 10, "content": f(l.Content)}, l.DiscordID)
	}
}

// Payload is the JSON body Discord takes for a message, with {{variables}} filled in by f. Pings are off unless the
// document allows mentions.
func (d *MessageDoc) Payload(f func(string) string) map[string]any {
	body := map[string]any{"allowed_mentions": d.allowedMentionsPayload()}
	flags := 0
	if d.SuppressEmbeds {
		flags |= 1 << 2
	}
	if d.SuppressNotifications {
		flags |= 1 << 12
	}
	if d.Mode == "v2" {
		flags |= V2Flag
		comps := make([]map[string]any, 0, len(d.V2))
		for _, it := range d.V2 {
			if it.Type != "container" {
				comps = append(comps, leaf(it, f))
				continue
			}
			children := make([]map[string]any, 0, len(it.Children))
			for _, c := range it.Children {
				children = append(children, leaf(c, f))
			}
			c := componentID(map[string]any{"type": 17, "spoiler": it.Spoiler, "components": children}, it.DiscordID)
			if n, ok := colorInt(it.Accent); ok {
				c["accent_color"] = n
			}
			comps = append(comps, c)
		}
		body["components"] = comps
		body["flags"] = flags
		return body
	}
	if flags != 0 {
		body["flags"] = flags
	}
	if d.Content != "" {
		body["content"] = f(d.Content)
	}
	embeds := make([]map[string]any, 0, len(d.Embeds))
	for _, e := range d.Embeds {
		m := map[string]any{}
		set := func(k, v string) {
			if v != "" {
				m[k] = v
			}
		}
		set("title", f(e.Title))
		set("url", e.URL)
		set("description", f(e.Description))
		if e.Blend {
			m["color"] = EmbedBlendColor
		} else if n, ok := colorInt(e.Color); ok {
			m["color"] = n
		}
		// Each part is filled first and left out when it comes out empty: a variable like {response.thumbnail} is empty on an
		// answer that has none, and Discord rejects an empty name or address.
		if name := f(e.Author.Name); name != "" {
			a := map[string]any{"name": name}
			if e.Author.URL != "" {
				a["url"] = e.Author.URL
			}
			if e.Author.IconURL != "" {
				a["icon_url"] = f(e.Author.IconURL)
			}
			m["author"] = a
		}
		if text := f(e.Footer.Text); text != "" {
			ft := map[string]any{"text": text}
			if e.Footer.IconURL != "" {
				ft["icon_url"] = f(e.Footer.IconURL)
			}
			m["footer"] = ft
		}
		if img := f(e.Image); img != "" {
			m["image"] = map[string]any{"url": img}
		}
		if thumb := f(e.Thumbnail); thumb != "" {
			m["thumbnail"] = map[string]any{"url": thumb}
		}
		if e.Timestamp {
			m["timestamp"] = time.Now().UTC().Format(time.RFC3339)
		}
		if len(e.Fields) > 0 {
			fields := make([]map[string]any, 0, len(e.Fields))
			for _, x := range e.Fields {
				fields = append(fields, map[string]any{"name": f(x.Name), "value": f(x.Value), "inline": x.Inline})
			}
			m["fields"] = fields
		}
		embeds = append(embeds, m)
	}
	body["embeds"] = embeds
	if len(d.Buttons) > 0 {
		row := make([]map[string]any, 0, len(d.Buttons))
		for _, b := range d.Buttons {
			row = append(row, link(b, f))
		}
		body["components"] = []map[string]any{{"type": 1, "components": row}}
	}
	return body
}

func (d *MessageDoc) allowedMentionsPayload() map[string]any {
	if d.AllowedMentions == nil {
		parse := []string{}
		if d.AllowMentions {
			parse = []string{"users", "roles"}
		}
		return map[string]any{"parse": parse}
	}

	users := validMentionIDs(d.AllowedMentions.Users)
	roles := validMentionIDs(d.AllowedMentions.Roles)
	parse := make([]string, 0, 3)
	seen := map[string]bool{}
	for _, kind := range d.AllowedMentions.Parse {
		if kind != "everyone" && kind != "users" && kind != "roles" {
			continue
		}
		if (kind == "users" && len(users) > 0) || (kind == "roles" && len(roles) > 0) || seen[kind] {
			continue
		}
		seen[kind] = true
		parse = append(parse, kind)
	}
	out := map[string]any{"parse": parse, "replied_user": d.AllowedMentions.RepliedUser}
	if len(users) > 0 {
		out["users"] = users
	}
	if len(roles) > 0 {
		out["roles"] = roles
	}
	return out
}

func validMentionIDs(ids []string) []string {
	out := make([]string, 0, min(len(ids), 100))
	seen := map[string]bool{}
	for _, id := range ids {
		if len(out) == 100 {
			break
		}
		if len(id) < 17 || len(id) > 20 || seen[id] {
			continue
		}
		valid := true
		for _, char := range id {
			if char < '0' || char > '9' {
				valid = false
				break
			}
		}
		if valid {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}

// SendPayload posts payload in channelID and returns the new message's id.
func SendPayload(s *discordgo.Session, channelID string, payload map[string]any) (string, error) {
	trimPayloadStrings(payload)
	body, err := s.RequestWithBucketID("POST", discordgo.EndpointChannelMessages(channelID), payload, discordgo.EndpointChannelMessages(channelID))
	if err != nil {
		return "", err
	}
	var m struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &m)
	return m.ID, nil
}

// EditPayload replaces a message's content with payload.
func EditPayload(s *discordgo.Session, channelID, messageID string, payload map[string]any) error {
	trimPayloadStrings(payload)
	_, err := s.RequestWithBucketID("PATCH", discordgo.EndpointChannelMessage(channelID, messageID), payload, discordgo.EndpointChannelMessage(channelID, ""))
	return err
}

// EditOriginalPayload fills in the reply of an interaction that was deferred. Whether it is ephemeral was fixed when it was deferred.
func EditOriginalPayload(s *discordgo.Session, i *discordgo.Interaction, payload map[string]any) error {
	trimPayloadStrings(payload)
	_, err := s.RequestWithBucketID("PATCH", discordgo.EndpointWebhookMessage(i.AppID, i.Token, "@original"), payload, discordgo.EndpointWebhookToken(i.AppID, ""))
	return err
}

// historyPagePrefix identifies history pagination button custom IDs.
const historyPagePrefix = "pgv1|"

// HistoryPageKind is defined in package types (interactions.go).
type HistoryPageKind = types.HistoryPageKind

const (
	HistoryPageStrikes     HistoryPageKind = "strikes"
	HistoryPagePunishments HistoryPageKind = "punishments"
	HistoryPageGames       HistoryPageKind = "games"
	HistoryPageDecays      HistoryPageKind = "decays"
)

// historyJumpSize is how many pages the jump buttons move by.
const historyJumpSize = 5

// IsHistoryPaginationCustomID reports whether a component custom ID belongs to
// the shared history pagination buttons.
func IsHistoryPaginationCustomID(customID string) bool {
	return strings.HasPrefix(customID, historyPagePrefix)
}

// BuildHistoryPageComponents builds the pagination button row for a history
// embed. The middle button displays the current page and is non-clickable. The
// Back 5 / Next 5 buttons are only shown when there are at least 5 pages.
func BuildHistoryPageComponents(kind HistoryPageKind, snowflake string, page, totalPages int, opts []string) []discordgo.MessageComponent {
	if totalPages <= 1 {
		return nil
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}

	row := &discordgo.ActionsRow{Components: make([]discordgo.MessageComponent, 0, 5)}

	if totalPages >= historyJumpSize {
		back5 := page - historyJumpSize
		if back5 < 1 {
			back5 = 1
		}
		row.Components = append(row.Components, historyPageButton(kind, snowflake, back5, "Back 5", opts, page <= historyJumpSize))
	}

	back := page - 1
	if back < 1 {
		back = 1
	}
	row.Components = append(row.Components, historyPageButton(kind, snowflake, back, "Back", opts, page == 1))

	row.Components = append(row.Components, &discordgo.Button{
		Label:    fmt.Sprintf("%d/%d", page, totalPages),
		Style:    discordgo.SecondaryButton,
		Disabled: true,
		CustomID: historyPagePrefix + "page",
	})

	next := page + 1
	if next > totalPages {
		next = totalPages
	}
	row.Components = append(row.Components, historyPageButton(kind, snowflake, next, "Next", opts, page == totalPages))

	if totalPages >= historyJumpSize {
		next5 := page + historyJumpSize
		if next5 > totalPages {
			next5 = totalPages
		}
		row.Components = append(row.Components, historyPageButton(kind, snowflake, next5, "Next 5", opts, page > totalPages-historyJumpSize))
	}

	return []discordgo.MessageComponent{row}
}

func historyPageButton(kind HistoryPageKind, snowflake string, targetPage int, label string, opts []string, disabled bool) discordgo.MessageComponent {
	return &discordgo.Button{
		CustomID: encodeHistoryPageCustomID(kind, snowflake, targetPage, opts),
		Label:    label,
		Style:    discordgo.SecondaryButton,
		Disabled: disabled,
	}
}

// encodeHistoryPageCustomID encodes a button target into a custom ID:
// pgv1|kind|snowflake|page|option...
func encodeHistoryPageCustomID(kind HistoryPageKind, snowflake string, page int, opts []string) string {
	parts := make([]string, 0, 4+len(opts))
	parts = append(parts, "pgv1", string(kind), snowflake, strconv.Itoa(page))
	parts = append(parts, opts...)
	return strings.Join(parts, "|")
}

func decodeHistoryPageCustomID(customID string) (kind HistoryPageKind, snowflake string, page int, opts []string, ok bool) {
	if !IsHistoryPaginationCustomID(customID) {
		return "", "", 0, nil, false
	}
	parts := strings.Split(customID, "|")
	if len(parts) < 4 {
		return "", "", 0, nil, false
	}
	page, err := strconv.Atoi(parts[3])
	if err != nil || page < 1 {
		return "", "", 0, nil, false
	}
	return HistoryPageKind(parts[1]), parts[2], page, parts[4:], true
}

func historyOptionAt(opts []string, index int) string {
	if index < 0 || index >= len(opts) {
		return ""
	}
	return opts[index]
}

func clampHistoryPage(page, totalPages int) int {
	if totalPages < 1 {
		return 1
	}
	if page < 1 {
		return 1
	}
	if page > totalPages {
		return totalPages
	}
	return page
}

// HandleHistoryPageClick responds to a Next/Back click on a strike,
// punishment or game history embed (see BuildHistoryPageComponents). It
// reports false when customID isn't one of these buttons, so the caller can
// fall through to other component handling.
func HandleHistoryPageClick(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	kind, snowflake, page, opts, ok := decodeHistoryPageCustomID(customID)
	if !ok {
		return false
	}

	// Only the person who ran the command turns its pages.
	if owner := historyOwner(i); owner != "" && owner != InteractionUserID(i) {
		_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Flags:  discordgo.MessageFlagsEphemeral,
				Embeds: []*discordgo.MessageEmbed{Preset("error", types.PresetOptions{Title: "Permission Denied", Description: "Only the player who ran this command can change pages."})},
			},
		})
		return true
	}

	emb, components, err := renderHistoryPage(i.GuildID, kind, snowflake, page, opts)
	if err != nil {
		emb = Preset("error", types.PresetOptions{
			Title:       "Error",
			Description: "Could not load that page. Please re-run the original command.",
		})
		components = nil
	}
	_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{emb},
			Components: components,
		},
	})
	return true
}

// historyOwner is who ran the command a history message answers ("" when Discord doesn't say).
func historyOwner(i *discordgo.InteractionCreate) string {
	if i.Message == nil {
		return ""
	}
	if meta := i.Message.InteractionMetadata; meta != nil && meta.User != nil {
		return meta.User.ID
	}
	if i.Message.Interaction != nil && i.Message.Interaction.User != nil {
		return i.Message.Interaction.User.ID
	}
	return ""
}

func renderHistoryPage(guildID string, kind HistoryPageKind, snowflake string, page int, opts []string) (*discordgo.MessageEmbed, []discordgo.MessageComponent, error) {
	svc := database.Get()
	if svc == nil {
		return nil, nil, fmt.Errorf("database service not initialized")
	}
	view, err := svc.Player.Get(guildID, snowflake)
	if err != nil {
		return nil, nil, err
	}
	username := view.Config.Username

	switch kind {
	case HistoryPageStrikes:
		duration, _ := ParseTimeFilter(historyOptionAt(opts, 0))
		entries, err := svc.Strikes.AllByPlayer(guildID, sdk.UintToString(view.Player.ID), 0, false)
		if err != nil {
			return nil, nil, err
		}
		filtered := FilterStrikeHistoryEntries(entries, duration)
		totalPages := HistoryTotalPages(len(filtered))
		page = clampHistoryPage(page, totalPages)
		emb := BuildStrikeHistoryEmbed(username, filtered, page-1)
		components := BuildHistoryPageComponents(HistoryPageStrikes, snowflake, page, totalPages, opts)
		return emb, components, nil

	case HistoryPagePunishments:
		activeOnly := historyOptionAt(opts, 0) == "1"
		duration, _ := ParseTimeFilter(historyOptionAt(opts, 1))
		entries, err := svc.Punishments.ByPlayer(guildID, sdk.UintToString(view.Player.ID), false, "", sdk.Page{})
		if err != nil {
			return nil, nil, err
		}
		filtered := FilterPunishmentHistoryEntries(entries.Data, activeOnly, duration)
		totalPages := HistoryTotalPages(len(filtered))
		page = clampHistoryPage(page, totalPages)
		emb := BuildPunishmentHistoryEmbed(username, filtered, page-1, activeOnly)
		components := BuildHistoryPageComponents(HistoryPagePunishments, snowflake, page, totalPages, opts)
		return emb, components, nil

	case HistoryPageDecays:
		duration, _ := ParseTimeFilter(historyOptionAt(opts, 0))
		_, seasonID, err := ActiveSeasonKey(guildID)
		if err != nil {
			return nil, nil, err
		}
		entries, err := svc.Decays.AllByPlayer(guildID, sdk.UintToString(view.Player.ID), seasonID, false)
		if err != nil {
			return nil, nil, err
		}
		filtered := FilterDecayHistoryEntries(entries, duration)
		totalPages := HistoryTotalPages(len(filtered))
		page = clampHistoryPage(page, totalPages)
		emb := BuildDecayHistoryEmbed(username, filtered, page-1)
		components := BuildHistoryPageComponents(HistoryPageDecays, snowflake, page, totalPages, opts)
		return emb, components, nil

	case HistoryPageGames:
		duration, _ := ParseTimeFilter(historyOptionAt(opts, 0))
		hideVoided := historyOptionAt(opts, 1) == "1"
		_, seasonID, err := ActiveSeasonKey(guildID)
		if err != nil {
			return nil, nil, err
		}
		participants, err := svc.Player.AllGames(guildID, sdk.UintToString(view.Player.ID))
		if err != nil {
			return nil, nil, err
		}
		entries := FilterGameHistoryEntries(participants, seasonID, duration, hideVoided)
		totalPages := HistoryTotalPages(len(entries))
		page = clampHistoryPage(page, totalPages)
		playersByGame, err := LoadGameHistoryPlayers(svc, guildID, entries, page-1)
		if err != nil {
			return nil, nil, err
		}
		emb := BuildGameHistoryEmbedWithPlayers(username, entries, page-1, playersByGame)
		components := BuildHistoryPageComponents(HistoryPageGames, snowflake, page, totalPages, opts)
		return emb, components, nil
	}
	return nil, nil, fmt.Errorf("unknown history page kind %q", kind)
}

// ── merged from interaction_ack.go ──

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
func editComponentEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) {
	WithGuildFooterIcon(s, i, embed)
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}})
}

// followupEmbed sends an ephemeral embed after the click was already acknowledged.
func followupEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) {
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
