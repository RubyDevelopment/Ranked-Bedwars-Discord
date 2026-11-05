package general

import (
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/division"
)

func init() {
	helpers.Register(PlayerPremiumCommand)
}

// PlayerPremiumCommand manages the Premium division: add, remove, spec roles and the roster (it has no vote).
var PlayerPremiumCommand = division.NewCommand(division.Premium)
