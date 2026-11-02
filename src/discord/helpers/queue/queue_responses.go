package queue

import (
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

func queuePartyEmbed(title, description string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       title,
		Description: description,
		Thumbnail:   helpers.ThumbFlag,
	})
}
