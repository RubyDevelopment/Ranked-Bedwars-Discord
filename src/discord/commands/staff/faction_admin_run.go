package staff

import (
	"fmt"
	"strings"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/commands/general"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/factionmgr"
	"ranked-bedwars/src/types"
)

// The handlers of /factionadmin (its options are in faction_admin.go). They answer through general.FactionFinish and
// general.FactionReply, like /faction does.

// factionAdminSubcommands maps each subcommand to its handler.
var factionAdminSubcommands = map[string]func(*factionAdminCtx) error{
	"create": factionAdminCreate, "delete": factionAdminDelete, "customize": factionAdminCustomize, "add": factionAdminAdd,
	"kick": factionAdminKick, "promote": factionAdminRole("officer"), "demote": factionAdminRole("member"),
	"transfer": factionAdminTransfer, "trophies": factionAdminTrophies,
}

// factionAdminCtx is one run of a /factionadmin subcommand.
type factionAdminCtx struct {
	s       *discordgo.Session
	i       *discordgo.InteractionCreate
	guildID string
	options []*discordgo.ApplicationCommandInteractionDataOption
}

func (c *factionAdminCtx) str(name string) string { return helpers.SubOptionString(c.options, name) }

// done answers a success.
func (c *factionAdminCtx) done(title, text string) error {
	return helpers.EditInteractionEmbed(c.s, c.i, helpers.Preset("success", types.PresetOptions{Title: title, Description: text, Thumbnail: helpers.ThumbPlayers}))
}

// refuseFactionAdmin stops the command with an error embed.
func refuseFactionAdmin(title, text string) error {
	return general.FactionReply(helpers.Preset("error", types.PresetOptions{Title: title, Description: text, Thumbnail: helpers.ThumbFlag}))
}

// faction is the faction named in the "faction" option.
func (c *factionAdminCtx) faction() (*database.FactionProfile, error) {
	name := c.str("faction")
	p, err := database.FactionNamed(c.guildID, name)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, general.FactionReply(general.FactionNotFoundEmbed(name))
	}
	return p, nil
}

// target is the "user" option: a real player who is registered.
func (c *factionAdminCtx) target() (*discordgo.User, error) {
	u := helpers.SubOptionUser(c.s, c.options, "user")
	if u == nil || u.Bot {
		return nil, general.FactionReply(general.FactionPlayerNotFoundEmbed())
	}
	if _, err := database.Must().Player.Get(c.guildID, u.ID); err != nil {
		if sdk.IsNotFound(err) {
			return nil, general.FactionReply(general.FactionTargetNotRegisteredEmbed(u.ID))
		}
		return nil, err
	}
	return u, nil
}

// targetFaction is the "user" option together with the faction that player is in.
func (c *factionAdminCtx) targetFaction() (*discordgo.User, *database.FactionProfile, error) {
	u, err := c.target()
	if err != nil {
		return nil, nil, err
	}
	p, err := database.FactionFor(c.guildID, u.ID)
	if err != nil {
		return nil, nil, err
	}
	if p == nil {
		return nil, nil, general.FactionReply(general.FactionPlayerHasNoneEmbed(u.ID))
	}
	return u, p, nil
}

func executeFactionAdmin(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	sub, options := types.SelectedSubcommand(i)
	handler := factionAdminSubcommands[sub]
	if handler == nil {
		return helpers.EditInteractionEmbed(s, i, general.FactionInvalidSubcommandEmbed())
	}
	return general.FactionFinish(s, i, "factionadmin", sub, handler(&factionAdminCtx{s: s, i: i, guildID: i.GuildID, options: options}))
}

func factionAdminCreate(c *factionAdminCtx) error {
	u, err := c.target()
	if err != nil {
		return err
	}
	p, err := database.FactionAdminCreate(c.guildID, u.ID, c.str("name"), c.str("tag"))
	if err != nil {
		return err
	}
	go factionmgr.RefreshMember(c.guildID, u.ID)
	return c.done("Faction Created", fmt.Sprintf("**%s** `[%s]` was created with <@%s> as its Founder.", p.Faction.Name, p.Faction.Tag, u.ID))
}

func factionAdminDelete(c *factionAdminCtx) error {
	p, err := c.faction()
	if err != nil {
		return err
	}
	if !helpers.SubOptionBool(c.options, "confirm") {
		return refuseFactionAdmin("Confirm Delete", fmt.Sprintf("Deleting **%s** removes all %d members and erases its trophies and badges. Run it again with `confirm` set to true.", p.Faction.Name, len(p.Members)))
	}
	out, err := database.FactionDisband(c.guildID, p.Faction.ID, "", true)
	if err != nil {
		return err
	}
	go func() {
		factionmgr.DeleteRole(c.guildID, out.Faction.RoleID)
		for _, id := range out.Members {
			factionmgr.RemoveFrom(c.guildID, id, "")
		}
	}()
	return c.done("Faction Deleted", fmt.Sprintf("**%s** was deleted.", out.Faction.Name))
}

func factionAdminCustomize(c *factionAdminCtx) error {
	p, err := c.faction()
	if err != nil {
		return err
	}
	fields, changed := map[string]any{}, []string{}
	for _, name := range []string{"name", "tag", "color", "icon", "symbol"} {
		if v := c.str(name); strings.TrimSpace(v) != "" {
			fields[name] = v
			changed = append(changed, name)
		}
	}
	if len(fields) == 0 {
		return general.FactionReply(general.FactionNothingToChangeEmbed())
	}
	if p, err = database.FactionCustomize(c.guildID, p.Faction.ID, "", true, fields); err != nil {
		return err
	}
	go factionmgr.SyncFaction(c.guildID, p)
	return helpers.EditInteractionEmbed(c.s, c.i, general.FactionCustomizedEmbed(p, changed))
}

func factionAdminAdd(c *factionAdminCtx) error {
	p, err := c.faction()
	if err != nil {
		return err
	}
	u, err := c.target()
	if err != nil {
		return err
	}
	if p, err = database.FactionAdminAdd(c.guildID, p.Faction.ID, u.ID); err != nil {
		return err
	}
	go factionmgr.SyncFaction(c.guildID, p)
	return c.done("Member Added", fmt.Sprintf("<@%s> is now in **%s**.", u.ID, p.Faction.Name))
}

func factionAdminKick(c *factionAdminCtx) error {
	u, p, err := c.targetFaction()
	if err != nil {
		return err
	}
	if err := database.FactionAdminKick(c.guildID, p.Faction.ID, u.ID); err != nil {
		return err
	}
	go factionmgr.RemoveFrom(c.guildID, u.ID, p.Faction.RoleID)
	return c.done("Member Removed", fmt.Sprintf("Removed <@%s> from **%s**.", u.ID, p.Faction.Name))
}

// factionAdminRole makes the player an "officer" or a "member" of their faction.
func factionAdminRole(role string) func(*factionAdminCtx) error {
	return func(c *factionAdminCtx) error {
		u, p, err := c.targetFaction()
		if err != nil {
			return err
		}
		if err := database.FactionAdminRole(c.guildID, p.Faction.ID, u.ID, role); err != nil {
			return err
		}
		article := "a"
		if role == "officer" {
			article = "an"
		}
		return c.done("Role Changed", fmt.Sprintf("<@%s> is now %s **%s** of **%s**.", u.ID, article, strings.ToUpper(role[:1])+role[1:], p.Faction.Name))
	}
}

func factionAdminTransfer(c *factionAdminCtx) error {
	u, p, err := c.targetFaction()
	if err != nil {
		return err
	}
	if err := database.FactionAdminTransfer(c.guildID, p.Faction.ID, u.ID); err != nil {
		return err
	}
	return c.done("Ownership Transferred", fmt.Sprintf("<@%s> is now the **Founder** of **%s**.", u.ID, p.Faction.Name))
}

func factionAdminTrophies(c *factionAdminCtx) error {
	p, err := c.faction()
	if err != nil {
		return err
	}
	board, mode, amount := c.str("board"), c.str("mode"), helpers.SubOptionInt(c.options, "amount")
	after, err := database.FactionAdminTrophies(c.guildID, p.Faction.ID, board, mode, amount)
	if err != nil {
		return err
	}
	now, was := after.LifetimeTrophies, p.LifetimeTrophies
	if board == "seasonal" {
		now, was = after.SeasonTrophies, p.SeasonTrophies
	}
	go factionmgr.SyncFaction(c.guildID, after) // a new level can unlock the tag and the role
	return c.done("Trophies Updated", fmt.Sprintf("**%s** %s trophies: %s → %s (level %d).", after.Faction.Name, board, factionmgr.Trophies(was), factionmgr.Trophies(now), after.Level))
}
