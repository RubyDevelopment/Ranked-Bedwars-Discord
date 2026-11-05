package general

import (
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/division"
)

func init() {
	helpers.Register(QueuePitCommand)
}

// QueuePitCommand manages the Pit division: add, remove, spec roles, the roster and the vote to add a player.
var QueuePitCommand = division.NewCommand(division.Pit)
