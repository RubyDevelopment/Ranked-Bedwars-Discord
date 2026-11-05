package general

import (
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/division"
)

func init() {
	helpers.Register(QueuePugsCommand)
}

// QueuePugsCommand manages the PUGS division: add, remove, spec roles, the roster and the vote to add a player.
var QueuePugsCommand = division.NewCommand(division.Pugs)
