package types

import (
	"github.com/bwmarrin/discordgo"
)

// EventConfig is one entry of the guild config's "event_configs" map, keyed by
// event name. Like CommandConfig it is seeded for every registered event on
// guild setup (see SeedEventConfigs) and edited per-guild from the dashboard:
// an on/off switch plus the roles, channels and categories the event uses.
type EventConfig struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsEnabled   bool   `json:"is_enabled"`
	// IsSystem marks the bot's own plumbing: always on, hidden from the dashboard.
	IsSystem bool `json:"is_system,omitempty"`

	RequiredRoles      map[string]RequirementSlot `json:"required_roles"`
	RequiredChannels   map[string]RequirementSlot `json:"required_channels"`
	RequiredCategories map[string]RequirementSlot `json:"required_categories"`
}

// EventCtx is what a handler knows about one thing that happened.
type EventCtx struct {
	User      *discordgo.User   // the person involved, when there is one
	Member    *discordgo.Member // set while they are in the server
	ChannelID string            // where it happened
	Extra     map[string]string // the event's own {{variables}} (role.name, message.content, change.old…)
}
