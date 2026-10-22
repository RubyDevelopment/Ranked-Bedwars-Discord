package types

import (
	"math/rand/v2"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
)

// Map is one entry of the game config's "maps" array.
type Map struct {
	Name  string   `json:"name"`
	Teams []string `json:"teams"` // legacy flat team list: the fallback for modes without their own
	// ModeSettings holds the team colours the map plays with in each game mode, keyed by mode name
	// (a 4v4 mode may use four colours, a Solo mode eight).
	ModeSettings   map[string]MapModeSettings `json:"mode_settings,omitempty"`
	MapHeight      int                        `json:"map_height"`
	GUISlot        int                        `json:"gui_slot"`
	IsEnabled      bool                       `json:"is_enabled"`
	IsMapAvailable bool                       `json:"is_map_available"`
	StableID       string                     `json:"stable_id"`
}

// MapModeSettings is what a map is configured with for one game mode.
type MapModeSettings struct {
	Teams []string `json:"teams"` // the in-game team colours, in team order (Team A gets the first)
}

// TeamsFor is the team colours of the map in the named mode (case-insensitive): the mode's own list, else the
// legacy flat list, else nil. Blank entries are dropped.
func (m Map) TeamsFor(mode string) []string {
	clean := func(in []string) []string {
		var out []string
		for _, t := range in {
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, t)
			}
		}
		return out
	}
	want := strings.ToLower(strings.TrimSpace(mode))
	for name, ms := range m.ModeSettings {
		if strings.ToLower(strings.TrimSpace(name)) == want {
			if teams := clean(ms.Teams); len(teams) > 0 {
				return teams
			}
		}
	}
	return clean(m.Teams)
}

// Mode is one entry of the game config's "modes" array.
type Mode struct {
	Name                   string  `json:"name"`
	StableID               string  `json:"stable_id"`
	Type                   string  `json:"type"` // standard, captain, ffa
	Description            *string `json:"description,omitempty"`
	Category               *string `json:"category,omitempty"`
	TeamCount              int     `json:"team_count"`
	PlayersPerTeam         int     `json:"players_per_team"`
	MaxPlayers             int     `json:"max_players"`
	IsEnabled              bool    `json:"is_enabled"`
	IsEloGainEnabled       bool    `json:"is_elo_gain_enabled"`
	IsStatsTrackingEnabled bool    `json:"is_stats_tracking_enabled"`
	IsAutoStrikingEnabled  bool    `json:"is_auto_striking_enabled"`
	IsPartyQueueEnabled    bool    `json:"is_party_queue_enabled"`
	IsQueueCategory        bool    `json:"is_queue_category"`
	GUISlot                int     `json:"gui_slot"`
	SettingsID             *uint   `json:"settings_id,omitempty"`
	FormationID            string  `json:"formation_id,omitempty"` // the team formation it uses (guild flows.formations); empty = the default for its type

	// Party holds this mode's party overrides; nil inherits the guild's PartiesConfig.
	Party *ModePartyConfig `json:"party,omitempty"`
}

// GameChannelsConfig is where and how a queue group's game channels are made.
// Mode "channel" (the default) creates a text and team voice channels in
// CategoryID (the queue's own category when empty); mode "thread" makes the
// game chat a thread of ThreadParentID instead, with the voice channels still
// made in the queue's category (threads can't hold voice).
type GameChannelsConfig struct {
	Mode             string   `json:"mode"` // "channel" or "thread"
	CategoryID       string   `json:"category_id,omitempty"`
	ThreadParentID   string   `json:"thread_parent_id,omitempty"`
	PrivateThreads   *bool    `json:"private_threads,omitempty"` // nil = private
	AutoArchiveMins  int      `json:"auto_archive_minutes,omitempty"`
	ExtraRoleIDs     []string `json:"extra_role_ids,omitempty"`     // roles that can also see the game channels
	TextNameTemplate string   `json:"text_name_template,omitempty"` // {game}, {season_game}, {total_game}, {game_id}, {mode}; empty = "game-{game}"
}

// IsThread reports whether the game chat is a thread.
func (c GameChannelsConfig) IsThread() bool { return c.Mode == "thread" }

// Private reports whether game threads are private (the default).
func (c GameChannelsConfig) Private() bool { return c.PrivateThreads == nil || *c.PrivateThreads }

// Bot is one entry of the game config's "bots" array.
type Bot struct {
	Username           string `json:"username"`
	Status             string `json:"status"` // available, offline, maintenance
	IsEnabled          bool   `json:"is_enabled"`
	Tier               string `json:"tier"` // titan, premium, standard
	PasswordCiphertext string `json:"password_ciphertext,omitempty"`
	PasswordSetAt      string `json:"password_set_at,omitempty"`
}

// GameStatus mirrors models.GamesModel.Status.
type GameStatus string

const (
	GameStatusWaiting    GameStatus = "waiting"
	GameStatusInProgress GameStatus = "in_progress"
	GameStatusCompleted  GameStatus = "completed"
	GameStatusVoided     GameStatus = "voided"
	GameStatusCancelled  GameStatus = "cancelled"
)

type CaptainFlow struct {
	MaxPartySize          *int    `json:"max_party_size"` // overrides the party limit's max size in captain queues; nil = inherit
	IsEnabled             bool    `json:"is_enabled"`
	PickTimeoutSeconds    int     `json:"pick_timeout_seconds"`
	TopEloCaptainChance   float64 `json:"top_elo_captain_chance"`
	AutoAssignParties     bool    `json:"auto_assign_party_members"`
	OneCaptainPerParty    bool    `json:"one_captain_per_party"`
	DefaultPickingType    string  `json:"default_picking_type"`
	AlternatingMinPlayers int     `json:"alternating_min_players"`
}

// CustomGame is what makes a Game a staff-built one.
type CustomGame struct {
	HostedBy string     `json:"hosted_by"`
	Ranked   bool       `json:"ranked"`
	Stats    bool       `json:"stats"`
	UseBot   bool       `json:"use_bot"`
	Teams    [][]string `json:"teams"`           // Discord IDs, one list per team
	Voice    []string   `json:"voice,omitempty"` // an existing voice channel per team; "" = make one
}

// CustomSpec is a staff-built game to start.
type CustomSpec struct {
	GuildID        string
	HostedBy       string
	QueueChannelID string // the queue the mode is bound to: where finished players are re-swept
	CategoryID     string // where the game's channels are made
	Mode           Mode
	Ranked, Stats  bool
	UseBot         bool
	Teams          [][]QueueMember
	Voice          []string
}

// DiscordLogAuthor is who wrote a Discord log message.
type DiscordLogAuthor struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`
	Bot    bool   `json:"bot,omitempty"`
}

// DiscordLogMessage is one message of the game's Discord chat.
type DiscordLogMessage struct {
	ID          string            `json:"id"`
	At          time.Time         `json:"at"`
	Author      DiscordLogAuthor  `json:"author"`
	Content     string            `json:"content,omitempty"`
	Attachments []string          `json:"attachments,omitempty"` // URLs
	Mentions    map[string]string `json:"mentions,omitempty"`    // user id -> name
}

// DraftConfig is the captain flow's settings.
type DraftConfig struct {
	PickingType           string  // "one_per_person" (default) or "alternating"
	TopEloChance          float64 // chance captains are the top-rated players rather than random from the top pool
	OneCaptainPerParty    bool
	AlternatingMinPlayers int
	AutoAssignParties     bool // pull a picked player's party mates onto their team
}

// DraftView is the state of a running draft.
type DraftView struct {
	Teams    [][]string // Teams[i][0] is the captain
	Pool     []string
	Turn     int // team index, -1 when done
	Type     string
	Captains []string
}

type Formation struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`

	// balanced / random
	RandomnessPercent  *int  `json:"randomness_percent"`
	KeepPartyTogether  *bool `json:"keep_party_on_same_team"`
	BalancePartyGroups *bool `json:"balance_party_groups"`
	AllowSplitParties  *bool `json:"allow_splitting_parties"`

	// every kind: a cap on the party size in queues that use it
	MaxPartySize *int `json:"max_party_size"`

	// captains
	PickTimeoutSeconds    *int     `json:"pick_timeout_seconds"`
	TopEloCaptainChance   *float64 `json:"top_elo_captain_chance"`
	AutoAssignParties     *bool    `json:"auto_assign_party_members"`
	OneCaptainPerParty    *bool    `json:"one_captain_per_party"`
	DefaultPickingType    string   `json:"default_picking_type"`
	AlternatingMinPlayers *int     `json:"alternating_min_players"`
}

// GameAPI is the slice of the database SDK the manager uses. Players are
// identified by Discord ID except where a database player ID is named.
type GameAPI interface {
	CreateGame(actor, guildID string, in sdk.CreateGameRequest) (*models.GamesModel, error)
	UpdateGame(actor, guildID string, id uint, fields map[string]any) (*models.GamesModel, error)
	StartGame(actor, guildID string, id uint) (*models.GamesModel, error)
	EndGame(actor, guildID string, id uint, status string) (*models.GamesModel, error)
	CompleteGame(actor, guildID string, id uint, in sdk.CompleteGameRequest) (*models.GamesModel, error)
	ReverseGame(actor, guildID string, id uint, playerIDs []uint, reopen bool) (int, error)
	GetGame(guildID string, id uint) (*models.GamesModel, error)
	GameByNumber(guildID string, number int) (*models.GamesModel, error)
	ListGames(guildID, status string) ([]models.GamesModel, error)
	RecentGames(guildID, status string, limit int) ([]models.GamesModel, error)
	Participants(guildID string, id uint) ([]models.GamePlayersModel, error)
	AddParticipant(actor, guildID string, id uint, p models.GamePlayersModel) error
	RemoveParticipant(actor, guildID string, id uint, playerID uint) error
	UpdateParticipant(actor, guildID string, id uint, playerID uint, fields map[string]any) error
	PlayerID(guildID, userID string) (uint, error)
	UserID(guildID string, playerID uint) (string, error)
	ActiveSeason(guildID string) (uint, error)
}

// GameChatLine is one line of Minecraft chat, with its time in Unix milliseconds.
type GameChatLine struct {
	At   int64  `json:"at"`
	Text string `json:"text"`
}

// GameEvent is passed to GameEffects.Notify.
type GameEvent struct {
	Kind    GameEventKind
	Reason  string
	Stage   string   // EventHostStatus: which setup stage the bot is in
	Waiting []string // EventHostStatus: Minecraft names still missing from the party or their team

	Key        string            // EventEmbed: which embed
	Vars       map[string]string // EventEmbed: extra variables for it
	JoinedIDs  []string          // EventEmbed: Discord ids that joined the party / their team
	WaitingIDs []string          // EventEmbed: Discord ids still missing
	Match      *MatchEvent       // EventMatch
}

// GameEventKind says what a GameEvent announces.
type GameEventKind int

// GameLogRefs is what the game's runtime metadata keeps: where each log lives.
type GameLogRefs struct {
	Discord string `json:"discord,omitempty"`
	Game    string `json:"game,omitempty"`
}

// GamePlayer is one participant. Stats are filled in by whatever tracks the
// match (a bot, staff) and feed the season counters and the MVP pick.
type GamePlayer struct {
	UserID        string `json:"user_id"`
	Username      string `json:"username"`
	PlayerID      uint   `json:"player_id"`
	Team          string `json:"team"`
	Captain       bool   `json:"captain,omitempty"`
	EloBefore     int    `json:"elo_before"` // rating at launch, for balancing and display
	Kills         int    `json:"kills"`
	Deaths        int    `json:"deaths"`
	Assists       int    `json:"assists"`
	FinalKills    int    `json:"final_kills"`
	BedsDestroyed int    `json:"beds_destroyed"`
	BedsLost      int    `json:"beds_lost"`
	FirstBloods   int    `json:"first_bloods"`

	// Custom are the stats the guild's server profile counts from chat (id -> total), on top of the ones above.
	Custom map[string]int `json:"custom,omitempty"`
}

// MatchEnd is how a match finished (or is finishing).
type MatchEnd string

// MatchEvent is one thing the hosting bot saw in the match. Players are Discord ids.
type MatchEvent struct {
	Kind      string // kill, bed, eliminated, phase or stat
	Killer    string
	Victim    string
	Final     bool
	Team      string
	Destroyer string
	Phase     int
	PhaseName string
	Stat      string
	Player    string
	Amount    float64
	Total     float64
}

// RecoverSummary counts what Recover did.
type RecoverSummary struct{ Kept, Voided, Cancelled, Closed int }

// ScoreOptions are staff's optional overrides when scoring.
type ScoreOptions struct {
	MVP string // Discord ID of the MVP (must be a winner); "" = pick from recorded stats
	Map string // the map that was played, if it was never recorded
}

// StandardFlow / CaptainFlow are the guild's team-formation settings (guild
// config "flows"); missing keys keep these defaults.
type StandardFlow struct {
	MaxPartySize       *int `json:"max_party_size"` // overrides the party limit's max size in standard queues; nil = inherit
	IsEnabled          bool `json:"is_enabled"`
	RandomnessPercent  int  `json:"randomness_percent"`
	KeepPartyTogether  bool `json:"keep_party_on_same_team"`
	BalancePartyGroups bool `json:"balance_party_groups"`
	AllowSplitParties  bool `json:"allow_splitting_parties"`
}

// TeamOptions configures AssignTeams.
type TeamOptions struct {
	Teams, PerTeam int
	Randomness     int // 0..100: 0 = strictly ELO-balanced, 100 = purely random
	Ratings        map[string]int
	Parties        []PartyGroup
	Method         string // party ELO aggregation: average, highest, sum, leader
	BalanceParties bool   // place the strongest party first (else random order)
	AllowSplit     bool   // a party that fits no team may be split up
	Rand           *rand.Rand
}

// VoidOptions tune Void.
type VoidOptions struct {
	KeepChannels bool // leave the Discord channels alone (e.g. staff want to inspect the chat)
}
