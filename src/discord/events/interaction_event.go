package events

import (
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(ComponentInteractionEvent)
}

// ComponentInteractionEvent routes the buttons and select menus that commands post to their own handlers.
var ComponentInteractionEvent = &types.Event{
	Name:        "Component Interaction",
	System:      true,
	Description: "Routes the party, game hosting, queue and draft buttons and menus to the command that posted them.",
	Type:        "interaction_create",
	Execute: func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.GuildID != "" && i.Type == discordgo.InteractionMessageComponent {
			helpers.HandleRegisteredComponent(s, i, i.MessageComponentData().CustomID)
		}
	},
}

func init() {
	helpers.RegisterEvent(PaginationEvent)
}

// PaginationEvent turns the pages of strike, punishment and game history embeds.
var PaginationEvent = &types.Event{
	Name:        "Pagination",
	System:      true,
	Description: "Turns the pages of the strike, punishment and game history lists when Back or Next is pressed.",
	Type:        "interaction_create",
	Execute: func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.GuildID != "" && i.Type == discordgo.InteractionMessageComponent {
			helpers.HandleHistoryPageClick(s, i, i.MessageComponentData().CustomID)
		}
	},
}
