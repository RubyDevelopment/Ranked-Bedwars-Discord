package events

import (
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(GiveawayEntryEvent)
}

// GiveawayEntryEvent takes a member's entry when they press a giveaway's Enter button.
var GiveawayEntryEvent = &types.Event{
	Name:        "Giveaway Entry",
	System:      true,
	Description: "Takes a giveaway entry when a member presses its Enter button, after checking the ELO requirement.",
	Type:        "interaction_create",
	Execute: func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.Type == discordgo.InteractionMessageComponent && i.GuildID != "" {
			helpers.HandleGiveawayEntry(s, i, i.MessageComponentData().CustomID)
		}
	},
}

func init() {
	helpers.RegisterEvent(GiveawaySweepEvent)
}

// GiveawaySweepEvent posts the dashboard's giveaways and draws their winners when they end.
var GiveawaySweepEvent = &types.Event{
	Name:        "Giveaways",
	System:      true,
	Description: "Posts the dashboard's giveaways, and draws and announces the winners when they end.",
	Type:        "ready",
	Execute:     func(s *discordgo.Session, _ *discordgo.Ready) { helpers.StartGiveaways(s) },
}

func init() {
	helpers.RegisterEvent(PanelPressEvent)
}

// PanelPressEvent answers presses of the custom panels' buttons and select menus.
var PanelPressEvent = &types.Event{
	Name:        "Panel Press",
	System:      true,
	Description: "Gives and takes roles, replies, opens tickets and runs commands when a member uses a custom panel.",
	Type:        "interaction_create",
	Execute: func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.Type == discordgo.InteractionMessageComponent && i.GuildID != "" {
			helpers.HandlePanelPress(s, i, i.MessageComponentData().CustomID)
		}
	},
}

func init() {
	helpers.RegisterEvent(PanelSyncEvent)
}

// PanelSyncEvent posts the dashboard's custom panels in their channels and keeps them up to date.
var PanelSyncEvent = &types.Event{
	Name:        "Panel Sync",
	System:      true,
	Description: "Posts the dashboard's custom panels in their channels and keeps them up to date.",
	Type:        "ready",
	Execute:     func(s *discordgo.Session, _ *discordgo.Ready) { helpers.StartPanelSync(s) },
}

func init() {
	helpers.RegisterEvent(ReactionRoleAddEvent)
}

// ReactionRoleAddEvent gives the role bound to an emoji when a member reacts with it.
var ReactionRoleAddEvent = &types.Event{
	Name:        "Reaction Roles",
	System:      true,
	Description: "Gives roles when members add the reactions bound on the dashboard.",
	Type:        "reaction_add",
	Execute:     helpers.HandleReactionAdd,
}

func init() {
	helpers.RegisterEvent(ReactionRoleRemoveEvent)
}

// ReactionRoleRemoveEvent takes the role bound to an emoji when a member removes their reaction.
var ReactionRoleRemoveEvent = &types.Event{
	Name:        "Reaction Roles Removal",
	System:      true,
	Description: "Takes a reaction role away when the member removes their reaction.",
	Type:        "reaction_remove",
	Execute:     helpers.HandleReactionRemove,
}
