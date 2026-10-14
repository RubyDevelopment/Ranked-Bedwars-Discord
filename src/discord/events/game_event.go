package events

import (
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(HostGameInteractionEvent)
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
