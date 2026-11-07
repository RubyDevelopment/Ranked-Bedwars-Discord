package staff

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/commands/general"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/factionmgr"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.Register(FactionAdminCommand)
}

// FactionAdminCommand is staff control over every faction: found, delete, rename, move players, change roles and trophies. None of the
// player-side limits (Founder only, levels, seat counts, creation requirements) apply.
var FactionAdminCommand = &types.Command{
	Name:         "factionadmin",
	Requirements: types.StaffRequirements,
	Description:  "Staff controls for factions: create, delete, edit, move members and adjust trophies.",
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{Options: func() []*discordgo.ApplicationCommandOption {
		str := func(name, desc string, required bool) *discordgo.ApplicationCommandOption {
			return &discordgo.ApplicationCommandOption{Name: name, Description: desc, Type: discordgo.ApplicationCommandOptionString, Required: required}
		}
		user := func(desc string) *discordgo.ApplicationCommandOption {
			return &discordgo.ApplicationCommandOption{Name: "user", Description: desc, Type: discordgo.ApplicationCommandOptionUser, Required: true}
		}
		faction := str("faction", "The faction's name or tag", true)
		sub := func(name, desc string, opts ...*discordgo.ApplicationCommandOption) *discordgo.ApplicationCommandOption {
			return &discordgo.ApplicationCommandOption{Name: name, Description: desc, Type: discordgo.ApplicationCommandOptionSubCommand, Options: opts}
		}
		choices := func(pairs ...string) []*discordgo.ApplicationCommandOptionChoice {
			var out []*discordgo.ApplicationCommandOptionChoice
			for _, p := range pairs {
				out = append(out, &discordgo.ApplicationCommandOptionChoice{Name: p, Value: strings.ToLower(p)})
			}
			return out
		}
		board := str("board", "Which tally to change", true)
		board.Choices = choices("Lifetime", "Seasonal")
		mode := str("mode", "Add (use a negative number to remove) or set it exactly", true)
		mode.Choices = choices("Add", "Set")
		amount := &discordgo.ApplicationCommandOption{Name: "amount", Description: "Trophies", Type: discordgo.ApplicationCommandOptionInteger, Required: true, MinValue: func() *float64 { v := -1000000.0; return &v }()}
		confirm := &discordgo.ApplicationCommandOption{Name: "confirm", Description: "true to confirm: this cannot be undone", Type: discordgo.ApplicationCommandOptionBoolean, Required: true}
		return []*discordgo.ApplicationCommandOption{
			sub("create", "Found a faction for a player, whatever the requirements say", user("The Founder"), str("name", "The faction's name", true), str("tag", "The faction's tag", true)),
			sub("delete", "Delete a faction", faction, confirm),
			sub("customize", "Change a faction's name, tag, colour, icon or symbol (no level needed)", faction,
				str("name", "A new name", false), str("tag", "A new tag", false), str("color", "A colour like #FF5555", false), str("icon", "An emoji or image link", false), str("symbol", "A symbol around the tag", false)),
			sub("add", "Put a player in a faction without an invitation", faction, user("The player")),
			sub("kick", "Remove a player from their faction (not its Founder)", user("The player")),
			sub("promote", "Make a player an Officer of their faction", user("The player")),
			sub("demote", "Make an Officer of a faction a plain Member", user("The player")),
			sub("transfer", "Make a player the Founder of their faction", user("The new Founder")),
			sub("trophies", "Add, remove or set a faction's trophies", faction, board, mode, amount),
		}
	}()},
	Execute: executeFactionAdmin,
}

func executeFactionAdmin(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	sub, opts := types.SelectedSubcommand(i)
	guildID := i.GuildID
	reply := func(e *discordgo.MessageEmbed) error { return helpers.EditInteractionEmbed(s, i, e) }
	fail := func(title, text string) error {
		return reply(helpers.Preset("error", types.PresetOptions{Title: title, Description: text, Thumbnail: helpers.ThumbFlag}))
	}
	done := func(title, text string) error {
		return reply(helpers.Preset("success", types.PresetOptions{Title: title, Description: text, Thumbnail: helpers.ThumbPlayers}))
	}

	// resolve finds the faction named in the "faction" option.
	resolve := func() (*database.FactionProfile, error, bool) {
		name := helpers.SubOptionString(opts, "faction")
		p, err := database.FactionNamed(guildID, name)
		if err != nil {
			return nil, err, false
		}
		if p == nil {
			return nil, reply(general.FactionNotFoundEmbed(name)), false
		}
		return p, nil, true
	}
	// target reads the "user" option: a real, registered player.
	target := func() (*discordgo.User, error, bool) {
		u := helpers.SubOptionUser(s, opts, "user")
		if u == nil || u.Bot {
			return nil, reply(general.FactionPlayerNotFoundEmbed()), false
		}
		if _, err := database.Must().Player.Get(guildID, u.ID); err != nil {
			if sdk.IsNotFound(err) {
				return nil, reply(general.FactionTargetNotRegisteredEmbed(u.ID)), false
			}
			return nil, err, false
		}
		return u, nil, true
	}
	// theirs is the faction a player is in.
	theirs := func(u *discordgo.User) (*database.FactionProfile, error, bool) {
		p, err := database.FactionFor(guildID, u.ID)
		if err != nil {
			return nil, err, false
		}
		if p == nil {
			return nil, reply(general.FactionPlayerHasNoneEmbed(u.ID)), false
		}
		return p, nil, true
	}

	result := func() error {
		switch sub {
		case "create":
			u, err, ok := target()
			if !ok {
				return err
			}
			p, err := database.FactionAdminCreate(guildID, u.ID, helpers.SubOptionString(opts, "name"), helpers.SubOptionString(opts, "tag"))
			if err != nil {
				return err
			}
			go factionmgr.RefreshMember(guildID, u.ID)
			return done("Faction Created", fmt.Sprintf("**%s** `[%s]` was created with <@%s> as its Founder.", p.Faction.Name, p.Faction.Tag, u.ID))

		case "delete":
			p, err, ok := resolve()
			if !ok {
				return err
			}
			if !helpers.SubOptionBool(opts, "confirm") {
				return fail("Confirm Delete", fmt.Sprintf("Deleting **%s** removes all %d members and erases its trophies and badges. Run it again with `confirm` set to true.", p.Faction.Name, len(p.Members)))
			}
			out, err := database.FactionDisband(guildID, p.Faction.ID, "", true)
			if err != nil {
				return err
			}
			go func() {
				factionmgr.DeleteRole(guildID, out.Faction.RoleID)
				for _, id := range out.Members {
					factionmgr.RemoveFrom(guildID, id, "")
				}
			}()
			return done("Faction Deleted", fmt.Sprintf("**%s** was deleted.", out.Faction.Name))

		case "customize":
			p, err, ok := resolve()
			if !ok {
				return err
			}
			fields, changed := map[string]any{}, []string{}
			for _, name := range []string{"name", "tag", "color", "icon", "symbol"} {
				if v := helpers.SubOptionString(opts, name); strings.TrimSpace(v) != "" {
					fields[name] = v
					changed = append(changed, name)
				}
			}
			if len(fields) == 0 {
				return reply(general.FactionNothingToChangeEmbed())
			}
			p, err = database.FactionCustomize(guildID, p.Faction.ID, "", true, fields)
			if err != nil {
				return err
			}
			go factionmgr.SyncFaction(guildID, p)
			return reply(general.FactionCustomizedEmbed(p, changed))

		case "add":
			p, err, ok := resolve()
			if !ok {
				return err
			}
			u, err, ok := target()
			if !ok {
				return err
			}
			p, err = database.FactionAdminAdd(guildID, p.Faction.ID, u.ID)
			if err != nil {
				return err
			}
			go factionmgr.SyncFaction(guildID, p)
			return done("Member Added", fmt.Sprintf("<@%s> is now in **%s**.", u.ID, p.Faction.Name))

		case "kick":
			u, err, ok := target()
			if !ok {
				return err
			}
			p, err, ok := theirs(u)
			if !ok {
				return err
			}
			if err := database.FactionAdminKick(guildID, p.Faction.ID, u.ID); err != nil {
				return err
			}
			go factionmgr.RemoveFrom(guildID, u.ID, p.Faction.RoleID)
			return done("Member Removed", fmt.Sprintf("Removed <@%s> from **%s**.", u.ID, p.Faction.Name))

		case "promote", "demote":
			u, err, ok := target()
			if !ok {
				return err
			}
			p, err, ok := theirs(u)
			if !ok {
				return err
			}
			role := "member"
			if sub == "promote" {
				role = "officer"
			}
			if err := database.FactionAdminRole(guildID, p.Faction.ID, u.ID, role); err != nil {
				return err
			}
			return done("Role Changed", fmt.Sprintf("<@%s> is now an **%s** of **%s**.", u.ID, strings.ToUpper(role[:1])+role[1:], p.Faction.Name))

		case "transfer":
			u, err, ok := target()
			if !ok {
				return err
			}
			p, err, ok := theirs(u)
			if !ok {
				return err
			}
			if err := database.FactionAdminTransfer(guildID, p.Faction.ID, u.ID); err != nil {
				return err
			}
			return done("Ownership Transferred", fmt.Sprintf("<@%s> is now the **Founder** of **%s**.", u.ID, p.Faction.Name))

		case "trophies":
			p, err, ok := resolve()
			if !ok {
				return err
			}
			board, mode, amount := helpers.SubOptionString(opts, "board"), helpers.SubOptionString(opts, "mode"), helpers.SubOptionInt(opts, "amount")
			after, err := database.FactionAdminTrophies(guildID, p.Faction.ID, board, mode, amount)
			if err != nil {
				return err
			}
			now, was := after.LifetimeTrophies, p.LifetimeTrophies
			if board == "seasonal" {
				now, was = after.SeasonTrophies, p.SeasonTrophies
			}
			go factionmgr.SyncFaction(guildID, after) // a new level can unlock the tag and the role
			return done("Trophies Updated", fmt.Sprintf("**%s** %s trophies: %s → %s (level %d).", after.Faction.Name, board, factionmgr.Trophies(was), factionmgr.Trophies(now), after.Level))
		}
		return fail("Invalid Subcommand", "That subcommand does not exist.")
	}()
	if result == nil {
		return nil
	}
	if msg := database.FactionMessage(result); msg != "" {
		return reply(general.FactionRefusedEmbed(msg))
	}
	log.Printf("[Faction] /factionadmin %s in %s: %v", sub, guildID, result)
	return reply(general.UnexpectedErrorFactionEmbed())
}
