package events

import (
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/screenshare"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(ScreenshareSweepEvent)
}

// ScreenshareSweepEvent expires the screenshare tickets nobody claimed in time.
var ScreenshareSweepEvent = &types.Event{
	Name:        "Screenshare Expiry",
	Description: "Expires screenshare tickets that no screensharer claimed within ten minutes, lifting the queue restriction they added.",
	Type:        "ready",
	Once:        false,
	Disabled:    false,
	System:      true,
	Execute:     func(s *discordgo.Session, _ *discordgo.Ready) { screenshare.StartSweeper(s) },
}
