package types

import (
	"time"
)

// QueueConfig is one entry of the game config's "queues" array.
type QueueConfig struct {
	Name        string                `json:"name"`
	CategoryID  string                `json:"category_id"`
	WaitingVCID string                `json:"waiting_vc_id"`
	IsEnabled   bool                  `json:"is_enabled"`
	Settings    []QueueChannelSetting `json:"settings"`

	// VoiceTeamTemplate names a game's team voice channels ({team}, {mode});
	// empty = "Team {team} - {mode}".
	VoiceTeamTemplate    string `json:"voice_team_template,omitempty"`
	VoiceWaitingTemplate string `json:"voice_waiting_template,omitempty"`

	// GameChannels says where the channels of this queue's games are created.
	GameChannels GameChannelsConfig `json:"game_channels"`
}

// QueueChannelSetting binds one queue voice channel to a mode.
type QueueChannelSetting struct {
	Name      string `json:"name,omitempty"`
	Mode      string `json:"mode"`
	ChannelID string `json:"channel_id"`
	IsEnabled bool   `json:"is_enabled"`
	// Allowed/Denied hold rank names or division labels; Denied wins, empty Allowed = everyone.
	AllowedRanks []string `json:"allowed_ranks,omitempty"`
	DeniedRanks  []string `json:"denied_ranks,omitempty"`
	// Allowed/Denied hold Discord role ids; Denied wins, empty Allowed = everyone.
	AllowedRoles []string `json:"allowed_roles,omitempty"`
	DeniedRoles  []string `json:"denied_roles,omitempty"`
	// EloDecay is the decay for players whose last game was in this queue's mode; nil = the guild's older default.
	EloDecay *EloDecaySetting `json:"elo_decay,omitempty"`
}

// QueueBinding ties a queue voice channel to its queue and mode.
type QueueBinding struct {
	Queue   QueueConfig
	Channel QueueChannelSetting
	Mode    Mode
}

// QueueGroup is a formed lobby waiting to become a game.
type QueueGroup struct {
	ID         string
	GuildID    string
	ChannelID  string
	CategoryID string
	Mode       Mode
	Players    []QueueMember
	Parties    []PartyGroup // only parties whose members are all in Players
	CreatedAt  time.Time
}

// QueueMember is a player in a queue channel.
type QueueMember struct {
	UserID   string
	Username string
}

// QueueNoticeKind says why a party was held back from a match.
type QueueNoticeKind int

// RankGateResult is the outcome of a queue's allowed/denied rank lists.
type RankGateResult struct {
	Allowed bool
	Reason  string // "denied" | "missing_allowed" when not allowed
}
