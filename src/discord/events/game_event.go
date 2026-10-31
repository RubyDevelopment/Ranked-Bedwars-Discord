package events

import (
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(HostGameInteractionEvent)
	helpers.RegisterEvent(HostButtonsEvent)
}

// HostButtonsEvent runs the host buttons under a bot-hosted game's party and team embeds.
var HostButtonsEvent = &types.Event{
	Name:        "Host Buttons",
	System:      true,
	Description: "Runs Party Warp, Party Invite, Game Warp and PG Kick as the game's host bot when a player presses them.",
	Type:        "interaction_create",
	Execute: func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.GuildID == "" {
			return
		}
		switch i.Type {
		case discordgo.InteractionMessageComponent:
			helpers.HandleHostButton(s, i, i.MessageComponentData().CustomID)
		case discordgo.InteractionModalSubmit:
			helpers.HandleHostButtonModal(s, i)
		}
	},
}

// HostGameInteractionEvent drives the /hostgame setup panel: its menus, toggles and the Start and Cancel buttons.
var HostGameInteractionEvent = &types.Event{
	Name:        "Host Game Panel",
	System:      true,
	Description: "Moves staff through the /hostgame setup panel: mode, options, teams and review.",
	Type:        "interaction_create",
	Execute: func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.GuildID != "" && i.Type == discordgo.InteractionMessageComponent {
			helpers.HandleHostGameComponent(s, i, i.MessageComponentData().CustomID)
		}
	},
}
