package general

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/factionmgr"
	"ranked-bedwars/src/discord/helpers/pagination"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(FactionCommand)
	helpers.RegisterComponentHandler(handleFactionInvite)
	helpers.RegisterComponentHandler(handleFactionLeaderboardPage)
}

const (
	factionLeaderboardPrefix = "fclb|" // fclb|<page to go to>|<lifetime or seasonal>; an inert button is fclb|off-<slot>
	factionPerPage           = 10
)

var factionInviteButton = regexp.MustCompile(`^faction_(accept|deny)_(\d+)_(\d+)$`)

// ============================================================================
// Command Definitions
// ============================================================================

func factionNameOption(required bool) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Name: "faction", Description: "A faction's name or tag (default: your own)", Type: discordgo.ApplicationCommandOptionString, Required: required}
}

func factionUserOption(description string) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Name: "user", Description: description, Type: discordgo.ApplicationCommandOptionUser, Required: true}
}

func factionBoardOptions() []*discordgo.ApplicationCommandOption {
	return []*discordgo.ApplicationCommandOption{
		{Name: "board", Description: "Lifetime trophies or this season's trophies (default: lifetime)", Type: discordgo.ApplicationCommandOptionString,
			Choices: []*discordgo.ApplicationCommandOptionChoice{{Name: "Lifetime", Value: "lifetime"}, {Name: "Seasonal", Value: "seasonal"}}},
		{Name: "page", Description: "The page of the leaderboard to display", Type: discordgo.ApplicationCommandOptionInteger, MinValue: ptrToFloat64(1)},
	}
}

// FactionCommand creates and manages factions: invites, roles, customisation, and the faction leaderboards.
var FactionCommand = &types.Command{
	Name:         "faction",
	Requirements: types.FactionRequirements,
	Description:  "Create and manage your faction: members, roles, cosmetics and the faction leaderboards.",
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "create", Description: "Found a new faction", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{
				{Name: "name", Description: "The faction's name", Type: discordgo.ApplicationCommandOptionString, Required: true},
				{Name: "tag", Description: "A short tag, shown next to its members' names", Type: discordgo.ApplicationCommandOptionString, Required: true},
			}},
			{Name: "info", Description: "View a faction's profile", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{
				{Name: "faction", Description: "A faction's name or tag", Type: discordgo.ApplicationCommandOptionString},
				{Name: "user", Description: "Show the faction this player is in (default: yours)", Type: discordgo.ApplicationCommandOptionUser},
			}},
			{Name: "level", Description: "Your faction's level and rewards, or what a level takes and unlocks", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{
				{Name: "level", Description: "Show this level of the ruleset", Type: discordgo.ApplicationCommandOptionInteger, MinValue: ptrToFloat64(1)},
				{Name: "faction", Description: "A faction's name or tag (default: yours)", Type: discordgo.ApplicationCommandOptionString},
			}},
			{Name: "levels", Description: "Every faction level with its trophies, seats and rewards", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{
				{Name: "page", Description: "The page to display", Type: discordgo.ApplicationCommandOptionInteger, MinValue: ptrToFloat64(1)},
			}},
			{Name: "leaderboard", Description: "View the lifetime or seasonal faction leaderboard", Type: discordgo.ApplicationCommandOptionSubCommand, Options: factionBoardOptions()},
			{Name: "invite", Description: "Invite a player to your faction", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{factionUserOption("Player to invite")}},
			{Name: "invites", Description: "See the faction invitations waiting for you", Type: discordgo.ApplicationCommandOptionSubCommand},
			{Name: "accept", Description: "Accept a faction invitation", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{factionNameOption(false)}},
			{Name: "decline", Description: "Decline a faction invitation", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{factionNameOption(false)}},
			{Name: "leave", Description: "Leave your faction", Type: discordgo.ApplicationCommandOptionSubCommand},
			{Name: "kick", Description: "Remove a member from your faction", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{factionUserOption("Member to remove")}},
			{Name: "promote", Description: "Make a member an Officer", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{factionUserOption("Member to promote")}},
			{Name: "demote", Description: "Make an Officer a plain Member", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{factionUserOption("Officer to demote")}},
			{Name: "transfer", Description: "Hand your faction to another member", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{factionUserOption("New Founder")}},
			{Name: "disband", Description: "Permanently disband your faction", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{
				{Name: "confirm", Description: "true to confirm: this cannot be undone", Type: discordgo.ApplicationCommandOptionBoolean, Required: true},
			}},
			{Name: "customize", Description: "Change your faction's name, tag, colour, icon or tag symbol", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{
				{Name: "name", Description: "A new name", Type: discordgo.ApplicationCommandOptionString},
				{Name: "tag", Description: "A new tag", Type: discordgo.ApplicationCommandOptionString},
				{Name: "color", Description: "A colour, like #FF5555 (unlocked by level)", Type: discordgo.ApplicationCommandOptionString},
				{Name: "icon", Description: "An emoji or image link (unlocked by level)", Type: discordgo.ApplicationCommandOptionString},
				{Name: "symbol", Description: "A symbol around the tag, like ✦ (unlocked by level)", Type: discordgo.ApplicationCommandOptionString},
			}},
		},
	},
	Execute: executeFactionCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeFactionCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	subcommand, options := types.SelectedSubcommand(i)
	if subcommand == "" {
		return helpers.EditInteractionEmbed(s, i, FactionInvalidSubcommandEmbed())
	}
	guildID, userID := i.GuildID, helpers.InteractionUserID(i)

	// Everything but reading a profile or a leaderboard is for registered players.
	if _, err := database.Must().Player.Get(guildID, userID); err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, FactionRegistrationRequiredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorFactionEmbed())
	}

	var result error
	switch subcommand {
	case "create":
		result = factionCreate(s, i, guildID, userID, options)
	case "info":
		result = factionInfo(s, i, guildID, userID, options, helpers.SubOptionString(options, "faction"), helpers.SubOptionUser(s, options, "user"))
	case "level":
		result = factionLevel(s, i, guildID, userID, options)
	case "levels":
		result = factionLevels(s, i, guildID, userID, options)
	case "leaderboard":
		result = factionLeaderboard(s, i, guildID, helpers.SubOptionString(options, "board"), max(1, helpers.SubOptionInt(options, "page")))
	case "invite":
		result = factionInvite(s, i, guildID, userID, options)
	case "invites":
		result = factionInvites(s, i, guildID, userID)
	case "accept", "decline":
		result = factionAnswer(s, i, guildID, userID, options, subcommand == "accept")
	case "leave":
		result = factionLeave(s, i, guildID, userID)
	case "kick":
		result = factionKick(s, i, guildID, userID, options)
	case "promote", "demote":
		result = factionRole(s, i, guildID, userID, options, subcommand == "promote")
	case "transfer":
		result = factionTransfer(s, i, guildID, userID, options)
	case "disband":
		result = factionDisband(s, i, guildID, userID, options)
	case "customize":
		result = factionCustomize(s, i, guildID, userID, options)
	default:
		return helpers.EditInteractionEmbed(s, i, FactionInvalidSubcommandEmbed())
	}
	if result == nil {
		return nil
	}
	if msg := database.FactionMessage(result); msg != "" {
		return helpers.EditInteractionEmbed(s, i, FactionRefusedEmbed(msg))
	}
	log.Printf("[Faction] /faction %s in %s: %v", subcommand, guildID, result)
	return helpers.EditInteractionEmbed(s, i, UnexpectedErrorFactionEmbed())
}

// ============================================================================
// Subcommands
// ============================================================================

func factionCreate(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	days := 0
	if i.Member != nil && !i.Member.JoinedAt.IsZero() {
		days = int(time.Since(i.Member.JoinedAt).Hours() / 24)
	}
	p, err := database.FactionCreate(guildID, userID, helpers.SubOptionString(options, "name"), helpers.SubOptionString(options, "tag"), days)
	if err != nil {
		return err
	}
	go factionmgr.RefreshMember(guildID, userID)
	return helpers.EditInteractionEmbed(s, i, FactionCreatedEmbed(p))
}

// factionInfo shows a profile: the faction named, the one the user is in, or the invoker's own.
func factionInfo(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, _ []*discordgo.ApplicationCommandInteractionDataOption, name string, user *discordgo.User) error {
	var p *database.FactionProfile
	var err error
	switch {
	case strings.TrimSpace(name) != "":
		p, err = database.FactionNamed(guildID, name)
	case user != nil:
		if user.Bot {
			return helpers.EditInteractionEmbed(s, i, FactionBotTargetEmbed())
		}
		p, err = database.FactionFor(guildID, user.ID)
	default:
		p, err = database.FactionFor(guildID, userID)
	}
	if err != nil {
		log.Printf("[Faction] info in %s: %v", guildID, err)
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorFactionEmbed())
	}
	if p == nil {
		switch {
		case strings.TrimSpace(name) != "":
			return helpers.EditInteractionEmbed(s, i, FactionNotFoundEmbed(name))
		case user != nil && user.ID != userID:
			return helpers.EditInteractionEmbed(s, i, FactionPlayerHasNoneEmbed(user.ID))
		}
		return helpers.EditInteractionEmbed(s, i, FactionNotInFactionEmbed())
	}
	cfg, _ := database.FactionConfigGet(guildID) // only the "next unlock" line needs it
	return helpers.EditInteractionEmbed(s, i, FactionInfoEmbed(p, cfg))
}

func factionLeaderboard(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, board string, page int) error {
	embed, components := buildFactionLeaderboard(guildID, board, page)
	if len(components) > 0 {
		return helpers.EditInteractionComponents(s, i, embed, components)
	}
	return helpers.EditInteractionEmbed(s, i, embed)
}

// buildFactionLeaderboard loads one page of a faction leaderboard and writes it, with its page buttons.
func buildFactionLeaderboard(guildID, board string, page int) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	if board != "seasonal" {
		board = "lifetime"
	}
	rows, total, err := database.FactionLeaderboard(guildID, board, factionPerPage, (page-1)*factionPerPage)
	if err != nil {
		log.Printf("[Faction] leaderboard in %s: %v", guildID, err)
		return UnexpectedErrorFactionEmbed(), nil
	}
	if len(rows) == 0 {
		if page > 1 {
			return FactionLeaderboardEmptyPageEmbed(page), nil
		}
		return FactionLeaderboardEmptyEmbed(board), nil
	}
	totalPages := max(1, (int(total)+factionPerPage-1)/factionPerPage)
	components := pagination.BuildPageRow(page, totalPages,
		func(target int) string { return fmt.Sprintf("%s%d|%s", factionLeaderboardPrefix, target, board) },
		func(slot string) string { return factionLeaderboardPrefix + "off-" + slot })
	return FactionLeaderboardEmbed(rows, board, page, totalPages), components
}

func factionInvite(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	target := helpers.SubOptionUser(s, options, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, FactionPlayerNotFoundEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, FactionBotTargetEmbed())
	}
	mine, err := database.FactionFor(guildID, userID)
	if err != nil {
		return err
	}
	if mine == nil {
		return helpers.EditInteractionEmbed(s, i, FactionNotInFactionEmbed())
	}
	if _, err := database.Must().Player.Get(guildID, target.ID); err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, FactionTargetNotRegisteredEmbed(target.ID))
		}
		return err
	}
	expires, err := database.FactionInvitePlayer(guildID, mine.Faction.ID, userID, target.ID)
	if err != nil {
		return err
	}
	msg := &discordgo.MessageSend{
		Content:         "<@" + target.ID + ">",
		Embeds:          []*discordgo.MessageEmbed{FactionInvitationEmbed(mine, userID, expires)},
		Components:      factionInviteButtons(mine.Faction.ID, target.ID),
		AllowedMentions: &discordgo.MessageAllowedMentions{Users: []string{target.ID}},
	}
	if _, sendErr := helpers.SendChannelMessage(s, guildID, i.ChannelID, msg); sendErr != nil {
		log.Printf("[Faction] could not post the invitation for %s in %s: %v", target.ID, guildID, sendErr)
		return helpers.EditInteractionEmbed(s, i, FactionInviteSentNoPostEmbed(target.ID, mine.Faction.Name))
	}
	return helpers.EditInteractionEmbed(s, i, FactionInviteSentEmbed(target.ID, mine.Faction.Name))
}

func factionInvites(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string) error {
	list, err := database.FactionInvites(guildID, userID)
	if err != nil {
		return err
	}
	return helpers.EditInteractionEmbed(s, i, FactionInvitesEmbed(list))
}

func factionAnswer(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption, accept bool) error {
	var factionID uint
	if name := helpers.SubOptionString(options, "faction"); strings.TrimSpace(name) != "" {
		p, err := database.FactionNamed(guildID, name)
		if err != nil {
			return err
		}
		if p == nil {
			return helpers.EditInteractionEmbed(s, i, FactionNotFoundEmbed(name))
		}
		factionID = p.Faction.ID
	}
	p, err := database.FactionAnswer(guildID, userID, factionID, accept)
	if err != nil {
		return err
	}
	if !accept {
		return helpers.EditInteractionEmbed(s, i, FactionDeclinedEmbed())
	}
	go factionmgr.SyncFaction(guildID, p)
	return helpers.EditInteractionEmbed(s, i, FactionJoinedEmbed(p))
}

func factionLeave(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string) error {
	f, err := database.FactionLeave(guildID, userID)
	if err != nil {
		return err
	}
	go factionmgr.RemoveFrom(guildID, userID, f.RoleID)
	return helpers.EditInteractionEmbed(s, i, FactionLeftEmbed(f.Name))
}

// myFaction is the invoker's faction, or answers the "you are not in a faction" embed and returns nil.
func myFaction(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string) (*database.FactionProfile, error, bool) {
	p, err := database.FactionFor(guildID, userID)
	if err != nil {
		return nil, err, false
	}
	if p == nil {
		return nil, helpers.EditInteractionEmbed(s, i, FactionNotInFactionEmbed()), false
	}
	return p, nil, true
}

func factionKick(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	target := helpers.SubOptionUser(s, options, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, FactionPlayerNotFoundEmbed())
	}
	mine, err, ok := myFaction(s, i, guildID, userID)
	if !ok {
		return err
	}
	if err := database.FactionKick(guildID, mine.Faction.ID, userID, target.ID); err != nil {
		return err
	}
	go factionmgr.RemoveFrom(guildID, target.ID, mine.Faction.RoleID)
	return helpers.EditInteractionEmbed(s, i, FactionKickedEmbed(target.ID, mine.Faction.Name))
}

func factionRole(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption, promote bool) error {
	target := helpers.SubOptionUser(s, options, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, FactionPlayerNotFoundEmbed())
	}
	mine, err, ok := myFaction(s, i, guildID, userID)
	if !ok {
		return err
	}
	role := "member"
	if promote {
		role = "officer"
	}
	if err := database.FactionSetRole(guildID, mine.Faction.ID, userID, target.ID, role); err != nil {
		return err
	}
	return helpers.EditInteractionEmbed(s, i, FactionRoleChangedEmbed(target.ID, mine.Faction.Name, promote))
}

func factionTransfer(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	target := helpers.SubOptionUser(s, options, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, FactionPlayerNotFoundEmbed())
	}
	mine, err, ok := myFaction(s, i, guildID, userID)
	if !ok {
		return err
	}
	if err := database.FactionTransfer(guildID, mine.Faction.ID, userID, target.ID); err != nil {
		return err
	}
	return helpers.EditInteractionEmbed(s, i, FactionTransferredEmbed(target.ID, mine.Faction.Name))
}

func factionDisband(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	mine, err, ok := myFaction(s, i, guildID, userID)
	if !ok {
		return err
	}
	if !helpers.SubOptionBool(options, "confirm") {
		return helpers.EditInteractionEmbed(s, i, FactionDisbandUnconfirmedEmbed(mine.Faction.Name))
	}
	out, err := database.FactionDisband(guildID, mine.Faction.ID, userID, false)
	if err != nil {
		return err
	}
	go func() {
		factionmgr.DeleteRole(guildID, out.Faction.RoleID)
		for _, id := range out.Members {
			factionmgr.RemoveFrom(guildID, id, "")
		}
	}()
	return helpers.EditInteractionEmbed(s, i, FactionDisbandedEmbed(out.Faction.Name))
}

func factionCustomize(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	mine, err, ok := myFaction(s, i, guildID, userID)
	if !ok {
		return err
	}
	fields := map[string]any{}
	var changed []string
	for _, name := range []string{"name", "tag", "color", "icon", "symbol"} {
		if v := helpers.SubOptionString(options, name); strings.TrimSpace(v) != "" {
			fields[name] = v
			changed = append(changed, name)
		}
	}
	if len(fields) == 0 {
		return helpers.EditInteractionEmbed(s, i, FactionNothingToChangeEmbed())
	}
	p, err := database.FactionCustomize(guildID, mine.Faction.ID, userID, false, fields)
	if err != nil {
		return err
	}
	go factionmgr.SyncFaction(guildID, p)
	return helpers.EditInteractionEmbed(s, i, FactionCustomizedEmbed(p, changed))
}

// ============================================================================
// Buttons
// ============================================================================

func factionInviteButtons(factionID uint, targetID string) []discordgo.MessageComponent {
	id := strconv.FormatUint(uint64(factionID), 10)
	return InviteButtons("faction", id, targetID)
}

// handleFactionInvite answers an invitation when its button is pressed. Only the invited player can.
func handleFactionInvite(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	m := factionInviteButton.FindStringSubmatch(customID)
	if m == nil {
		return false
	}
	accept := m[1] == "accept"
	factionID, _ := strconv.ParseUint(m[2], 10, 64)
	userID := helpers.InteractionUserID(i)
	if userID != m[3] {
		_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral, Embeds: []*discordgo.MessageEmbed{FactionNotYourInvitationEmbed()}},
		})
		return true
	}
	p, err := database.FactionAnswer(i.GuildID, userID, uint(factionID), accept)
	var embed *discordgo.MessageEmbed
	switch {
	case err != nil && database.FactionMessage(err) != "":
		embed = FactionRefusedEmbed(database.FactionMessage(err))
	case err != nil:
		log.Printf("[Faction] invitation button in %s: %v", i.GuildID, err)
		embed = UnexpectedErrorFactionEmbed()
	case !accept:
		embed = FactionDeclinedEmbed()
	default:
		embed = FactionJoinedEmbed(p)
		go factionmgr.SyncFaction(i.GuildID, p)
	}
	// The answer replaces the invitation, so its buttons are gone.
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Components: []discordgo.MessageComponent{}},
	})
	return true
}

// handleFactionLeaderboardPage turns the page of a faction leaderboard. Only the player who ran the command turns its pages.
func handleFactionLeaderboardPage(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	rest, ok := strings.CutPrefix(customID, factionLeaderboardPrefix)
	if !ok {
		return false
	}
	target, board, found := strings.Cut(rest, "|")
	page, err := strconv.Atoi(target)
	if !found || err != nil || page < 1 {
		return true // an inert button: nothing to do
	}
	if owner := pagination.MessageOwner(i); owner != "" && owner != helpers.InteractionUserID(i) {
		_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral, Embeds: []*discordgo.MessageEmbed{
				helpers.Preset("error", types.PresetOptions{Title: "Permission Denied", Description: "Only the player who ran this command can change pages. Run `/faction leaderboard` to see your own."}),
			}},
		})
		return true
	}
	embed, components := buildFactionLeaderboard(i.GuildID, board, page)
	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Components: components},
	})
	return true
}

// ============================================================================
// Helper functions
// ============================================================================

// factionColor is the colour of an embed about a faction: its own, or 0 (the preset's) when it has none.
func factionColor(p *database.FactionProfile) int { return factionmgr.ColorInt(p.Color) }

// factionHeading is a faction's name with its tag in front, as members see it.
func factionHeading(name, tag string) string {
	if tag == "" {
		return name
	}
	return tag + " " + name
}

func progressBar(have, from, to int) string {
	if to <= from {
		return "▰▰▰▰▰▰▰▰▰▰"
	}
	filled := min(max((have-from)*10/(to-from), 0), 10)
	return strings.Repeat("▰", filled) + strings.Repeat("▱", 10-filled)
}

func factionMentions(members []database.FactionMember, role string) string {
	var ids []string
	for _, m := range members {
		if m.Role == role {
			ids = append(ids, "<@"+m.SnowflakeID+">")
		}
	}
	if len(ids) == 0 {
		return "—"
	}
	return strings.Join(ids, "\n")
}

// factionNextUnlock names the next cosmetic reward the faction has not reached yet, or "".
func factionNextUnlock(p *database.FactionProfile, cfg *database.FactionConfig) string {
	if cfg == nil {
		return ""
	}
	next, nextName := 0, ""
	for _, r := range []struct {
		at   int
		name string
	}{{cfg.Unlocks.Tag, "Faction Tag"}, {cfg.Unlocks.Color, "Faction Colour"}, {cfg.Unlocks.Role, "Discord Faction Role"}, {cfg.Unlocks.Icon, "Faction Icon"},
		{cfg.Unlocks.Advanced, "Advanced Customization"}, {cfg.Unlocks.Prestige, "Prestige Badge"}, {cfg.Unlocks.Legendary, "Legendary Status"}} {
		if r.at > p.Level && (next == 0 || r.at < next) {
			next, nextName = r.at, r.name
		}
	}
	if next == 0 {
		return ""
	}
	return fmt.Sprintf("**%s** at level %d", nextName, next)
}

// ============================================================================
// Embeds
// ============================================================================

func FactionInvalidSubcommandEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "That faction subcommand does not exist. Use the slash command menu to see the available options.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func UnexpectedErrorFactionEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while processing the faction request. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionRefusedEmbed(message string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Faction Request Refused",
		Description: message,
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionRegistrationRequiredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must be registered to use factions. Run `/register` first, then try again.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionNotInFactionEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not in a Faction",
		Description: "You are not in a faction. Use `/faction create` to found one or ask a faction's Founder or an Officer to invite you.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionPlayerHasNoneEmbed(userID string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Faction",
		Description: fmt.Sprintf("<@%s> is not in a faction.", userID),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionNotFoundEmbed(name string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Faction Not Found",
		Description: fmt.Sprintf("No faction is named or tagged **%s**. Check the spelling or use `/faction leaderboard` to browse the factions.", name),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionPlayerNotFoundEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: "Could not find that player. Select a user from the command options.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be in a faction. Select a registered player instead.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionTargetNotRegisteredEmbed(userID string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: fmt.Sprintf("<@%s> is not registered in this guild. They must run `/register` before they can join a faction.", userID),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionNotYourInvitationEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not Your Invitation",
		Description: "Only the invited player can answer this invitation.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionNothingToChangeEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Nothing to Change",
		Description: "Fill in at least one option: `name`, `tag`, `color`, `icon` or `symbol`.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionCreatedEmbed(p *database.FactionProfile) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Faction Founded",
		Description: fmt.Sprintf("**%s** `[%s]` is now a faction, and you are its **Founder**.\n\n> **Seats:** %d (%d Officer)\n> **Level:** %d\n\nInvite players with `/faction invite`. Every ranked win by a member earns the faction trophies.", p.Faction.Name, p.Faction.Tag, p.Slots, p.OfficerSlots, p.Level),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionInviteSentEmbed(targetID, faction string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Invite Sent",
		Description: fmt.Sprintf("Invited <@%s> to **%s**. They can accept with the buttons below the invitation or with `/faction accept`.", targetID, faction),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionInviteSentNoPostEmbed(targetID, faction string) *discordgo.MessageEmbed {
	return helpers.Preset("caution", types.PresetOptions{
		Title:       "Invite Saved",
		Description: fmt.Sprintf("Invited <@%s> to **%s**, but the invitation message could not be posted here. Ask them to run `/faction accept`.", targetID, faction),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionInvitationEmbed(p *database.FactionProfile, inviterID string, expires time.Time) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Faction Invitation",
		Description: fmt.Sprintf("<@%s> invited you to join **%s**.\n\n> **Level:** %d\n> **Members:** %d/%d\n> **Founder:** <@%s>\n> **Expires:** <t:%d:R>", inviterID, factionHeading(p.Faction.Name, p.DisplayTag), p.Level, len(p.Members), p.Slots, p.Founder, expires.Unix()),
		Color:       factionColor(p),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionInvitesEmbed(list []database.FactionInvite) *discordgo.MessageEmbed {
	if len(list) == 0 {
		return helpers.Preset("info", types.PresetOptions{
			Title:       "No Invitations",
			Description: "You have no faction invitations right now. Ask a faction's Founder or an Officer to invite you.",
			Thumbnail:   helpers.ThumbPlayers,
		})
	}
	var b strings.Builder
	for _, inv := range list {
		fmt.Fprintf(&b, "> **%s** `[%s]` · from <@%s> · expires <t:%d:R>\n", inv.FactionName, inv.FactionTag, inv.InvitedBy, inv.ExpiresAt.Unix())
	}
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Faction Invitations",
		Description: "Accept one with `/faction accept`, or decline it with `/faction decline`.\n\n" + b.String(),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionJoinedEmbed(p *database.FactionProfile) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Faction Joined",
		Description: fmt.Sprintf("You joined **%s**!\n\n> **Members:** %d/%d\n> **Level:** %d\n> **Founder:** <@%s>\n\nEvery ranked win you earn adds trophies to the faction.", factionHeading(p.Faction.Name, p.DisplayTag), len(p.Members), p.Slots, p.Level, p.Founder),
		Color:       factionColor(p),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionDeclinedEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Invitation Declined",
		Description: "You declined the faction invitation. If you change your mind, ask for a new one.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionLeftEmbed(name string) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Faction Left",
		Description: fmt.Sprintf("You left **%s**. You can create a faction with `/faction create` or accept another invitation.", name),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionKickedEmbed(targetID, name string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Member Removed",
		Description: fmt.Sprintf("Removed <@%s> from **%s**.", targetID, name),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionRoleChangedEmbed(targetID, name string, promoted bool) *discordgo.MessageEmbed {
	if promoted {
		return helpers.Preset("success", types.PresetOptions{
			Title:       "Member Promoted",
			Description: fmt.Sprintf("<@%s> is now an **Officer** of **%s**. They can invite players and remove members.", targetID, name),
			Thumbnail:   helpers.ThumbPlayers,
		})
	}
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Officer Demoted",
		Description: fmt.Sprintf("<@%s> is now a **Member** of **%s**.", targetID, name),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionTransferredEmbed(targetID, name string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Ownership Transferred",
		Description: fmt.Sprintf("<@%s> is now the **Founder** of **%s**. You stay in the faction as an Officer, or a Member when no Officer seat is free.", targetID, name),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionDisbandUnconfirmedEmbed(name string) *discordgo.MessageEmbed {
	return helpers.Preset("caution", types.PresetOptions{
		Title:       "Confirm Disband",
		Description: fmt.Sprintf("Disbanding **%s** removes every member and erases its trophies and badges. This cannot be undone. Run `/faction disband` again with `confirm` set to true.", name),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionDisbandedEmbed(name string) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Faction Disbanded",
		Description: fmt.Sprintf("**%s** has been disbanded. You can create a new faction with `/faction create`.", name),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionCustomizedEmbed(p *database.FactionProfile, changed []string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Faction Updated",
		Description: fmt.Sprintf("Updated the %s of **%s**. Members' names and the faction role are refreshing now.\n\n> **Tag:** %s", strings.Join(changed, ", "), p.Faction.Name, orDash(p.DisplayTag)),
		Color:       factionColor(p),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// FactionInfoEmbed is a faction's profile: name and tag, level, trophies, rank, members, founder, officers, top contributors, cosmetics
// and badges, and when it was created.
func FactionInfoEmbed(p *database.FactionProfile, cfg *database.FactionConfig) *discordgo.MessageEmbed {
	heading := factionHeading(p.Faction.Name, p.DisplayTag)
	if p.Faction.Icon != "" && !strings.HasPrefix(p.Faction.Icon, "http") {
		heading = p.Faction.Icon + " " + heading
	}
	level := fmt.Sprintf("%d", p.Level)
	if p.Level >= p.MaxLevel {
		level += " (max)"
	}
	standing := "> **Level:** " + level
	if p.Prestige != "" {
		standing += " · **" + p.Prestige + "**"
	}
	standing += "\n> **Founder:** <@" + p.Founder + ">"
	if p.NextLevelAt > 0 {
		standing += fmt.Sprintf("\n> **Progress:** %s %s / %s", progressBar(p.LifetimeTrophies, p.LevelStartAt, p.NextLevelAt), factionmgr.Thousands(p.LifetimeTrophies), factionmgr.Trophies(p.NextLevelAt))
	}

	fields := []*discordgo.MessageEmbedField{
		{Name: "Lifetime Trophies", Value: factionmgr.Trophies(p.LifetimeTrophies), Inline: true},
		{Name: "Seasonal Trophies", Value: factionmgr.Trophies(p.SeasonTrophies), Inline: true},
		{Name: "Rank", Value: fmt.Sprintf("Lifetime **#%d**\nSeasonal **#%d**", p.LifetimeRank, max(p.SeasonRank, 1)), Inline: true},
		{Name: fmt.Sprintf("Members (%d/%d)", len(p.Members), p.Slots), Value: factionMentions(p.Members, "member"), Inline: true},
		{Name: fmt.Sprintf("Officers (%d/%d)", countRole(p.Members, "officer"), p.OfficerSlots), Value: factionMentions(p.Members, "officer"), Inline: true},
	}
	if len(p.Contributors) > 0 {
		var b strings.Builder
		for _, m := range p.Contributors {
			fmt.Fprintf(&b, "<@%s> — %s\n", m.SnowflakeID, factionmgr.Trophies(m.Trophies))
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Top Contributors", Value: b.String(), Inline: false})
	}
	var perks []string
	for _, u := range []struct {
		on   bool
		name string
	}{{p.Unlocked.Tag, "Tag"}, {p.Unlocked.Color, "Colour"}, {p.Unlocked.Role, "Role"}, {p.Unlocked.Icon, "Icon"}, {p.Unlocked.Advanced, "Advanced"}, {p.Unlocked.Prestige, "Prestige"}, {p.Unlocked.Legendary, "Legendary"}} {
		if u.on {
			perks = append(perks, u.name)
		}
	}
	cosmetics := "None yet"
	if len(perks) > 0 {
		cosmetics = strings.Join(perks, " · ")
	}
	if next := factionNextUnlock(p, cfg); next != "" {
		cosmetics += "\n*Next:* " + next
	}
	fields = append(fields, &discordgo.MessageEmbedField{Name: "Unlocked Cosmetics", Value: cosmetics, Inline: false})
	if len(p.Badges) > 0 {
		var titles []string
		for _, b := range p.Badges {
			titles = append(titles, "🏅 "+b.Title)
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Seasonal Badges", Value: strings.Join(titles, "\n"), Inline: false})
	}
	fields = append(fields, &discordgo.MessageEmbedField{Name: "Created", Value: fmt.Sprintf("<t:%d:D>", p.Faction.CreatedAt.Unix()), Inline: true})

	embed := helpers.Preset("info", types.PresetOptions{
		Title:       heading,
		Description: standing,
		Fields:      fields,
		Color:       factionColor(p),
		Thumbnail:   helpers.ThumbPlayers,
	})
	if strings.HasPrefix(p.Faction.Icon, "http") {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: p.Faction.Icon}
	}
	return embed
}

func countRole(members []database.FactionMember, role string) int {
	n := 0
	for _, m := range members {
		if m.Role == role {
			n++
		}
	}
	return n
}

func FactionLeaderboardEmptyEmbed(board string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Faction Leaderboard · " + strings.ToUpper(board[:1]) + board[1:],
		Description: "No factions exist yet. Be the first with `/faction create`.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionLeaderboardEmptyPageEmbed(page int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Page Empty",
		Description: fmt.Sprintf("There are no leaderboard entries on page `%d`. Try a lower page number.", page),
		Thumbnail:   helpers.ThumbFlag,
	})
}

// FactionLeaderboardEmbed lists a page of factions with their level, trophies and rank.
func FactionLeaderboardEmbed(rows []database.FactionRow, board string, page, totalPages int) *discordgo.MessageEmbed {
	var b strings.Builder
	for _, r := range rows {
		trophies := r.LifetimeTrophies
		if board == "seasonal" {
			trophies = r.SeasonTrophies
		}
		fmt.Fprintf(&b, "**#%d** %s · Lv **%d** · %s · %d members", r.Rank, factionHeading(r.Name, r.DisplayTag), r.Level, factionmgr.Trophies(trophies), r.MemberCount)
		if r.Prestige != "" {
			b.WriteString(" · " + r.Prestige)
		}
		b.WriteString("\n")
	}
	title := "Faction Leaderboard · Lifetime"
	if board == "seasonal" {
		title = "Faction Leaderboard · Seasonal"
	}
	return helpers.Preset("success", types.PresetOptions{
		Title:       title,
		Description: b.String(),
		FooterText:  fmt.Sprintf("Page %d/%d", page, totalPages),
		Thumbnail:   helpers.ThumbPlayers,
	})
}
