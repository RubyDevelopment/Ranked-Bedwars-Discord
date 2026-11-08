package general

import (
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/screenshare"
	"ranked-bedwars/src/discord/helpers/tickets"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(ModerationSsCloseCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationSsCloseCommand closes the screenshare ticket it is run in.
var ModerationSsCloseCommand = &types.Command{
	Name:        "ssclose",
	Description: "Closes the screenshare ticket you are in",
	Ephemeral:   true,
	Data:        &discordgo.ApplicationCommand{},
	Execute:     executeModerationSsCloseCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

// executeModerationSsCloseCommand closes the screenshare ticket in this channel the way the ticket's Close button does: the
// transcript goes to the logs channel, the player's role is lifted and the channel is deleted.
func executeModerationSsCloseCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ctx, err := tickets.LoadTicketContext(i.GuildID, i.ChannelID)
	if err != nil || ctx.Panel.Ticket.Command != screenshare.Command {
		return helpers.EditInteractionEmbed(s, i, ModerationSsCloseNotInTicketEmbed(i))
	}
	if !screenshare.CanHandle(s, i) {
		return helpers.EditInteractionEmbed(s, i, ModerationSsCloseNotEligibleEmbed(i))
	}
	if _, err := tickets.CloseTicket(s, i.GuildID, i.ChannelID, helpers.InteractionUserID(i), true); err != nil {
		return helpers.EditInteractionEmbed(s, i, ModerationSsCloseNotInTicketEmbed(i))
	}
	return helpers.EditInteractionEmbed(s, i, ModerationSsCloseClosedEmbed(i))
}

// ============================================================================
// Helper functions
// ============================================================================

func ModerationSsCloseClosedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Screenshare Closed",
		Description: "The screenshare ticket was closed. This channel will be deleted in a few seconds.",
		Thumbnail:   helpers.ThumbTada,
	})
}

func ModerationSsCloseNotInTicketEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not A Screenshare Ticket",
		Description: "Run this command inside a screenshare ticket channel.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ModerationSsCloseNotEligibleEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not Eligible",
		Description: "Only members with the screensharer role can close screenshare tickets.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ModerationSsCloseNotClaimedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Ticket Not Claimed",
		Description: "Only a claimed screenshare ticket can be closed. Claim it first with the button in the ticket.",
		Thumbnail:   helpers.ThumbFlag,
	})
}
