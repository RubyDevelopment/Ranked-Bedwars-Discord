package punishments

import (
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(PunishmentExpiryEvent)
}

// PunishmentExpiryEvent starts the sweep that ends timed mutes, queue bans, voice mutes and server bans.
var PunishmentExpiryEvent = &types.Event{
	Name:        "Punishment Expiry",
	System:      true,
	Description: "Ends timed mutes, queue bans, voice mutes and server bans when they run out.",
	Type:        "ready",
	Execute:     func(s *discordgo.Session, _ *discordgo.Ready) { helpers.StartPunishmentExpiry(s) },
}
