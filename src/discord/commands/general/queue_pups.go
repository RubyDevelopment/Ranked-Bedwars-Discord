package general

import (
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/division"
)

func init() {
	helpers.Register(QueuePupsCommand)
}

// QueuePupsCommand manages the PUPS division: add, remove, spec roles, the roster and the vote to add a player.
var QueuePupsCommand = division.NewCommand(division.Pups)
