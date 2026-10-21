package punishments

import (
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(DecaySweeperEvent)
}

// DecaySweeperEvent starts the ELO decay engine: it warns players before their ELO decays and applies inactivity decay.
var DecaySweeperEvent = &types.Event{
	Name:        "Decay Sweeper",
	System:      true,
	Description: "Warns players before their ELO decays and applies inactivity decay as the server configured it.",
	Type:        "ready",
	Execute:     func(s *discordgo.Session, _ *discordgo.Ready) { helpers.StartDecaySweeper(s) },
}
