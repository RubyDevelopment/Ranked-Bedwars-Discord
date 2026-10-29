package events

import (
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(ChannelCreateEvent)
}

// ChannelCreateEvent adds a newly created channel or category to the guild state snapshot.
var ChannelCreateEvent = &types.Event{
	Name:        "Channel Create",
	System:      true,
	Description: "Adds new channels and categories to the server snapshot the dashboard's pickers read.",
	Type:        "channel_create",
	Once:        false,
	Disabled:    false,
	Execute:     helpers.HandleChannelCreate,
}

func init() {
	helpers.RegisterEvent(ChannelDeleteEvent)
}

// ChannelDeleteEvent removes a deleted channel or category from the snapshot.
var ChannelDeleteEvent = &types.Event{
	Name:        "Channel Delete",
	System:      true,
	Description: "Removes deleted channels and categories from the server snapshot.",
	Type:        "channel_delete",
	Once:        false,
	Disabled:    false,
	Execute:     helpers.HandleChannelDelete,
}

func init() {
	helpers.RegisterEvent(ChannelUpdateEvent)
}

// ChannelUpdateEvent keeps a renamed or moved channel or category current in the snapshot.
var ChannelUpdateEvent = &types.Event{
	Name:        "Channel Update",
	System:      true,
	Description: "Keeps renamed and moved channels and categories current in the server snapshot.",
	Type:        "channel_update",
	Once:        false,
	Disabled:    false,
	Execute:     helpers.HandleChannelUpdate,
}

func init() {
	helpers.RegisterEvent(GuildJoinEvent)
}

// GuildJoinEvent sets up a guild the bot is added to after startup.
var GuildJoinEvent = &types.Event{
	Name:        "Guild Join",
	System:      true,
	Description: "Registers a server the bot is added to and builds its config, snapshot and defaults.",
	Type:        "guild_create",
	Once:        false,
	Disabled:    false,
	Execute:     helpers.HandleGuildJoin,
}

func init() {
	helpers.RegisterEvent(GuildUpdateEvent)
}

// GuildUpdateEvent refreshes stored Discord branding when the server changes its icon or banner.
var GuildUpdateEvent = &types.Event{
	Name:        "Guild Update",
	System:      true,
	Description: "Keeps saved server branding and default bot appearance current.",
	Type:        "guild_update",
	Once:        false,
	Disabled:    false,
	Execute:     helpers.HandleGuildUpdate,
}

func init() {
	helpers.RegisterEvent(GuildSetupEvent)
}

// GuildSetupEvent checks every guild the bot is in once it connects.
var GuildSetupEvent = &types.Event{
	Name:        "Guild Setup",
	System:      true,
	Description: "On startup, creates whatever each server is missing (config, snapshot, command and event settings) and re-syncs it.",
	Type:        "ready",
	Once:        true,
	Disabled:    false,
	Execute:     helpers.HandleGuildSetup,
}

func init() {
	helpers.RegisterEvent(RoleCreateEvent)
}

// RoleCreateEvent adds a newly created role to the guild state snapshot.
var RoleCreateEvent = &types.Event{
	Name:        "Role Create",
	System:      true,
	Description: "Adds new roles to the server snapshot the dashboard's pickers read.",
	Type:        "role_create",
	Once:        false,
	Disabled:    false,
	Execute:     helpers.HandleRoleCreate,
}

func init() {
	helpers.RegisterEvent(RoleDeleteEvent)
}

// RoleDeleteEvent removes a deleted role from the snapshot.
var RoleDeleteEvent = &types.Event{
	Name:        "Role Delete",
	System:      true,
	Description: "Removes deleted roles from the server snapshot.",
	Type:        "role_delete",
	Once:        false,
	Disabled:    false,
	Execute:     helpers.HandleRoleDelete,
}

func init() {
	helpers.RegisterEvent(RoleUpdateEvent)
}

// RoleUpdateEvent keeps a renamed or recoloured role current in the snapshot.
var RoleUpdateEvent = &types.Event{
	Name:        "Role Update",
	System:      true,
	Description: "Keeps renamed and recoloured roles current in the server snapshot.",
	Type:        "role_update",
	Once:        false,
	Disabled:    false,
	Execute:     helpers.HandleRoleUpdate,
}

func init() {
	helpers.RegisterEvent(PlanReminderEvent)
}

// PlanReminderEvent tells each server's owner by DM when the plan is about to end, and once after it has.
var PlanReminderEvent = &types.Event{
	Name:        "Plan Reminders",
	System:      true,
	Description: "Messages a server's owner a few days before its plan ends, and once after it has, until it is renewed.",
	Type:        "ready",
	Execute:     helpers.HandlePlanReminders,
}

// AppearanceSyncEvent keeps the bot's per-server nickname, avatar, banner and bio in line with the dashboard.
var AppearanceSyncEvent = &types.Event{
	Name:        "Appearance Sync",
	System:      true,
	Description: "Re-applies each server's configured bot nickname, avatar, banner and bio on startup; later changes are applied the moment the dashboard saves them (POST /live/appearance).",
	Type:        "ready",
	Once:        false,
	Disabled:    false,
	Execute:     helpers.HandleAppearanceSync,
}
