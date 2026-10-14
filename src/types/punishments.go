package types

import (
	"github.com/bwmarrin/discordgo"
)

// DiscordAction performs the Discord-side half of a punishment and reports
// whether it was applied.
type DiscordAction func(s *discordgo.Session, guildID, targetID, reason string) bool
