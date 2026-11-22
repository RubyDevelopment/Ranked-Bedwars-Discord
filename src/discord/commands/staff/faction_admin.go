package staff

import (
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
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

