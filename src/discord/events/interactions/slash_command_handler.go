package interactions

import (
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(AutocompleteEvent)
}

// AutocompleteEvent answers autocomplete requests for command options.
var AutocompleteEvent = &types.Event{
	Name:        "Autocomplete",
	System:      true,
	Description: "Answers autocomplete requests for command options.",
	Type:        "interaction_create",
	Execute: func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.GuildID != "" && i.Type == discordgo.InteractionApplicationCommandAutocomplete {
			helpers.HandleAutocomplete(s, i)
		}
	},
}

func init() {
	helpers.RegisterEvent(RequirementFixEvent)
}

// RequirementFixEvent lets an admin assign a command's missing role or channel from the error message.
var RequirementFixEvent = &types.Event{
	Name:        "Requirement Fix",
	System:      true,
	Description: "Lets an admin pick the role or channel a command is missing straight from its error message.",
	Type:        "interaction_create",
	Execute: func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.GuildID != "" && i.Type == discordgo.InteractionMessageComponent {
			helpers.HandleRequirementFixSelect(s, i, i.MessageComponentData().CustomID)
		}
	},
}

func init() {
	helpers.RegisterEvent(SlashCommandEvent)
}

// SlashCommandEvent runs the slash command a member invokes.
var SlashCommandEvent = &types.Event{
	Name:        "Slash Command Router",
	System:      true,
	Description: "Runs slash commands through the same access rules and requirements as prefix commands.",
	Type:        "interaction_create",
	Execute: func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.GuildID != "" && i.Type == discordgo.InteractionApplicationCommand {
			helpers.HandleSlashCommand(s, i)
		}
	},
}
