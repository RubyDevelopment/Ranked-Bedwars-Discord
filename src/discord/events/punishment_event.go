package events

import (
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/punish"
	"ranked-bedwars/src/discord/helpers/ratings"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(DecaySweeperEvent)
}

// DecaySweeperEvent starts the ELO decay engine: it warns players before their ELO decays and applies inactivity decay.
var DecaySweeperEvent = &types.Event{
	Name:         "Decay Sweeper",
	Requirements: types.DecayChannelRequirements,
	System:       true,
	Description:  "Warns players before their ELO decays and applies inactivity decay as the server configured it.",
	Type:         "ready",
	Execute:      func(s *discordgo.Session, _ *discordgo.Ready) { ratings.StartDecaySweeper(s) },
}

func init() {
	helpers.RegisterEvent(PunishmentExpiryEvent)
}

// PunishmentExpiryEvent starts the sweep that ends timed mutes, queue bans, voice mutes and server bans.
var PunishmentExpiryEvent = &types.Event{
	Name:         punish.PunishmentExpiryEventName,
	Requirements: types.PunishmentEventRequirements(),
	System:       true,
	Description:  "Ends timed mutes, queue bans, voice mutes and server bans when they run out.",
	Type:         "ready",
	Execute:      func(s *discordgo.Session, _ *discordgo.Ready) { punish.StartPunishmentExpiry(s) },
}

func init() {
	helpers.RegisterEvent(PunishmentRejoinEvent)
	helpers.RegisterEvent(PunishmentVoiceEvent)
}

// PunishmentRejoinEvent hands a returning member the roles of the punishments still in force on them.
var PunishmentRejoinEvent = &types.Event{
	Name:        "Punishment Rejoin",
	System:      true,
	Description: "Gives a member who rejoins the server the muted and queue banned roles of their active punishments.",
	Type:        "guild_member_add",
	Execute: func(s *discordgo.Session, m *discordgo.GuildMemberAdd) {
		punish.ReapplyPunishments(s, m.GuildID, m.User.ID)
	},
}

// PunishmentVoiceEvent server-mutes a voice-muted member the moment they join a voice channel.
var PunishmentVoiceEvent = &types.Event{
	Name:        "Punishment Voice Mute",
	System:      true,
	Description: "Server-mutes members with an active voice mute when they join a voice channel.",
	Type:        "voice_state_update",
	Execute: func(s *discordgo.Session, v *discordgo.VoiceStateUpdate) {
		if v.ChannelID == "" || (v.BeforeUpdate != nil && v.BeforeUpdate.ChannelID != "") || v.Mute {
			return // left voice, only moved between channels, or already muted
		}
		punish.ReapplyVoiceMute(s, v.GuildID, v.UserID)
	},
}
