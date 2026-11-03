package match

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers/partymgr"
	"ranked-bedwars/src/discord/helpers/queue"
	"ranked-bedwars/src/discord/helpers/ratings"
	"ranked-bedwars/src/types"
	"slices"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
)

// ============================================================================

// GameState is where a match is in its life.
type GameState string

const (
	GameWaiting  GameState = "waiting"   // created, pre-game not started
	GamePreGame  GameState = "pre_game"  // roster check, channels, teams / draft
	GameInGame   GameState = "in_game"   // being played
	GamePostGame GameState = "post_game" // over, result awaiting staff scoring
	GameCleanup  GameState = "cleanup"   // scored and done
	GameVoided   GameState = "voided"
)

func (s GameState) active() bool { return s == GameWaiting || s == GamePreGame || s == GameInGame }

// dbStatus is the games.status the state maps to (types.GameStatus values).
func (s GameState) dbStatus() string {
	switch s {
	case GameWaiting, GamePreGame:
		return string(types.GameStatusWaiting)
	case GameInGame, GamePostGame:
		return string(types.GameStatusInProgress)
	case GameCleanup:
		return string(types.GameStatusCompleted)
	default:
		return string(types.GameStatusVoided)
	}
}

// MatchEnd is defined in package types (games.go).
type MatchEnd = types.MatchEnd

const (
	EndNone         MatchEnd = ""
	EndStaffPending MatchEnd = "staff_pending" // a player submitted; staff must score it
	EndRanked       MatchEnd = "ranked"        // scored
	EndCasual       MatchEnd = "casual"        // mode awards no ELO
	EndVoided       MatchEnd = "voided"
)

// GamePlayer is defined in package types (games.go).
type GamePlayer = types.GamePlayer

// GameChannels are the Discord channels a match owns.
type GameChannels struct {
	Category string            `json:"category,omitempty"`
	Text     string            `json:"text,omitempty"`
	Voice    map[string]string `json:"voice,omitempty"`   // team name -> voice channel
	Waiting  string            `json:"waiting,omitempty"` // where players go when the voice channels are deleted

	// Borrowed are channels of the list above that already existed (a staff-built game's own voice
	// channels): the game uses them but never deletes them.
	Borrowed []string `json:"borrowed,omitempty"`
}

// ownedIDs are the channels the game created, and so deletes.
func (c GameChannels) ownedIDs() []string {
	return slices.DeleteFunc(c.ids(), func(id string) bool { return slices.Contains(c.Borrowed, id) })
}

func (c GameChannels) ids() []string {
	var out []string
	for _, id := range append([]string{c.Text}, mapValues(c.Voice)...) {
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// Game is a snapshot of a match; the manager hands out copies.
type Game struct {
	ID       uint                  `json:"id"`
	Number   int                   `json:"number"`          // within its season
	Total    int                   `json:"total,omitempty"` // among all the guild's games, across seasons (the record's guild_number); the match page's URL holds it
	GuildID  string                `json:"guild_id"`
	SeasonID uint                  `json:"season_id"`
	Mode     types.Mode            `json:"mode"`
	Map      string                `json:"map"`
	State    GameState             `json:"state"`
	End      MatchEnd              `json:"end,omitempty"`
	Players  []GamePlayer          `json:"players"`
	Teams    []string              `json:"teams,omitempty"` // team names, in order
	Parties  []partymgr.PartyGroup `json:"parties,omitempty"`
	Host     string                `json:"host,omitempty"`
	Winner   string                `json:"winner,omitempty"`
	MVP      string                `json:"mvp,omitempty"`
	Captain  bool                  `json:"captain,omitempty"` // teams come from a captain draft
	Bot      string                `json:"bot,omitempty"`
	Custom   *CustomGame           `json:"custom,omitempty"` // set for a staff-built game (/hostgame)

	Channels        GameChannels `json:"channels"`
	QueueChannelID  string       `json:"queue_channel_id"`
	QueueCategoryID string       `json:"queue_category_id"`
	StaffCategoryID string       `json:"staff_category_id,omitempty"`

	VoidReason string `json:"void_reason,omitempty"`
	VoidedBy   string `json:"voided_by,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	StateAt   time.Time `json:"state_at"`
	StartedAt time.Time `json:"started_at,omitempty"`

	Results []ratings.EloResult `json:"-"` // set once scored
}

func (g Game) clone() Game {
	g.Players = slices.Clone(g.Players)
	g.Teams = slices.Clone(g.Teams)
	g.Results = slices.Clone(g.Results)
	parties := make([]partymgr.PartyGroup, len(g.Parties))
	for i, p := range g.Parties {
		parties[i] = partymgr.PartyGroup{Leader: p.Leader, Members: slices.Clone(p.Members)}
	}
	g.Parties = parties
	if g.Custom != nil {
		c := *g.Custom
		c.Teams = make([][]string, len(g.Custom.Teams))
		for i, t := range g.Custom.Teams {
			c.Teams[i] = slices.Clone(t)
		}
		c.Voice = slices.Clone(g.Custom.Voice)
		g.Custom = &c
	}
	g.Channels.Borrowed = slices.Clone(g.Channels.Borrowed)
	voice := make(map[string]string, len(g.Channels.Voice))
	for k, v := range g.Channels.Voice {
		voice[k] = v
	}
	g.Channels.Voice = voice
	return g
}

// Player returns the participant with the given Discord ID.
// PlayerIDs lists the Discord IDs of everyone in the game, for alerts that ping the players.
func (g Game) PlayerIDs() []string {
	ids := make([]string, 0, len(g.Players))
	for _, p := range g.Players {
		if p.UserID != "" {
			ids = append(ids, p.UserID)
		}
	}
	return ids
}

func (g Game) Player(userID string) (GamePlayer, bool) {
	for _, p := range g.Players {
		if p.UserID == userID {
			return p, true
		}
	}
	return GamePlayer{}, false
}

// TeamPlayers lists a team's members.
func (g Game) TeamPlayers(team string) []GamePlayer {
	var out []GamePlayer
	for _, p := range g.Players {
		if strings.EqualFold(p.Team, team) {
			out = append(out, p)
		}
	}
	return out
}

func (g Game) userIDs() []string {
	ids := make([]string, len(g.Players))
	for i, p := range g.Players {
		ids[i] = p.UserID
	}
	return ids
}

// TeamLayout is the mode's team count and team size.
func TeamLayout(m types.Mode) (teams, perTeam int) {
	teams = max(2, m.TeamCount)
	return teams, queue.RequiredSlots(m) / teams
}

// ============================================================================
// Errors
// ============================================================================

var (
	ErrGameNotFound     = errors.New("game not found")
	ErrWrongState       = errors.New("game is not in the right state for that")
	ErrNotParticipant   = errors.New("not a player in this game")
	ErrAlreadySubmitted = errors.New("a result was already submitted")
	ErrUnknownTeam      = errors.New("unknown team")
	ErrNoSeason         = errors.New("no active season")
	ErrInvalidGroup     = errors.New("invalid lobby")
	ErrNothingToRestore = errors.New("game has no recorded result to restore")
	ErrBusy             = errors.New("game is being scored")
	ErrAlreadyVoided    = errors.New("game is already voided")
	ErrNotScored        = errors.New("game has not been scored")
)

// NotRegisteredError lists players who have no player record in the guild.
type NotRegisteredError struct{ UserIDs []string }

func (e *NotRegisteredError) Error() string {
	return "players not registered: " + strings.Join(e.UserIDs, ", ")
}

// BusyError means a player is already in a game.
type BusyError struct{ UserID string }

func (e *BusyError) Error() string { return "player " + e.UserID + " is already in a game" }

// TooEarlyError means a result was submitted before the minimum match length.
type TooEarlyError struct{ Remaining time.Duration }

func (e *TooEarlyError) Error() string {
	return fmt.Sprintf("too early to submit, wait %s", e.Remaining.Round(time.Second))
}

// ============================================================================
// Ports: database and Discord effects
// ============================================================================

// GameAPI is defined in package types (games.go).
type GameAPI = types.GameAPI

type sdkGameAPI struct{ s *sdk.Services }

func (a sdkGameAPI) ctx() context.Context { return context.Background() }

func (a sdkGameAPI) CreateGame(actor, g string, in sdk.CreateGameRequest) (*models.GamesModel, error) {
	return a.s.Client().CreateGame(a.ctx(), actor, g, in)
}

func (a sdkGameAPI) UpdateGame(actor, g string, id uint, f map[string]any) (*models.GamesModel, error) {
	return a.s.Client().UpdateGame(a.ctx(), actor, g, id, f, nil)
}

func (a sdkGameAPI) StartGame(actor, g string, id uint) (*models.GamesModel, error) {
	return a.s.Client().StartGame(a.ctx(), actor, g, id, string(types.GameStatusInProgress))
}

func (a sdkGameAPI) EndGame(actor, g string, id uint, status string) (*models.GamesModel, error) {
	return a.s.Client().EndGame(a.ctx(), actor, g, id, status)
}

func (a sdkGameAPI) CompleteGame(actor, g string, id uint, in sdk.CompleteGameRequest) (*models.GamesModel, error) {
	return a.s.Client().CompleteGame(a.ctx(), actor, g, id, in)
}

func (a sdkGameAPI) CompleteGameWithCustomStats(actor, g string, id uint, in sdk.CompleteGameRequest, customStats map[uint]map[string]float64) (*models.GamesModel, error) {
	type participantRequest struct {
		models.GamePlayersModel
		CustomStats map[string]float64 `json:"custom_stats,omitempty"`
	}
	participants := make([]participantRequest, 0, len(in.Participants))
	for _, participant := range in.Participants {
		participants = append(participants, participantRequest{
			GamePlayersModel: participant,
			CustomStats:      customStats[participant.PlayerID],
		})
	}
	request := struct {
		Status       string               `json:"status"`
		Scoring      string               `json:"scoring,omitempty"`
		Participants []participantRequest `json:"participants,omitempty"`
	}{Status: in.Status, Scoring: in.Scoring, Participants: participants}
	var game models.GamesModel
	path := fmt.Sprintf("/v1/guilds/%s/games/%d/complete", url.PathEscape(g), id)
	err := a.s.Client().API.DoWithHeaders(a.ctx(), http.MethodPost, path, request, &game, map[string]string{"X-Actor": actor})
	return &game, err
}

func (a sdkGameAPI) ReverseGame(actor, g string, id uint, players []uint, reopen bool) (int, error) {
	res, err := a.s.Client().ReverseGame(a.ctx(), actor, g, id, players, reopen)
	if err != nil {
		return 0, err
	}
	return res.Reversed, nil
}

func (a sdkGameAPI) GetGame(g string, id uint) (*models.GamesModel, error) {
	return a.s.Client().GetGame(a.ctx(), g, id)
}

func (a sdkGameAPI) GameByNumber(g string, n int) (*models.GamesModel, error) {
	return a.s.Client().GetGameByNumber(a.ctx(), g, n, nil)
}

func (a sdkGameAPI) ListGames(g, status string) ([]models.GamesModel, error) {
	var all []models.GamesModel
	for off := 0; ; off += 100 {
		page, err := a.s.Client().ListGames(a.ctx(), g, sdk.Q("status", status, "limit", 100, "offset", off))
		if err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if len(page.Data) < 100 {
			return all, nil
		}
	}
}

func (a sdkGameAPI) ListForceVoidGames(guildID string) ([]models.GamesModel, error) {
	season, err := a.s.Season.Active(guildID)
	if err != nil {
		return nil, fmt.Errorf("get active season: %w", err)
	}

	var all []models.GamesModel
	for offset := 0; ; offset += 100 {
		page, err := a.s.Client().ListGames(a.ctx(), guildID, sdk.Q(
			"status", "waiting,in_progress,completed",
			"season_id", season.ID,
			"limit", 100,
			"offset", offset,
		))
		if err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if len(page.Data) < 100 {
			return all, nil
		}
	}
}

func (a sdkGameAPI) RecentGames(g, status string, limit int) ([]models.GamesModel, error) {
	page, err := a.s.Client().ListGames(a.ctx(), g, sdk.Q("status", status, "limit", limit))
	if err != nil {
		return nil, err
	}
	return page.Data, nil
}

func (a sdkGameAPI) Participants(g string, id uint) ([]models.GamePlayersModel, error) {
	var all []models.GamePlayersModel
	for off := 0; ; off += 100 {
		page, err := a.s.Client().GameParticipants(a.ctx(), g, id, sdk.Q("limit", 100, "offset", off))
		if err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if len(page.Data) < 100 {
			return all, nil
		}
	}
}

func (a sdkGameAPI) AddParticipant(actor, g string, id uint, p models.GamePlayersModel) error {
	_, err := a.s.Client().AddParticipant(a.ctx(), actor, g, id, p)
	return err
}

func (a sdkGameAPI) RemoveParticipant(actor, g string, id, playerID uint) error {
	return a.s.Client().RemoveParticipant(a.ctx(), actor, g, id, sdk.UintToString(playerID))
}

func (a sdkGameAPI) UpdateParticipant(actor, g string, id, playerID uint, f map[string]any) error {
	_, err := a.s.Client().UpdateParticipant(a.ctx(), actor, g, id, sdk.UintToString(playerID), f)
	return err
}

func (a sdkGameAPI) PlayerID(g, u string) (uint, error) {
	v, err := a.s.Player.Get(g, u)
	if err != nil {
		return 0, err
	}
	return v.Player.ID, nil
}

func (a sdkGameAPI) UserID(g string, playerID uint) (string, error) {
	v, err := a.s.Player.Get(g, sdk.UintToString(playerID))
	if err != nil {
		return "", err
	}
	return v.Player.SnowflakeID, nil
}

func (a sdkGameAPI) ActiveSeason(g string) (uint, error) {
	s, err := a.s.Season.Active(g)
	if err != nil {
		return 0, err
	}
	return s.ID, nil
}

// GameEventKind is defined in package types (games.go).
type GameEventKind = types.GameEventKind

const (
	EventDraftNeeded  GameEventKind = iota // captains were picked; the draft should start
	EventTeams                             // teams are final
	EventStarted                           // the match is on (post the submit instructions)
	EventStaffPending                      // a result was submitted for staff to score
	EventCasualDone                        // a casual match finished
	EventScored                            // ranked results are in (Results is set)
	EventVoided                            // the match was voided (Reason says why)
	EventHostStatus                        // the hosting bot made progress (Reason is the detail, Waiting who is missing)
	EventTrackingLost                      // the bot lost the match: staff must finish it by hand (Reason says why)
	EventEmbed                             // the hosting bot asks for one of the configured embeds (Key, Vars and the id lists say which)
	EventMatch                             // something happened in the match (Match says what)
	EventMatchEnded                        // the bot saw the match end, before it is scored
	EventChatVote                          // a player voted for a map in the Minecraft party chat (Key is their Discord id, Vars["number"] the map number)
)

// MatchEvent is defined in package types (games.go).
type MatchEvent = types.MatchEvent

// GameEvent is defined in package types (games.go).
type GameEvent = types.GameEvent

// GameEffects are the Discord side effects of a match. Every field is
// optional; nil does nothing (and lets tests run without Discord).
type GameEffects struct {
	// CreateChannels makes the game's category/text/voice channels.
	CreateChannels func(g Game) (GameChannels, error)
	// ApplyTeams sets team voice permissions and moves players into them.
	ApplyTeams func(g Game) error
	// DeleteChannels moves anyone left in the game's voice channels back to the
	// waiting VC and deletes the channels. Called after a delay.
	DeleteChannels func(guildID string, ch GameChannels)
	// Notify posts the event's embed to the game chat / logs.
	Notify func(g Game, ev GameEvent)
	// StaffScoring moves the game chat to the staff scoring category and returns it.
	StaffScoring func(g Game) (categoryID string, err error)
	// PresentInQueue reports who is still in the queue voice channel; nil = everyone.
	PresentInQueue func(g Game) map[string]bool
	// Substitutes returns up to need eligible players waiting in the queue, in line order.
	Substitutes func(g Game, need int) []queue.QueueMember
	// WaitingPlayers lists the members sitting in the game's queue channel who passed its join checks and are not in a game.
	WaitingPlayers func(g Game) []string
	// HasSpoken reports whether the member wrote anything in the game's chat. When that cannot be told it says true.
	HasSpoken func(g Game, userID string) bool
	// SwapMembers makes a substitute's channel access the leaver's, moves the leaver to the waiting voice channel and the
	// substitute into the leaver's team voice channel.
	SwapMembers func(g Game, out, in GamePlayer) error
	// ChannelsExist reports whether the game's Discord channels still exist (recovery).
	ChannelsExist func(g Game) bool
	// Released is called when a game ends so the queue can be re-swept.
	Released func(guildID, queueChannelID string)
	// SelectMap votes from configured maps, optionally limited to maps read from the mode's live server menu.
	SelectMap func(g Game, available []MapOption, mapMenuRead bool) (*MapSelection, error)
	// CollectChat reads the game's Discord chat (oldest first) so it can be archived before the channels go.
	CollectChat func(ch GameChannels) []DiscordLogMessage
	// Finished is called once a game reached an end (scored, finished or voided) and was released.
	Finished func(g Game)
	// Host hands the match to the Minecraft side (the engine) once teams are final. The engine asks selectMap to vote after it
	// opens the selected mode and reads that mode's map menu. handled=true means the engine now runs the match; false keeps hosting
	// manual. A non-nil error voids the game, its text is the reason.
	Host func(g Game, selectMap func([]MapOption, bool) (*MapSelection, error)) (handled bool, err error)
}

// StandardFlow is defined in package types (games.go).
type StandardFlow = types.StandardFlow

// CaptainFlow is defined in package types (games.go).
type CaptainFlow = types.CaptainFlow

type FlowSettings struct {
	Standard StandardFlow `json:"standard"`
	Captain  CaptainFlow  `json:"captain"`
	// Formations are the named ways of forming teams a mode can pick (see team_formation.go).
	Formations []Formation `json:"formations"`
}

// DefaultFlows are the settings of a guild that configured nothing.
func DefaultFlows() FlowSettings {
	return FlowSettings{
		Standard: StandardFlow{IsEnabled: true, RandomnessPercent: 100, KeepPartyTogether: true, BalancePartyGroups: true},
		Captain: CaptainFlow{IsEnabled: true, PickTimeoutSeconds: 60, TopEloCaptainChance: 0.75, AutoAssignParties: true,
			OneCaptainPerParty: true, DefaultPickingType: "alternating", AlternatingMinPlayers: 8},
	}
}

func loadFlows(guildID string) (FlowSettings, error) {
	f := DefaultFlows()
	raw, err := database.Must().GuildConfig.Field(guildID, "flows")
	if err != nil {
		if sdk.IsNotFound(err) {
			return f, nil
		}
		return f, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &f); err != nil {
			return DefaultFlows(), err
		}
	}
	return f, nil
}

// GameDeps are the manager's outside dependencies; nil = the database-backed default.
type GameDeps struct {
	API          GameAPI
	Elo          *ratings.EloManager
	Parties      interface{ RecordGame(guildID, userID string) }
	ReleaseGroup func(guildID, groupID string) // drops the pending queue lobby
	Flows        func(guildID string) FlowSettings
	RankIssues   func(guildID string) []string // problems with the rank ladder; ranked games need none
	Now          func() time.Time
	After        func(d time.Duration, f func()) (cancel func())
	Rand         *rand.Rand // nil = unseeded; tests pin it

	SubmitMin    time.Duration // shortest match a result may be submitted for
	PreGameLimit time.Duration // longest a game may sit in setup before it is voided
	MaxInGame    time.Duration // 0 = never auto-void a running game
	CleanupDelay time.Duration // wait before deleting a finished game's channels
}

// ============================================================================
// Manager

// ============================================================================
// Engine link
// ============================================================================

type MapOption struct {
	Name    string `json:"name"`
	GUISlot int    `json:"gui_slot"`
	Item    string `json:"item,omitempty"`
}

type MapSelection struct {
	Name      string         `json:"name"`
	GUISlot   int            `json:"gui_slot"`
	MapHeight int            `json:"map_height,omitempty"`
	Teams     []string       `json:"teams,omitempty"`
	Fallbacks []MapSelection `json:"fallbacks,omitempty"`
}

type ControlResult struct {
	OK    bool            `json:"ok"`
	Bot   string          `json:"bot,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// HostActionResult is the engine's answer to a host button.
type HostActionResult struct {
	OK       bool
	Command  string
	Error    string
	Cooldown int
}

// EngineLink is the engine connection a game is hosted through. The engine package sets HostEngine when it loads, so
// the game code does not depend on it.
type EngineLink interface {
	Connected() bool
	Cancel(gameID uint, reason string)
	Write(v any) error
	Host(g Game, selectMap func([]MapOption, bool) (*MapSelection, error)) (bool, error)
	HostAction(ctx context.Context, gameID uint, action, mcName, target string) (HostActionResult, error)
	Control(guild, bot, action string, params json.RawMessage) ControlResult
	RefreshNames(guildID, userID string)
}

// HostEngine is the engine connection; see EngineLink.
var HostEngine EngineLink
