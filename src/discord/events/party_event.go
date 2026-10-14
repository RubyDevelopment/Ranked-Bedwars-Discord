package events

import (
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(PartyInviteButtonEvent)
}

// PartyInviteButtonEvent answers the Accept and Deny buttons on a party invitation.
var PartyInviteButtonEvent = &types.Event{
	Name:        "Party Invitation Button",
	System:      true,
	Description: "Joins or declines a party when the invited member presses Accept or Deny.",
	Type:        "interaction_create",
	Execute: func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.GuildID != "" && i.Type == discordgo.InteractionMessageComponent {
			helpers.HandlePartyButton(s, i, i.MessageComponentData().CustomID)
		}
	},
}

func init() {
	helpers.RegisterEvent(PartySweeperEvent)
}

// PartySweeperEvent starts the sweep that disbands idle parties and expires invites.
var PartySweeperEvent = &types.Event{
	Name:        "Party Manager Startup",
	System:      true,
	Description: "Disbands idle parties and expires party invitations in every server.",
	Type:        "ready",
	Execute:     func(s *discordgo.Session, _ *discordgo.Ready) { helpers.StartPartySweeper(s) },
}
