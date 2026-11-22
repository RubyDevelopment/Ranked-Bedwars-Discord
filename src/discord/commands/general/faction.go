package general

import (
	"errors"
	"log"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// The faction commands are split by what they do:
//
//	faction.go         the command, its options and the dispatch (this file)
//	faction_manage.go  founding, inviting, roles, customising and leaving
//	faction_view.go    /faction info, a profile with Home, Members, Levels and Achievements tabs
//	faction_levels.go  /faction level and /faction levels
//	faction_board.go   /faction leaderboard
//	faction_embeds.go  every embed they answer with
//	faction_util.go    small helpers the embeds share
//
// /factionadmin (staff) lives in commands/staff and answers through FactionFinish and FactionReply, like these.

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(FactionCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

func factionNameOption(required bool) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Name: "faction", Description: "A faction's name or tag (default: your own)", Type: discordgo.ApplicationCommandOptionString, Required: required}
}

func factionUserOption(description string) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Name: "user", Description: description, Type: discordgo.ApplicationCommandOptionUser, Required: true}
}

func factionSub(name, description string, options ...*discordgo.ApplicationCommandOption) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Name: name, Description: description, Type: discordgo.ApplicationCommandOptionSubCommand, Options: options}
}

func factionPageOption(description string) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Name: "page", Description: description, Type: discordgo.ApplicationCommandOptionInteger, MinValue: ptrToFloat64(1)}
}

func factionTextOption(name, description string, required bool) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Name: name, Description: description, Type: discordgo.ApplicationCommandOptionString, Required: required}
}

// FactionCommand creates and manages factions: invites, roles, customisation, and the faction leaderboards.
var FactionCommand = &types.Command{
	Name:         "faction",
	Requirements: types.FactionRequirements,
	Description:  "Create and manage your faction: members, roles, cosmetics and the faction leaderboards.",
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			// Look
			factionSub("info", "View a faction's profile", factionNameOption(false), &discordgo.ApplicationCommandOption{Name: "user", Description: "Show the faction this player is in (default: yours)", Type: discordgo.ApplicationCommandOptionUser}),
			factionSub("level", "Your faction's level and rewards, or what a level takes and unlocks",
				&discordgo.ApplicationCommandOption{Name: "level", Description: "Show this level of the ruleset", Type: discordgo.ApplicationCommandOptionInteger, MinValue: ptrToFloat64(1)}, factionNameOption(false)),
			factionSub("levels", "Every faction level with its trophies, seats and rewards", factionPageOption("The page to display")),
			factionSub("leaderboard", "View the lifetime or seasonal faction leaderboard",
				&discordgo.ApplicationCommandOption{Name: "board", Description: "Lifetime trophies or this season's trophies (default: lifetime)", Type: discordgo.ApplicationCommandOptionString,
					Choices: []*discordgo.ApplicationCommandOptionChoice{{Name: "Lifetime", Value: "lifetime"}, {Name: "Seasonal", Value: "seasonal"}}},
				factionPageOption("The page of the leaderboard to display")),

			// Join and leave
			factionSub("create", "Found a new faction", factionTextOption("name", "The faction's name", true), factionTextOption("tag", "A short tag, shown next to its members' names", true)),
			factionSub("invites", "See the faction invitations waiting for you"),
			factionSub("accept", "Accept a faction invitation", factionNameOption(false)),
			factionSub("decline", "Decline a faction invitation", factionNameOption(false)),
			factionSub("leave", "Leave your faction"),

			// Run it
			factionSub("invite", "Invite a player to your faction", factionUserOption("Player to invite")),
			factionSub("kick", "Remove a member from your faction", factionUserOption("Member to remove")),
			factionSub("promote", "Make a member an Officer", factionUserOption("Member to promote")),
			factionSub("demote", "Make an Officer a plain Member", factionUserOption("Officer to demote")),
			factionSub("transfer", "Hand your faction to another member", factionUserOption("New Founder")),
			factionSub("customize", "Change your faction's name, tag, colour, icon or tag symbol",
				factionTextOption("name", "A new name", false), factionTextOption("tag", "A new tag", false),
				factionTextOption("color", "A faction colour by name, like Red or Blue (unlocked by level)", false),
				factionTextOption("icon", "An emoji or image link (unlocked by level)", false),
				factionTextOption("symbol", "A symbol around the tag, like ✦ (unlocked by level)", false)),
			factionSub("disband", "Permanently disband your faction",
				&discordgo.ApplicationCommandOption{Name: "confirm", Description: "true to confirm: this cannot be undone", Type: discordgo.ApplicationCommandOptionBoolean, Required: true}),
		},
	},
	Execute: executeFactionCommand,
}

// factionSubcommands maps each subcommand to its handler.
var factionSubcommands = map[string]func(*factionCtx) error{
	"info": factionInfo, "level": factionLevel, "levels": factionLevels, "leaderboard": factionLeaderboard,
	"create": factionCreate, "invites": factionInvites, "accept": factionAnswer(true), "decline": factionAnswer(false), "leave": factionLeave,
	"invite": factionInvite, "kick": factionKick, "promote": factionRole(true), "demote": factionRole(false),
	"transfer": factionTransfer, "customize": factionCustomize, "disband": factionDisband,
}

// ============================================================================
// Command Execution
// ============================================================================

// factionCtx is one run of a faction subcommand: who ran it, where, and with which options.
type factionCtx struct {
	s       *discordgo.Session
	i       *discordgo.InteractionCreate
	guildID string
	userID  string
	options []*discordgo.ApplicationCommandInteractionDataOption
}

func (c *factionCtx) str(name string) string  { return helpers.SubOptionString(c.options, name) }
func (c *factionCtx) integer(name string) int { return helpers.SubOptionInt(c.options, name) }

// send answers with an embed, and its buttons when it has any. An embed with buttons is sent as built, without the server's saved
// response style: pressing a button replaces the message as built too, so the style would show on the first view only.
func (c *factionCtx) send(embed *discordgo.MessageEmbed, components ...discordgo.MessageComponent) error {
	if len(components) > 0 && !helpers.IsPrefixInteraction(c.i) {
		helpers.WithGuildFooterIcon(c.s, c.i, embed)
		_, err := c.s.InteractionResponseEdit(c.i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}, Components: &components})
		return err
	}
	if len(components) > 0 {
		return helpers.EditInteractionComponents(c.s, c.i, embed, components)
	}
	return helpers.EditInteractionEmbed(c.s, c.i, embed)
}

// mine is the invoker's faction; for a player with none it answers "not in a faction".
func (c *factionCtx) mine() (*database.FactionProfile, error) {
	p, err := database.FactionFor(c.guildID, c.userID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, FactionReply(FactionNotInFactionEmbed())
	}
	return p, nil
}

// target is the player picked in the "user" option; a missing one, or a bot, answers why it cannot be used.
func (c *factionCtx) target() (*discordgo.User, error) {
	u := helpers.SubOptionUser(c.s, c.options, "user")
	switch {
	case u == nil:
		return nil, FactionReply(FactionPlayerNotFoundEmbed())
	case u.Bot:
		return nil, FactionReply(FactionBotTargetEmbed())
	}
	return u, nil
}

// named is the faction called name; one that does not exist answers "not found".
func (c *factionCtx) named(name string) (*database.FactionProfile, error) {
	p, err := database.FactionNamed(c.guildID, name)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, FactionReply(FactionNotFoundEmbed(name))
	}
	return p, nil
}

func executeFactionCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	subcommand, options := types.SelectedSubcommand(i)
	handler := factionSubcommands[subcommand]
	if handler == nil {
		return helpers.EditInteractionEmbed(s, i, FactionInvalidSubcommandEmbed())
	}
	c := &factionCtx{s: s, i: i, guildID: i.GuildID, userID: helpers.InteractionUserID(i), options: options}

	// Everything here is for registered players.
	if _, err := database.Must().Player.Get(c.guildID, c.userID); err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, FactionRegistrationRequiredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorFactionEmbed())
	}
	return FactionFinish(s, i, "faction", subcommand, handler(c))
}

// ============================================================================
// Answering
// ============================================================================

// factionReply is an error that carries the embed to answer with: something that stops a command without being a failure, like
// "you are not in a faction".
type factionReply struct{ embed *discordgo.MessageEmbed }

func (r factionReply) Error() string { return r.embed.Title }

// FactionReply stops a faction command and answers with embed.
func FactionReply(embed *discordgo.MessageEmbed) error { return factionReply{embed} }

// FactionFinish ends a faction command: nothing more for nil, the embed of a FactionReply, the database's refusal message, and for
// anything else a logged error and an "unexpected error" embed.
func FactionFinish(s *discordgo.Session, i *discordgo.InteractionCreate, command, subcommand string, err error) error {
	var reply factionReply
	switch {
	case err == nil:
		return nil
	case errors.As(err, &reply):
		return helpers.EditInteractionEmbed(s, i, reply.embed)
	}
	if msg := database.FactionMessage(err); msg != "" {
		return helpers.EditInteractionEmbed(s, i, FactionRefusedEmbed(msg))
	}
	log.Printf("[Faction] /%s %s in %s: %v", command, subcommand, i.GuildID, err)
	return helpers.EditInteractionEmbed(s, i, UnexpectedErrorFactionEmbed())
}
