package helpers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
	appLog "ranked-bedwars/src/utility"
)

// maxAutocompleteChoices is the most choices Discord accepts in one autocomplete answer.
const maxAutocompleteChoices = 25

// GameNumberAutocomplete answers an autocomplete for a game-number option with the guild's
// games, limited to the given states (every state when none are given).
func GameNumberAutocomplete(states ...GameState) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, maxAutocompleteChoices)
		for _, g := range Games.GuildGames(i.GuildID) {
			if len(states) > 0 && !slices.Contains(states, g.State) {
				continue
			}
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  fmt.Sprintf("#%d | %s | %s", g.Number, g.Mode.Name, g.State),
				Value: g.Number,
			})
			if len(choices) == maxAutocompleteChoices {
				break
			}
		}

		_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionApplicationCommandAutocompleteResult,
			Data: &discordgo.InteractionResponseData{Choices: choices},
		})
	}
}

// ForceVoidGameNumberAutocomplete includes persisted open games and completed
// games from the active season, along with the current in-memory game snapshots.
func ForceVoidGameNumberAutocomplete() func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		games, err := Games.ForceVoidCandidates(i.GuildID)
		if err != nil {
			appLog.EngineLogger.Error("[forcevoid] autocomplete: " + err.Error())
			respondGameNumberAutocomplete(s, i, nil)
			return
		}

		sort.Slice(games, func(i, j int) bool {
			if games[i].Number == games[j].Number {
				return games[i].ID > games[j].ID
			}
			return games[i].Number > games[j].Number
		})

		choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, maxAutocompleteChoices)
		for _, game := range games {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  fmt.Sprintf("#%d | %s | %s", game.Number, game.Mode.Name, game.State),
				Value: game.Number,
			})
			if len(choices) == maxAutocompleteChoices {
				break
			}
		}
		respondGameNumberAutocomplete(s, i, choices)
	}
}

func respondGameNumberAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate, choices []*discordgo.ApplicationCommandOptionChoice) {
	_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
}

// ============================================================================
// Game channels
// ============================================================================

// The Discord side of a game's channels, driven by the queue group's
// "game_channels" config (see types.GameChannelsConfig): either a text channel
// plus team voice channels in a category, or a thread of a chosen channel.
// Players and the configured extra roles are the only ones who can see them.

const (
	gameChannelAllow = discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory |
		discordgo.PermissionVoiceConnect | discordgo.PermissionVoiceSpeak
	defaultTeamVoiceTemplate = "Team {team} - {mode}"
	defaultTextTemplate      = "game-{game}"
	defaultThreadArchive     = 1440
)

// NewGameChannelEffects returns the CreateChannels and DeleteChannels effects
// for GameManager.SetEffects. session returns the live Discord session.
func NewGameChannelEffects(session func() *discordgo.Session) (create func(Game) (GameChannels, error), remove func(guildID string, ch GameChannels)) {
	create = func(g Game) (GameChannels, error) {
		s := session()
		if s == nil {
			return GameChannels{}, fmt.Errorf("discord session not ready")
		}
		return createGameChannels(s, g)
	}
	remove = func(guildID string, ch GameChannels) {
		if s := session(); s != nil {
			deleteGameChannels(s, ch)
		}
	}
	return create, remove
}

func createGameChannels(s *discordgo.Session, g Game) (GameChannels, error) {
	var queue types.QueueConfig
	if snap, err := Queues.Snapshot(g.GuildID); err == nil && snap != nil {
		if b, ok := snap.Bindings[g.QueueChannelID]; ok {
			queue = b.Queue
		}
	}
	cfg := queue.GameChannels
	if strings.Contains(cfg.TextNameTemplate+queue.VoiceTeamTemplate, "{total_game}") {
		g.Total = guildGameTotal(g.GuildID)
	}

	// Voice channels (and the text channel in "channel" mode) live in this category.
	category := firstNonEmpty(cfg.CategoryID, queue.CategoryID, g.QueueCategoryID)
	if cfg.IsThread() {
		category = firstNonEmpty(queue.CategoryID, g.QueueCategoryID)
	}

	out := GameChannels{Voice: map[string]string{}, Waiting: strings.TrimSpace(queue.WaitingVCID)}
	overwrites := gameOverwrites(s, g, cfg)
	cleanup := func() { deleteGameChannels(s, out) }

	// Text: a thread of the chosen channel, or a text channel in the category.
	textName := expandGameName(firstNonEmpty(cfg.TextNameTemplate, defaultTextTemplate), g, "")
	if cfg.IsThread() && strings.TrimSpace(cfg.ThreadParentID) != "" {
		thread, err := startGameThread(s, g, cfg, textName)
		if err != nil {
			return GameChannels{}, fmt.Errorf("create game thread: %w", err)
		}
		out.Text = thread.ID
	} else {
		text, err := s.GuildChannelCreateComplex(g.GuildID, discordgo.GuildChannelCreateData{
			Name: slugify(textName), Type: discordgo.ChannelTypeGuildText, ParentID: category, PermissionOverwrites: overwrites,
		})
		if err != nil {
			return GameChannels{}, fmt.Errorf("create game text channel: %w", err)
		}
		out.Text = text.ID
	}

	// One voice channel per team.
	teams, _ := TeamLayout(g.Mode)
	if g.Custom != nil {
		teams = len(g.Custom.Teams)
	}
	template := firstNonEmpty(queue.VoiceTeamTemplate, defaultTeamVoiceTemplate)
	for t := 0; t < teams; t++ {
		name := TeamName(t)
		if g.Custom != nil && t < len(g.Custom.Voice) && g.Custom.Voice[t] != "" {
			out.Voice[name] = g.Custom.Voice[t] // staff picked a voice channel for this team: use it as is
			out.Borrowed = append(out.Borrowed, g.Custom.Voice[t])
			continue
		}
		vc, err := s.GuildChannelCreateComplex(g.GuildID, discordgo.GuildChannelCreateData{
			Name: expandGameName(template, g, name), Type: discordgo.ChannelTypeGuildVoice, ParentID: category, PermissionOverwrites: overwrites,
		})
		if err != nil {
			cleanup()
			return GameChannels{}, fmt.Errorf("create %s voice channel: %w", name, err)
		}
		out.Voice[name] = vc.ID
	}
	return out, nil
}

// startGameThread makes the game chat thread and brings the players (and, in a
// private thread, the extra roles, by mentioning them) into it.
func startGameThread(s *discordgo.Session, g Game, cfg types.GameChannelsConfig, name string) (*discordgo.Channel, error) {
	kind := discordgo.ChannelTypeGuildPrivateThread
	if !cfg.Private() {
		kind = discordgo.ChannelTypeGuildPublicThread
	}
	thread, err := s.ThreadStartComplex(cfg.ThreadParentID, &discordgo.ThreadStart{
		Name: truncateName(name, 100), Type: kind, AutoArchiveDuration: firstPositive(cfg.AutoArchiveMins, defaultThreadArchive),
	})
	if err != nil {
		return nil, err
	}
	for _, p := range g.Players {
		_ = s.ThreadMemberAdd(thread.ID, p.UserID) // a member who can't be added still sees the voice channels
	}
	if roles := cleanIDs(cfg.ExtraRoleIDs); len(roles) > 0 && cfg.Private() {
		var mentions []string
		for _, id := range roles {
			mentions = append(mentions, "<@&"+id+">")
		}
		_, _ = s.ChannelMessageSendComplex(thread.ID, &discordgo.MessageSend{
			Content:         strings.Join(mentions, " "),
			AllowedMentions: &discordgo.MessageAllowedMentions{Roles: roles},
		})
	}
	return thread, nil
}

// gameOverwrites hides the channels from everyone but the bot, the players and the extra roles.
func gameOverwrites(s *discordgo.Session, g Game, cfg types.GameChannelsConfig) []*discordgo.PermissionOverwrite {
	out := []*discordgo.PermissionOverwrite{
		{ID: g.GuildID, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel},
	}
	if s.State != nil && s.State.User != nil {
		out = append(out, &discordgo.PermissionOverwrite{ID: s.State.User.ID, Type: discordgo.PermissionOverwriteTypeMember, Allow: gameChannelAllow | discordgo.PermissionManageChannels})
	}
	for _, p := range g.Players {
		out = append(out, &discordgo.PermissionOverwrite{ID: p.UserID, Type: discordgo.PermissionOverwriteTypeMember, Allow: gameChannelAllow})
	}
	for _, id := range cleanIDs(cfg.ExtraRoleIDs) {
		out = append(out, &discordgo.PermissionOverwrite{ID: id, Type: discordgo.PermissionOverwriteTypeRole, Allow: gameChannelAllow})
	}
	return out
}

func deleteGameChannels(s *discordgo.Session, ch GameChannels) {
	for _, id := range ch.ownedIDs() {
		_, _ = s.ChannelDelete(id) // also deletes threads; already gone is fine
	}
}

// ============================================================================
// Helpers
// ============================================================================

// expandGameName fills a channel name template: {game} and {season_game} (the game's number in its season),
// {total_game} (its number among every game the guild has played), {game_id} (the game's unique id), {mode} and {team}.
func expandGameName(template string, g Game, team string) string {
	return truncateName(strings.NewReplacer(
		"{game}", strconv.Itoa(g.Number), "{season_game}", strconv.Itoa(g.Number), "{total_game}", strconv.Itoa(g.Total),
		"{game_id}", strconv.FormatUint(uint64(g.ID), 10), "{mode}", g.Mode.Name, "{team}", team,
	).Replace(template), 100)
}

// guildGameTotal counts every game the guild has recorded, across seasons (the new game included). It pages through
// the guild's games, so it is only asked when a name template uses {total_game}.
func guildGameTotal(guildID string) int {
	svc := database.Get()
	if svc == nil {
		return 0
	}
	total := 0
	for off := 0; ; off += 100 {
		page, err := svc.Client().ListGames(context.Background(), guildID, sdk.Q("limit", 100, "offset", off))
		if err != nil {
			return total
		}
		total += len(page.Data)
		if len(page.Data) < 100 {
			return total
		}
	}
}

// slugify makes a Discord text channel name: lowercase, spaces as dashes.
func slugify(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "-")
}

func truncateName(name string, n int) string {
	if r := []rune(name); len(r) > n {
		return string(r[:n])
	}
	return name
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func cleanIDs(in []string) []string {
	var out []string
	for _, id := range in {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// ============================================================================
// Custom games
// ============================================================================

// A custom game is built by staff (/hostgame) instead of formed from a queue:
// they pick the mode, the teams and, optionally, the voice channel each team
// plays in, and whether the match counts toward ELO and season stats. Nothing is
// drafted or randomised, and it never touches a queue.

// CustomGame is defined in package types (games.go).
type CustomGame = types.CustomGame

// CustomSpec is defined in package types (games.go).
type CustomSpec = types.CustomSpec

var (
	ErrNotCustom = errors.New("that game was not built by staff")
	ErrBotHosted = errors.New("a bot is hosting that game, its roster can't be changed")
)

// customMode is the queue mode with the team layout and scoring staff chose.
func customMode(mode types.Mode, teams [][]QueueMember, ranked, stats bool) types.Mode {
	total, largest := 0, 1
	for _, t := range teams {
		total += len(t)
		largest = max(largest, len(t))
	}
	mode.TeamCount = max(2, len(teams))
	mode.PlayersPerTeam = largest
	mode.MaxPlayers = total
	mode.IsEloGainEnabled = ranked
	mode.IsStatsTrackingEnabled = stats
	return mode
}

// StartCustom creates a staff-built game and starts its pre-game in the background.
func (m *GameManager) StartCustom(spec CustomSpec) (Game, error) {
	if len(spec.Teams) < 2 {
		return Game{}, fmt.Errorf("%w: a game needs at least two teams", ErrBadRoster)
	}
	var players []QueueMember
	ids := make([][]string, len(spec.Teams))
	for i, t := range spec.Teams {
		if len(t) == 0 {
			return Game{}, fmt.Errorf("%w: team %s has no players", ErrBadRoster, TeamName(i))
		}
		for _, p := range t {
			players = append(players, p)
			ids[i] = append(ids[i], p.UserID)
		}
	}

	group := &QueueGroup{
		ID: newID(), GuildID: spec.GuildID, ChannelID: spec.QueueChannelID, CategoryID: spec.CategoryID,
		Mode: customMode(spec.Mode, spec.Teams, spec.Ranked, spec.Stats), Players: players,
	}
	custom := &CustomGame{
		HostedBy: spec.HostedBy, Ranked: spec.Ranked, Stats: spec.Stats, UseBot: spec.UseBot,
		Teams: ids, Voice: slices.Clone(spec.Voice),
	}
	g, err := m.start(group, custom)
	if err != nil {
		return Game{}, err
	}
	go m.RunPreGame(g.ID)
	return g, nil
}

// assignCustomTeams is the custom game's team step: the teams are the ones staff built.
func (m *GameManager) assignCustomTeams(lg *liveGame) error {
	g := m.snapshot(lg)
	return m.setTeams(lg, g.Custom.Teams, nil)
}

// CustomGames lists the live staff-built games of a guild.
func (m *GameManager) CustomGames(guildID string) []Game {
	var out []Game
	for _, g := range m.GuildGames(guildID) {
		if g.Custom != nil {
			out = append(out, g)
		}
	}
	return out
}

// customLive finds a live custom game of the guild, locked for editing its roster or options.
func (m *GameManager) customLive(guildID string, id uint) (*liveGame, error) {
	lg := m.liveIn(guildID, id)
	if lg == nil {
		return nil, ErrGameNotFound
	}
	if lg.g.Custom == nil {
		return nil, ErrNotCustom
	}
	return lg, nil
}

// SetCustomOptions changes what a running custom game counts toward (nil = unchanged).
func (m *GameManager) SetCustomOptions(guildID string, id uint, ranked, stats *bool) error {
	lg, err := m.customLive(guildID, id)
	if err != nil {
		return err
	}
	lg.mu.Lock()
	if !lg.g.State.active() && lg.g.State != GamePostGame {
		lg.mu.Unlock()
		return ErrWrongState
	}
	if ranked != nil {
		lg.g.Custom.Ranked, lg.g.Mode.IsEloGainEnabled = *ranked, *ranked
	}
	if stats != nil {
		lg.g.Custom.Stats, lg.g.Mode.IsStatsTrackingEnabled = *stats, *stats
	}
	lg.mu.Unlock()
	m.sync(lg)
	return nil
}

// MovePlayer moves a player to another team (team is an index). Not for bot-hosted games.
func (m *GameManager) MovePlayer(guildID string, id uint, userID string, team int) error {
	lg, err := m.customLive(guildID, id)
	if err != nil {
		return err
	}
	lg.mu.Lock()
	if lg.g.Bot != "" {
		lg.mu.Unlock()
		return ErrBotHosted
	}
	if team < 0 || team >= len(lg.g.Custom.Teams) {
		lg.mu.Unlock()
		return ErrUnknownTeam
	}
	idx := slices.IndexFunc(lg.g.Players, func(p GamePlayer) bool { return p.UserID == userID })
	if idx < 0 {
		lg.mu.Unlock()
		return ErrNotParticipant
	}
	for i := range lg.g.Custom.Teams {
		lg.g.Custom.Teams[i] = slices.DeleteFunc(lg.g.Custom.Teams[i], func(u string) bool { return u == userID })
	}
	lg.g.Custom.Teams[team] = append(lg.g.Custom.Teams[team], userID)
	name := TeamName(team)
	started := len(lg.g.Teams) > 0 // teams are final: the participant rows carry them
	if started {
		lg.g.Players[idx].Team = name
	}
	player := lg.g.Players[idx]
	lg.mu.Unlock()

	if started {
		if err := m.api().UpdateParticipant(actorSystem, guildID, id, player.PlayerID, map[string]any{"team": name}); err != nil {
			return err
		}
	}
	m.sync(lg)
	return nil
}

// ReplacePlayer swaps a player for another on the same team. Not for bot-hosted games.
func (m *GameManager) ReplacePlayer(guildID string, id uint, out, in, inName string) error {
	lg, err := m.customLive(guildID, id)
	if err != nil {
		return err
	}
	if m.InGame(guildID, in) {
		return &BusyError{in}
	}
	newID, err := m.api().PlayerID(guildID, in)
	if err != nil {
		if isNotFound(err) {
			return &NotRegisteredError{[]string{in}}
		}
		return err
	}

	lg.mu.Lock()
	if lg.g.Bot != "" {
		lg.mu.Unlock()
		return ErrBotHosted
	}
	idx := slices.IndexFunc(lg.g.Players, func(p GamePlayer) bool { return p.UserID == out })
	if idx < 0 {
		lg.mu.Unlock()
		return ErrNotParticipant
	}
	old := lg.g.Players[idx]
	rating := m.deps.Elo.Ratings(guildID, []string{in})[in]
	lg.g.Players[idx] = GamePlayer{UserID: in, Username: inName, PlayerID: newID, Team: old.Team, Captain: old.Captain, EloBefore: rating}
	for i := range lg.g.Custom.Teams {
		for j, u := range lg.g.Custom.Teams[i] {
			if u == out {
				lg.g.Custom.Teams[i][j] = in
			}
		}
	}
	if lg.g.Custom.HostedBy == out {
		lg.g.Custom.HostedBy = in
	}
	if lg.g.Host == out {
		lg.g.Host = in
	}
	m.mu.Lock()
	delete(m.byPlayer, playerKey(guildID, out))
	m.mu.Unlock()
	m.indexLocked(lg)
	teamName, started := old.Team, len(lg.g.Teams) > 0
	lg.mu.Unlock()

	if started {
		if err := m.api().RemoveParticipant(actorSystem, guildID, id, old.PlayerID); err != nil {
			return err
		}
		row := participantRow(Game{}, GamePlayer{PlayerID: newID, Team: teamName})
		if err := m.api().AddParticipant(actorSystem, guildID, id, row); err != nil {
			return err
		}
	}
	m.sync(lg)
	return nil
}

// SetTeamVoice points a team at an existing voice channel (team is an index).
func (m *GameManager) SetTeamVoice(guildID string, id uint, team int, channelID string) error {
	lg, err := m.customLive(guildID, id)
	if err != nil {
		return err
	}
	lg.mu.Lock()
	if team < 0 || team >= len(lg.g.Custom.Teams) {
		lg.mu.Unlock()
		return ErrUnknownTeam
	}
	for len(lg.g.Custom.Voice) < len(lg.g.Custom.Teams) {
		lg.g.Custom.Voice = append(lg.g.Custom.Voice, "")
	}
	lg.g.Custom.Voice[team] = channelID
	name := TeamName(team)
	if lg.g.Channels.Voice == nil {
		lg.g.Channels.Voice = map[string]string{}
	}
	lg.g.Channels.Voice[name] = channelID
	if !slices.Contains(lg.g.Channels.Borrowed, channelID) {
		lg.g.Channels.Borrowed = append(lg.g.Channels.Borrowed, channelID)
	}
	m.indexLocked(lg)
	lg.mu.Unlock()
	m.sync(lg)
	return nil
}

// ============================================================================
// Game Discord effects
// ============================================================================

// The Discord side of a match's life, wired in with GameManager.SetEffects:
// the notices posted in the game chat, moving players into their team voice
// channels, the map vote and the captain draft. The game manager decides what
// happens; this only shows it and collects the clicks.

const (
	mapVoteDuration   = 45 * time.Second
	draftIDPrefix     = "gdraft1|"
	mapVoteIDPrefix   = "gmvote1|"
	maxMapVoteOptions = 20 // five rows of five, minus the room Discord keeps
)

// GameDiscordEffects are the effects this file provides.
type GameDiscordEffects struct {
	Notify     func(g Game, ev GameEvent)
	ApplyTeams func(g Game) error
	SelectMap  func(g Game, available []engineMapOption, mapMenuRead bool) (*engineMapSelection, error)
}

// NewGameDiscordEffects returns the effects for GameManager.SetEffects and
// registers the component handlers their buttons and menus need. session
// returns the live Discord session.
func NewGameDiscordEffects(session func() *discordgo.Session) GameDiscordEffects {
	d := &gameDiscord{session: session, votes: map[uint]*mapVote{}, closedVotes: map[uint]*closedVote{}, drafts: map[uint]*draftUI{}, live: map[uint]*embedState{}, introductions: map[uint]bool{}}
	RegisterComponentHandler(d.handleComponent)
	return GameDiscordEffects{Notify: d.notify, ApplyTeams: d.applyTeams, SelectMap: d.selectMap}
}

// closedVote is a map vote that is over: its message and the per-map scores it ended with.
type closedVote struct {
	msg  *discordgo.Message
	rows []map[string]string
}

type gameDiscord struct {
	session func() *discordgo.Session

	mu            sync.Mutex
	votes         map[uint]*mapVote
	closedVotes   map[uint]*closedVote // a vote's message once it closed, until the bot has picked the map
	drafts        map[uint]*draftUI
	live          map[uint]*embedState
	introductions map[uint]bool
}

// ============================================================================
// Notices
// ============================================================================

func (d *gameDiscord) notify(g Game, ev GameEvent) {
	s := d.session()
	if s == nil || g.Channels.Text == "" {
		return
	}
	switch ev.Kind {
	case EventDraftNeeded:
		d.startDraft(s, g)
	case EventTeams:
		if Engine.Connected() && GuildHasFeature(g.GuildID, "automated-hosting") && (g.Custom == nil || g.Custom.UseBot) {
			d.postIntroduction(s, g)
		}
		// A pipeline with its own "Teams final" Announce step decides when this goes out.
		if g.Bot != "" || !Engine.Connected() || !flowOf(g.GuildID).Announced["teams"] {
			d.post(s, g, "teams", embedData{})
		}
	case EventStarted:
		if g.Bot != "" {
			d.post(s, g, "matchStarted", embedData{})
			d.startTracker(s, g)
		} else {
			d.manualSetup(s, g)
			d.post(s, g, "matchStartedManual", embedData{})
		}
	case EventHostStatus:
		// Progress is shown by the configured embeds (EventEmbed); the status only matters to the logs.
	case EventEmbed:
		if ev.Key == "introduction" {
			d.mu.Lock()
			alreadyPosted := d.introductions[g.ID]
			d.mu.Unlock()
			if alreadyPosted {
				return
			}
		}
		if ev.Key == "mapSelected" && d.finishVote(s, g, ev) {
			return
		}
		d.post(s, g, ev.Key, embedData{Vars: ev.Vars, Joined: ev.JoinedIDs, Waiting: ev.WaitingIDs, Invited: ev.InvitedIDs})
	case EventMatch:
		d.matchEvent(s, g, ev.Match)
	case EventMatchEnded:
		d.post(s, g, "matchEnded", embedData{Vars: ev.Vars})
	case EventStaffPending:
		d.post(s, g, "staffPending", embedData{})
		d.forget(g.ID)
	case EventCasualDone:
		d.post(s, g, "casualDone", embedData{})
		d.forget(g.ID)
	case EventScored:
		d.post(s, g, "scored", embedData{})
		d.forget(g.ID)
	case EventTrackingLost:
		reason := ev.Reason
		if reason == "" {
			reason = "The hosting bot stopped tracking the match."
		}
		d.post(s, g, "trackingLost", embedData{Vars: map[string]string{"reason": reason}})
		d.forget(g.ID)
	case EventVoided:
		d.post(s, g, "voided", embedData{Vars: map[string]string{"reason": ev.Reason}})
		d.forget(g.ID)
	}
}

// finishVote edits the closed map vote's own message with the map the bot picked, like the old bot did. It reports false
// when there is no such message (a game with one map, say), so the caller posts the embed instead.
func (d *gameDiscord) finishVote(s *discordgo.Session, g Game, ev GameEvent) bool {
	d.mu.Lock()
	cv := d.closedVotes[g.ID]
	delete(d.closedVotes, g.ID)
	d.mu.Unlock()
	if cv == nil {
		return false
	}
	emb, _, _ := d.build(g, "mapSelected", embedData{Vars: ev.Vars, MapRows: cv.rows})
	if emb == nil { // the guild switched the embed off: leave the vote as it ended
		return true
	}
	WithGuildFooterIconForGuild(s, g.GuildID, emb)
	comps := []discordgo.MessageComponent{}
	_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{Channel: cv.msg.ChannelID, ID: cv.msg.ID, Embeds: &[]*discordgo.MessageEmbed{emb}, Components: &comps})
	return true
}

// manualSetup tells the players of a game nobody hosts for them how to set it up: the party to form and the map to play.
func (d *gameDiscord) manualSetup(s *discordgo.Session, g Game) {
	flow := flowOf(g.GuildID)
	var invite []string
	for _, p := range g.Players {
		if p.UserID != g.Host && strings.TrimSpace(p.Username) != "" {
			invite = append(invite, p.Username)
		}
	}
	command := fill(firstNonEmpty(flow.PartyInvite, "/party invite {players}"), map[string]string{"players": strings.Join(invite, " ")})
	d.post(s, g, "partySetup", embedData{Vars: map[string]string{"inviteCommand": strings.TrimSpace(command)}})

	if g.Map == "" || g.Map == MapUndecided {
		return
	}
	maps, _ := GuildMetaMaps(g.GuildID)
	vars := map[string]string{"mapHeight": "—", "mapTeams": "—"}
	count := 0
	for _, m := range maps {
		if m.IsEnabled && m.IsMapAvailable {
			count++
		}
		if m.Name == g.Map {
			if m.MapHeight > 0 {
				vars["mapHeight"] = strconv.Itoa(m.MapHeight)
			}
			if teams := m.TeamsFor(g.Mode.Name); len(teams) > 0 {
				vars["mapTeams"] = strings.Join(teams, ", ")
			}
		}
	}
	vars["mapCount"] = strconv.Itoa(count)
	d.post(s, g, "mapSetup", embedData{Vars: vars})
}

// embedState is what the live embeds of a running match remember: their messages and the match state they show.
type embedState struct {
	msgs       map[string]string // live embed key -> message id
	eliminated map[string]bool   // user id -> out of the match
	bedLost    map[string]bool   // team -> bed gone
	startedAt  time.Time
	pending    bool // a tracker refresh is already scheduled
}

func (d *gameDiscord) state(id uint) *embedState {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.live[id]
	if st == nil {
		st = &embedState{msgs: map[string]string{}, eliminated: map[string]bool{}, bedLost: map[string]bool{}}
		d.live[id] = st
	}
	return st
}

func (d *gameDiscord) forget(id uint) {
	d.mu.Lock()
	delete(d.live, id)
	delete(d.introductions, id)
	d.mu.Unlock()
}

// postIntroduction sends the pipeline's introduction once teams are known, but before the teams and map-vote
// announcements. The engine's announce step still runs first in its own pipeline; its later echo is suppressed.
func (d *gameDiscord) postIntroduction(s *discordgo.Session, g Game) {
	if !flowOf(g.GuildID).Announced["introduction"] {
		return
	}
	if d.post(s, g, "introduction", embedData{}) {
		d.mu.Lock()
		d.introductions[g.ID] = true
		d.mu.Unlock()
	}
}

// build renders the guild's template for key against the game as it is now. ok is false for a key without a default. The
// embed is nil when the guild switched it off (unless data.Force).
func (d *gameDiscord) build(g Game, key string, data embedData) (emb *discordgo.MessageEmbed, t embedTemplate, ok bool) {
	flow := flowOf(g.GuildID)
	t, ok = flow.embedFor(key)
	if !ok {
		return nil, t, false
	}
	st := d.state(g.ID)
	d.mu.Lock()
	elim, bed := maps.Clone(st.eliminated), maps.Clone(st.bedLost)
	started := st.startedAt
	d.mu.Unlock()

	data.Game, data.Flow, data.Eliminated, data.BedLost = g, flow, elim, bed
	if !started.IsZero() {
		vars := maps.Clone(data.Vars)
		if vars == nil {
			vars = map[string]string{}
		}
		vars["startedAt"] = strconv.FormatInt(started.Unix(), 10)
		data.Vars = vars
	}
	return renderEmbed(key, t, data), t, true
}

// post renders the guild's template for key and sends it, or edits the earlier message of a live embed.
func (d *gameDiscord) post(s *discordgo.Session, g Game, key string, data embedData) bool {
	emb, t, ok := d.build(g, key, data)
	if !ok || emb == nil {
		return false
	}
	st := d.state(g.ID)
	d.mu.Lock()
	msgID := st.msgs[key]
	d.mu.Unlock()
	WithGuildFooterIconForGuild(s, g.GuildID, emb)
	live := embedKinds[key].Live
	if live && msgID != "" {
		embeds := []*discordgo.MessageEmbed{emb}
		if _, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{Channel: g.Channels.Text, ID: msgID, Embeds: &embeds}); err == nil {
			return true
		}
	}
	send := &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{emb}}
	if t.Ping != nil && *t.Ping {
		send.Content = pingsOf(g)
		send.AllowedMentions = &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{discordgo.AllowedMentionTypeUsers}}
	}
	m, err := s.ChannelMessageSendComplex(g.Channels.Text, send)
	if err != nil {
		return false
	}
	if live {
		d.mu.Lock()
		st.msgs[key] = m.ID
		d.mu.Unlock()
	}
	return true
}

// startTracker posts the live match tracker the moment the bot starts tracking.
func (d *gameDiscord) startTracker(s *discordgo.Session, g Game) {
	st := d.state(g.ID)
	d.mu.Lock()
	st.startedAt = time.Now()
	d.mu.Unlock()
	d.post(s, g, "liveTracker", embedData{})
}

// refreshTracker edits the live tracker, at most once every moment even when kills come in bursts.
func (d *gameDiscord) refreshTracker(s *discordgo.Session, g Game) {
	st := d.state(g.ID)
	d.mu.Lock()
	if st.pending {
		d.mu.Unlock()
		return
	}
	st.pending = true
	d.mu.Unlock()
	time.AfterFunc(1500*time.Millisecond, func() {
		d.mu.Lock()
		st.pending = false
		d.mu.Unlock()
		d.post(s, g, "liveTracker", embedData{})
	})
}

func mention(id string) string {
	if id == "" {
		return "someone"
	}
	return "<@" + id + ">"
}

// matchEvent updates what the live tracker shows and posts the in-game embeds the guild switched on.
func (d *gameDiscord) matchEvent(s *discordgo.Session, g Game, ev *MatchEvent) {
	if ev == nil {
		return
	}
	st := d.state(g.ID)
	switch ev.Kind {
	case "kill":
		if !ev.Final {
			return
		}
		d.mu.Lock()
		st.eliminated[ev.Victim] = true
		d.mu.Unlock()
		d.post(s, g, "finalKill", embedData{Vars: map[string]string{"killer": mention(ev.Killer), "victim": mention(ev.Victim)}})
	case "bed":
		d.mu.Lock()
		st.bedLost[ev.Team] = true
		d.mu.Unlock()
		d.post(s, g, "bedDestroyed", embedData{Vars: map[string]string{"team": ev.Team, "destroyer": mention(ev.Destroyer)}})
	case "eliminated":
		d.mu.Lock()
		st.bedLost[ev.Team] = true
		for _, p := range g.TeamPlayers(ev.Team) {
			st.eliminated[p.UserID] = true
		}
		d.mu.Unlock()
		d.post(s, g, "teamEliminated", embedData{Vars: map[string]string{"team": ev.Team}})
	case "phase":
		name := ev.PhaseName
		if name == "" {
			name = fmt.Sprintf("Phase %d", ev.Phase)
		}
		d.post(s, g, "phaseChanged", embedData{Vars: map[string]string{"phase": strconv.Itoa(ev.Phase), "phaseName": name}})
		return
	case "stat":
		if !flowOf(g.GuildID).announces(ev.Stat) {
			return
		}
		d.post(s, g, "statTracked", embedData{Vars: map[string]string{
			"stat": flowOf(g.GuildID).label(ev.Stat), "player": mention(ev.Player),
			"amount": strconv.FormatFloat(ev.Amount, 'f', -1, 64), "total": strconv.FormatFloat(ev.Total, 'f', -1, 64),
		}})
		return
	}
	d.refreshTracker(s, g)
}

// applyTeams moves players already in voice into their team's voice channel.
func (d *gameDiscord) applyTeams(g Game) error {
	s := d.session()
	if s == nil {
		return nil
	}
	for _, p := range g.Players {
		vcID := g.Channels.Voice[p.Team]
		if vcID == "" {
			continue
		}
		if vs, err := s.State.VoiceState(g.GuildID, p.UserID); err != nil || vs.ChannelID == "" {
			continue // not in voice: nothing to move
		}
		_ = s.GuildMemberMove(g.GuildID, p.UserID, &vcID)
	}
	return nil
}

// ============================================================================
// Map vote
// ============================================================================

type mapVote struct {
	maps      []types.Map
	votes     map[string]int // user -> map index
	weights   map[string]float64
	message   *discordgo.Message
	done      chan struct{}
	closeOnce sync.Once
	game      Game
	endsAt    int64 // when the vote closes (Unix seconds)
}

// selectMap votes only for configured maps that are present in the selected mode's live menu.
// Each vote is worth the voter's best perk map-vote multiplier.
func (d *gameDiscord) selectMap(g Game, available []engineMapOption, mapMenuRead bool) (*engineMapSelection, error) {
	s := d.session()
	if s == nil || g.Channels.Text == "" {
		return nil, nil
	}
	all, err := GuildMetaMaps(g.GuildID)
	if err != nil {
		return nil, err
	}
	availableSlots := map[string]int{}
	for _, option := range available {
		if name := strings.ToLower(strings.TrimSpace(option.Name)); name != "" {
			availableSlots[name] = option.GUISlot
		}
	}
	var maps []types.Map
	for _, m := range all {
		if m.IsEnabled && m.IsMapAvailable && strings.TrimSpace(m.Name) != "" {
			if mapMenuRead {
				slot, found := availableSlots[strings.ToLower(strings.TrimSpace(m.Name))]
				if !found {
					continue
				}
				m.GUISlot = slot
			}
			maps = append(maps, m)
		}
	}
	// Prefer maps configured with enough team colours, but retain the legacy list if none meet that threshold.
	teams, _ := TeamLayout(g.Mode)
	maps = mapsWithEnoughTeams(maps, g.Mode.Name, teams)
	if len(maps) == 0 {
		d.post(s, g, "noMaps", embedData{})
		return nil, nil
	}
	if len(maps) > maxMapVoteOptions {
		rand.Shuffle(len(maps), func(i, j int) { maps[i], maps[j] = maps[j], maps[i] })
		maps = maps[:maxMapVoteOptions]
	}
	if len(maps) == 1 {
		return mapSelectionWithFallbacks(maps, maps[0], g.Mode.Name), nil
	}

	v := &mapVote{maps: maps, votes: map[string]int{}, weights: map[string]float64{}, done: make(chan struct{}), game: g, endsAt: time.Now().Add(mapVoteDuration).Unix()}
	for _, p := range g.Players {
		v.weights[p.UserID] = Elo.perks(g.GuildID, p.UserID).MapVoteWeight()
	}
	embed, _ := d.voteEmbed(v, "")
	WithGuildFooterIconForGuild(s, g.GuildID, embed)
	msg, err := s.ChannelMessageSendComplex(g.Channels.Text, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{embed}, Components: v.components(g.ID, false, -1)})
	if err != nil {
		return nil, err
	}
	v.message = msg

	d.mu.Lock()
	d.votes[g.ID] = v
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.votes, g.ID)
		d.mu.Unlock()
	}()

	select {
	case <-v.done:
	case <-time.After(mapVoteDuration):
	}

	d.mu.Lock()
	winner := v.winner()
	d.mu.Unlock()
	embed, rows := d.voteEmbed(v, winner.Name)
	WithGuildFooterIconForGuild(s, g.GuildID, embed)
	winnerIdx := slices.IndexFunc(v.maps, func(m types.Map) bool { return m.Name == winner.Name })
	comps := v.components(g.ID, true, winnerIdx)
	_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel: msg.ChannelID, ID: msg.ID, Embeds: &[]*discordgo.MessageEmbed{embed}, Components: &comps,
	})
	// The bot's click on the map edits this same message ("The map selection has concluded!"), as the old bot did.
	d.mu.Lock()
	d.closedVotes[g.ID] = &closedVote{msg: msg, rows: rows}
	d.mu.Unlock()
	selected := slices.IndexFunc(maps, func(m types.Map) bool { return m.Name == winner.Name })
	if selected < 0 {
		return nil, errors.New("the winning map is no longer in the available map list")
	}
	return mapSelectionWithFallbacks(maps, maps[selected], g.Mode.Name), nil
}

func mapsWithEnoughTeams(maps []types.Map, mode string, teamCount int) []types.Map {
	playable := make([]types.Map, 0, len(maps))
	for _, m := range maps {
		if len(m.TeamsFor(mode)) >= teamCount {
			playable = append(playable, m)
		}
	}
	if len(playable) > 0 {
		return playable
	}
	return maps
}

func mapSelectionWithFallbacks(maps []types.Map, selected types.Map, mode string) *engineMapSelection {
	selection := mapSelection(selected, mode)
	seenNames := map[string]struct{}{strings.ToLower(strings.TrimSpace(selected.Name)): {}}
	seenSlots := map[int]struct{}{selected.GUISlot: {}}
	for _, candidate := range maps {
		name := strings.ToLower(strings.TrimSpace(candidate.Name))
		if name == "" {
			continue
		}
		if _, seen := seenNames[name]; seen {
			continue
		}
		if _, seen := seenSlots[candidate.GUISlot]; seen {
			continue
		}
		seenNames[name] = struct{}{}
		seenSlots[candidate.GUISlot] = struct{}{}
		selection.Fallbacks = append(selection.Fallbacks, *mapSelection(candidate, mode))
	}
	return selection
}

func mapSelection(m types.Map, mode string) *engineMapSelection {
	return &engineMapSelection{
		Name:      m.Name,
		GUISlot:   m.GUISlot,
		MapHeight: m.MapHeight,
		Teams:     m.TeamsFor(mode),
	}
}

// totals is the vote weight each map has.
func (v *mapVote) totals() []float64 {
	out := make([]float64, len(v.maps))
	for user, idx := range v.votes {
		out[idx] += v.weights[user]
	}
	return out
}

// winner is the heaviest map; ties and an empty ballot are settled by chance.
func (v *mapVote) winner() types.Map {
	totals := v.totals()
	best := slices.Max(totals)
	var leaders []int
	for i, t := range totals {
		if t == best {
			leaders = append(leaders, i)
		}
	}
	return v.maps[leaders[rand.IntN(len(leaders))]]
}

// voteEmbed is the map vote as the guild's "mapVoting" template draws it, or, once winner is set, the "mapVoteEnded" one.
// The vote cannot be skipped, so a template the guild switched off is drawn anyway.
func (d *gameDiscord) voteEmbed(v *mapVote, winner string) (*discordgo.MessageEmbed, []map[string]string) {
	d.mu.Lock()
	totals := v.totals()
	voted := len(v.votes)
	d.mu.Unlock()
	lines := make([]string, len(v.maps))
	order := make([]int, len(v.maps))
	for i, m := range v.maps {
		lines[i] = fmt.Sprintf("**%s** · `%s`", m.Name, formatWeight(totals[i]))
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return totals[order[a]] > totals[order[b]] })
	var mapRows []map[string]string
	for _, i := range order {
		icon, tag := "🗺️", ""
		if winner != "" && v.maps[i].Name == winner {
			icon, tag = "🏆", " [WINNER]"
		}
		mapRows = append(mapRows, map[string]string{"mapName": v.maps[i].Name, "mapIcon": icon, "mapScore": formatWeight(totals[i]), "winnerTag": tag})
	}
	vars := map[string]string{
		"endsAt": strconv.FormatInt(v.endsAt, 10), "mapVotes": strings.Join(lines, "\n"),
		"voted": strconv.Itoa(voted), "mapCount": strconv.Itoa(len(v.maps)),
	}
	key := "mapVoting"
	if winner != "" {
		key, vars["map"] = "mapVoteEnded", winner
	}
	emb, _, _ := d.build(v.game, key, embedData{Vars: vars, MapRows: mapRows, Force: true})
	if emb == nil { // no default for the key: cannot happen, but a vote must never be left without a message
		emb = &discordgo.MessageEmbed{Title: "Map Voting", Description: strings.Join(lines, "\n")}
	}
	return emb, mapRows
}

// components are the vote's buttons, each showing its map's score. Once the vote is over (winner is its map's index, else -1)
// only the maps that got votes stay, disabled, and the winner's is green.
func (v *mapVote) components(gameID uint, disabled bool, winner int) []discordgo.MessageComponent {
	var rows []discordgo.MessageComponent
	var row []discordgo.MessageComponent
	totals := v.totals()
	for i, m := range v.maps {
		if disabled && totals[i] <= 0 && i != winner {
			continue
		}
		style := discordgo.SecondaryButton
		if disabled && i == winner {
			style = discordgo.SuccessButton
		}
		row = append(row, &discordgo.Button{Label: truncateName(fmt.Sprintf("%s (%s)", m.Name, formatWeight(totals[i])), 80), Style: style, Disabled: disabled, CustomID: mapVoteIDPrefix + strconv.FormatUint(uint64(gameID), 10) + "|" + strconv.Itoa(i)})
		if len(row) == 5 {
			rows = append(rows, &discordgo.ActionsRow{Components: row})
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, &discordgo.ActionsRow{Components: row})
	}
	return rows
}

func formatWeight(w float64) string {
	return strconv.FormatFloat(w, 'f', -1, 64)
}

// MapVoteWeight is how much one map vote of this player counts: their best
// perk's map-vote multiplier (1 without one).
func (c PerkContext) MapVoteWeight() float64 {
	best := 1.0
	for _, p := range c.Perks {
		if p.MapVotingMultiplier > best {
			best = p.MapVotingMultiplier
		}
	}
	return best
}

// ============================================================================
// Captain draft
// ============================================================================

type draftUI struct {
	channelID string
	messageID string
	turnKey   string     // changes whenever someone picks, so a stale auto-pick timer does nothing
	endsAt    int64      // when the current pick times out (Unix seconds), 0 without a time limit
	prev      [][]string // the rosters as the message last showed them, to tell who a pick pulled along
}

// setTimer notes when the pick that starts now times out.
func (ui *draftUI) setTimer(seconds int) {
	ui.endsAt = 0
	if seconds > 0 {
		ui.endsAt = time.Now().Add(time.Duration(seconds) * time.Second).Unix()
	}
}

func (d *gameDiscord) startDraft(s *discordgo.Session, g Game) {
	view, err := Games.Draft(g.ID)
	if err != nil {
		return
	}
	ui := &draftUI{channelID: g.Channels.Text}
	ui.setTimer(Games.FlowSettings(g.GuildID).CaptainFor(g.Mode).PickTimeoutSeconds)
	embed := d.draftEmbed(s, g, view, ui)
	WithGuildFooterIconForGuild(s, g.GuildID, embed)
	msg, err := s.ChannelMessageSendComplex(g.Channels.Text, &discordgo.MessageSend{
		Content:    draftPing(view),
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: draftComponents(g, view),
	})
	if err != nil {
		return
	}
	ui.messageID = msg.ID
	// The captains' party mates were put on their teams before the first pick.
	captainsOnly := make([][]string, len(view.Teams))
	for t, members := range view.Teams {
		captainsOnly[t] = members[:min(1, len(members))]
	}
	d.announceAutoAssigned(s, g, captainsOnly, view.Teams, false)
	ui.prev = cloneTeams(view.Teams)
	d.mu.Lock()
	d.drafts[g.ID] = ui
	d.mu.Unlock()
	d.armDraftTimer(s, g.ID, ui)
}

// armDraftTimer auto-picks for a captain who takes too long.
func (d *gameDiscord) armDraftTimer(s *discordgo.Session, gameID uint, ui *draftUI) {
	g, ok := Games.Get(gameID)
	if !ok {
		return
	}
	seconds := Games.FlowSettings(g.GuildID).CaptainFor(g.Mode).PickTimeoutSeconds
	if seconds <= 0 {
		return
	}
	d.mu.Lock()
	ui.turnKey = strconv.FormatInt(time.Now().UnixNano(), 10)
	key := ui.turnKey
	d.mu.Unlock()

	// Halfway through, the captain is reminded.
	if view, err := Games.Draft(gameID); err == nil && view.Turn >= 0 && view.Turn < len(view.Teams) && len(view.Teams[view.Turn]) > 0 {
		captain := view.Teams[view.Turn][0]
		time.AfterFunc(time.Duration(seconds)*time.Second/2, func() {
			d.mu.Lock()
			current := d.drafts[gameID] == ui && ui.turnKey == key
			d.mu.Unlock()
			if current {
				d.say(s, ui.channelID, "<@"+captain+"> ⏰ **Half the time has passed!** Please make your selection soon.")
			}
		})
	}

	time.AfterFunc(time.Duration(seconds)*time.Second, func() {
		d.mu.Lock()
		current := d.drafts[gameID] == ui && ui.turnKey == key
		d.mu.Unlock()
		if !current {
			return
		}
		teamName := ""
		if before, err := Games.Draft(gameID); err == nil && before.Turn >= 0 {
			teamName = TeamName(before.Turn)
		}
		if captain, picked, err := Games.DraftAutoPick(gameID); err == nil {
			d.say(s, ui.channelID, "<@"+captain+"> has picked <@"+picked+"> for **"+teamName+"** (auto-pick: timeout)")
			d.refreshDraft(s, gameID, ui)
		}
	})
}

// refreshDraft redraws the draft message after a pick, or closes it when the draft is done.
func (d *gameDiscord) refreshDraft(s *discordgo.Session, gameID uint, ui *draftUI) {
	g, ok := Games.Get(gameID)
	if !ok {
		return
	}
	view, err := Games.Draft(gameID)
	d.mu.Lock()
	before := ui.prev
	d.mu.Unlock()
	if err != nil || view.Turn < 0 {
		d.mu.Lock()
		delete(d.drafts, gameID)
		d.mu.Unlock()
		// The teams embed that follows shows the finished rosters, so the picking message goes.
		if s.ChannelMessageDelete(ui.channelID, ui.messageID) != nil {
			done := d.draftEmbed(s, g, view, ui)
			done.Description = "The draft is complete."
			WithGuildFooterIconForGuild(s, g.GuildID, done)
			_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{Channel: ui.channelID, ID: ui.messageID, Embeds: &[]*discordgo.MessageEmbed{done}, Components: &[]discordgo.MessageComponent{}})
		}
		if err == nil {
			d.announceAutoAssigned(s, g, before, view.Teams, true)
		}
		return
	}
	d.mu.Lock()
	ui.setTimer(Games.FlowSettings(g.GuildID).CaptainFor(g.Mode).PickTimeoutSeconds)
	d.mu.Unlock()
	embed := d.draftEmbed(s, g, view, ui)
	WithGuildFooterIconForGuild(s, g.GuildID, embed)
	_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel: ui.channelID, ID: ui.messageID, Content: ptr(draftPing(view)),
		Embeds: &[]*discordgo.MessageEmbed{embed}, Components: ptr(draftComponents(g, view)),
	})
	d.announceAutoAssigned(s, g, before, view.Teams, true)
	d.mu.Lock()
	ui.prev = cloneTeams(view.Teams)
	d.mu.Unlock()
	d.armDraftTimer(s, gameID, ui)
}

func ptr[T any](v T) *T { return &v }

func draftPing(view DraftView) string {
	if view.Turn < 0 || view.Turn >= len(view.Teams) || len(view.Teams[view.Turn]) == 0 {
		return ""
	}
	return "<@" + view.Teams[view.Turn][0] + ">"
}

// draftRows are the rosters of the captain draft for the "captainPicking" template's per-team fields. The draft has no
// teams on the game yet, so they come from the draft's own view.
func draftRows(g Game, view DraftView, t embedTemplate) []map[string]string {
	def := defaultPlayerLines["draft"]
	line, empty := orDefault(t.PlayerLine, def.line), orDefault(t.TeamEmpty, def.empty)
	sep := "\n"
	if t.PlayerSeparator != nil && *t.PlayerSeparator != "" {
		sep = strings.ReplaceAll(*t.PlayerSeparator, `\n`, "\n")
	}
	rows := make([]map[string]string, 0, len(view.Teams))
	for ti, members := range view.Teams {
		lines := make([]string, 0, len(members))
		for i, id := range members {
			name, elo := id, "0"
			if p, ok := g.Player(id); ok {
				name, elo = p.Username, strconv.Itoa(p.EloBefore)
			}
			captain := ""
			if i == 0 {
				captain = "👑"
			}
			lines = append(lines, strings.TrimSpace(fill(line, map[string]string{
				"mention": "<@" + id + ">", "name": name, "id": id, "captain": captain, "team": TeamName(ti), "elo": elo,
			})))
		}
		if len(lines) == 0 {
			lines = []string{empty}
		}
		rows = append(rows, map[string]string{"team": TeamName(ti), "teamPlayers": strings.Join(lines, sep), "bed": ""})
	}
	return rows
}

// draftEmbed is the captain draft as the guild's "captainPicking" template draws it: whose turn it is and the rosters so far.
// The draft cannot be skipped, so a template the guild switched off is drawn anyway.
func (d *gameDiscord) draftEmbed(s *discordgo.Session, g Game, view DraftView, ui *draftUI) *discordgo.MessageEmbed {
	flow := flowOf(g.GuildID)
	t, _ := flow.embedFor("captainPicking")
	vars := map[string]string{"timeLeft": "No time limit"}
	if ui != nil && ui.endsAt > 0 {
		vars["timeLeft"] = "<t:" + strconv.FormatInt(ui.endsAt, 10) + ":R>"
		vars["endsAt"] = strconv.FormatInt(ui.endsAt, 10)
	}
	if view.Turn >= 0 && view.Turn < len(view.Teams) && len(view.Teams[view.Turn]) > 0 {
		captain := view.Teams[view.Turn][0]
		vars["captain"], vars["team"] = "<@"+captain+">", TeamName(view.Turn)
		vars["captainName"] = captain
		if p, ok := g.Player(captain); ok {
			vars["captainName"] = p.Username
		}
		if s != nil {
			if u, err := s.User(captain); err == nil {
				vars["captainAvatar"] = AvatarOf(u)
			}
		}
	}
	remaining := make([]string, 0, len(view.Pool))
	for _, id := range view.Pool {
		line := "- <@" + id + ">"
		if p, ok := g.Player(id); ok {
			line += fmt.Sprintf(" · ELO: `%d`", p.EloBefore)
		}
		remaining = append(remaining, line)
	}
	vars["remaining"] = "`None`"
	if len(remaining) > 0 {
		vars["remaining"] = strings.Join(remaining, "\n")
	}
	emb, _, _ := d.build(g, "captainPicking", embedData{Vars: vars, TeamRows: draftRows(g, view, t), Force: true})
	if emb == nil {
		emb = &discordgo.MessageEmbed{Title: "Captain Picking", Description: "Captains pick their teams in turn."}
	}
	return emb
}

// announceAutoAssigned posts the "partyMembersAutoAssigned" embed for the players a pick pulled onto a team to stay with
// their party. before is the rosters as they were when the message was last drawn.
func (d *gameDiscord) announceAutoAssigned(s *discordgo.Session, g Game, before, after [][]string, picked bool) {
	for t, members := range after {
		from := 0
		if t < len(before) {
			from = len(before[t])
		}
		if from >= len(members) {
			continue
		}
		added := members[from:]
		if picked && from > 0 && len(added) > 0 { // the first newcomer is the pick itself
			added = added[1:]
		}
		if len(added) == 0 || len(members) == 0 {
			continue
		}
		names := make([]string, len(added))
		for i, id := range added {
			names[i] = id
			if p, ok := g.Player(id); ok {
				names[i] = "`" + p.Username + "`"
			}
		}
		// "to stay with" the player the pick pulled them along with (the pick itself), or the captain for the first assignment.
		captain := members[0]
		if picked && from > 0 && from < len(members) {
			captain = members[from]
		}
		if p, ok := g.Player(captain); ok {
			captain = p.Username
		}
		wasWere := "were"
		if len(added) == 1 {
			wasWere = "was"
		}
		d.post(s, g, "partyMembersAutoAssigned", embedData{Vars: map[string]string{
			"names": strings.Join(names, ", "), "wasWere": wasWere, "team": TeamName(t), "captain": captain, "count": strconv.Itoa(len(added)),
		}})
	}
}

func cloneTeams(in [][]string) [][]string {
	out := make([][]string, len(in))
	for i, t := range in {
		out[i] = slices.Clone(t)
	}
	return out
}

func draftComponents(g Game, view DraftView) []discordgo.MessageComponent {
	if view.Turn < 0 || len(view.Pool) == 0 {
		return nil
	}
	options := make([]discordgo.SelectMenuOption, 0, len(view.Pool))
	for _, id := range view.Pool {
		label, desc := id, ""
		if p, ok := g.Player(id); ok {
			label, desc = p.Username, fmt.Sprintf("ELO: %d", p.EloBefore)
			if g.inParty(id) {
				label += " (Party)"
				desc += " | In a party"
			}
		}
		options = append(options, discordgo.SelectMenuOption{Label: truncateName(label, 100), Value: id, Description: desc})
	}
	if len(options) > 25 {
		options = options[:25]
	}
	return []discordgo.MessageComponent{&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		&discordgo.SelectMenu{CustomID: draftIDPrefix + strconv.FormatUint(uint64(g.ID), 10), Placeholder: "Select a player to pick...", Options: options},
	}}}
}

// ============================================================================
// Component handling
// ============================================================================

func (d *gameDiscord) handleComponent(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	switch {
	case strings.HasPrefix(customID, mapVoteIDPrefix):
		d.handleMapVote(s, i, strings.Split(strings.TrimPrefix(customID, mapVoteIDPrefix), "|"))
		return true
	case strings.HasPrefix(customID, draftIDPrefix):
		d.handleDraftPick(s, i, strings.TrimPrefix(customID, draftIDPrefix))
		return true
	}
	return false
}

func ephemeral(s *discordgo.Session, i *discordgo.InteractionCreate, text string) {
	_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: text, Flags: discordgo.MessageFlagsEphemeral},
	})
}

func (d *gameDiscord) handleMapVote(s *discordgo.Session, i *discordgo.InteractionCreate, parts []string) {
	if len(parts) != 2 {
		return
	}
	gameID, _ := strconv.ParseUint(parts[0], 10, 64)
	idx, err := strconv.Atoi(parts[1])
	user := InteractionUserID(i)

	d.mu.Lock()
	v := d.votes[uint(gameID)]
	if v == nil || err != nil || idx < 0 || idx >= len(v.maps) {
		d.mu.Unlock()
		ephemeral(s, i, "This vote is over.")
		return
	}
	if _, in := v.game.Player(user); !in {
		d.mu.Unlock()
		ephemeral(s, i, "Only the players of this game can vote.")
		return
	}
	v.votes[user] = idx
	all := len(v.votes) >= len(v.game.Players)
	d.mu.Unlock()
	embed, _ := d.voteEmbed(v, "")
	WithGuildFooterIconForGuild(s, v.game.GuildID, embed)

	_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Components: v.components(uint(gameID), false, -1)},
	})
	if all {
		v.closeOnce.Do(func() { close(v.done) })
	}
}

func (d *gameDiscord) handleDraftPick(s *discordgo.Session, i *discordgo.InteractionCreate, gameIDText string) {
	gameID, _ := strconv.ParseUint(gameIDText, 10, 64)
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}
	user := InteractionUserID(i)

	d.mu.Lock()
	ui := d.drafts[uint(gameID)]
	d.mu.Unlock()
	if ui == nil {
		ephemeral(s, i, "This draft is over.")
		return
	}
	teamName := ""
	if before, err := Games.Draft(uint(gameID)); err == nil && before.Turn >= 0 {
		teamName = TeamName(before.Turn)
	}
	if err := Games.DraftPick(uint(gameID), user, values[0]); err != nil {
		ephemeral(s, i, draftErrorText(err))
		return
	}
	d.say(s, ui.channelID, "<@"+user+"> has picked <@"+values[0]+"> for **"+teamName+"**")
	_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})
	d.refreshDraft(s, uint(gameID), ui)
}

// say posts a plain line in the game chat.
func (d *gameDiscord) say(s *discordgo.Session, channelID, text string) {
	_, _ = s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Content: text, AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{discordgo.AllowedMentionTypeUsers}}})
}

func draftErrorText(err error) string {
	switch {
	case errors.Is(err, ErrNotYourTurn):
		return "It isn't your turn to pick."
	case errors.Is(err, ErrNotACaptain):
		return "Only the captains pick."
	case errors.Is(err, ErrDraftOver):
		return "The draft is already complete."
	}
	return "That pick wasn't allowed: " + err.Error()
}

// ============================================================================
// Configurable game embeds
// ============================================================================

// Every embed a match posts (pre-game, in-game and post-game) is a template. The defaults below are the old bot's
// wording; a guild changes them on the dashboard (Server Config → Embeds), which stores the changes in the server
// profile's `embeds`. The keys, variables and defaults mirror
// Ranked-Bedwars-Dashboard/src/lib/server-profile/flow.ts: keep the two in step.

// Dots used to show a player's state in a roster.
const (
	DotGreen  = "<:dot_green:1559223765953683476>"
	DotRed    = "<:dot_red:1559223764590272512>"
	DotGray   = "<:dot_gray:1559223768289775646>"
	DotYellow = "<:dot_yellow:1559223767270686730>"
	DotOrange = "<:dot_orange:1559223763785093301>"
	DotWhite  = "<:dot_white:1559223762514214953>"
)

type embedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
	Each   string `json:"each,omitempty"` // "team": repeat for every team; "map": for every map of the vote
}

// embedTemplate is one embed as stored. In an override, a field that is absent keeps the default.
type embedTemplate struct {
	Enabled     *bool         `json:"enabled,omitempty"`
	Style       *string       `json:"style,omitempty"`
	Color       *string       `json:"color,omitempty"`
	Title       *string       `json:"title,omitempty"`
	Description *string       `json:"description,omitempty"`
	Thumbnail   *string       `json:"thumbnail,omitempty"`
	Image       *string       `json:"image,omitempty"`
	Footer      *string       `json:"footer,omitempty"`
	Ping        *bool         `json:"ping,omitempty"`
	Fields      *[]embedField `json:"fields,omitempty"`

	// How the {teamPlayers} list of a team is written. PlayerLine is one player, PlayerLineOut one who is out of the match
	// (live tracker), TeamEmpty a team with nobody, PlayerSeparator what goes between the lines. A line can use {mention},
	// {name}, {id}, {dot}, {captain}, {team}, {elo}, {kills}, {deaths}, {finalKills}, {beds}, {assists}. Absent = the default of the embed.
	PlayerLine      *string `json:"playerLine,omitempty"`
	PlayerLineOut   *string `json:"playerLineOut,omitempty"`
	TeamEmpty       *string `json:"teamEmpty,omitempty"`
	PlayerSeparator *string `json:"playerSeparator,omitempty"`
}

// embedKind says how an embed behaves; the text lives in the template.
type embedKind struct {
	Live      bool   // edited in place instead of posting again
	TeamLines string // "plain" (a mention each), "status" (joined / waiting), "alive" (alive / eliminated) or "locked" (all on their team)
}

var embedKinds = map[string]embedKind{
	"introduction":    {TeamLines: "plain"},
	"teams":           {TeamLines: "plain"},
	"partyInvitation": {Live: true},
	"teamSelection":   {Live: true, TeamLines: "status"},
	"teamsVerified":   {TeamLines: "status"},
	"matchStarted":    {TeamLines: "locked"},
	"liveTracker":     {Live: true, TeamLines: "alive"},
	"captainPicking":  {Live: true, TeamLines: "draft"},
	"matchJoined":     {TeamLines: "tbd"},
	"partySetup":      {TeamLines: "plain"},
}

func str(s string) *string { return &s }
func yes(b bool) *bool     { return &b }

func tpl(style, title, description string, fields ...embedField) embedTemplate {
	t := embedTemplate{Style: str(style), Title: str(title), Description: str(description)}
	if len(fields) > 0 {
		t.Fields = &fields
	}
	return t
}

func off(t embedTemplate) embedTemplate { t.Enabled = yes(false); return t }

var teamField = embedField{Name: "{team}", Value: "{teamPlayers}", Inline: true, Each: "team"}

// merge puts an override on top of a default.
func (t embedTemplate) merge(o embedTemplate) embedTemplate {
	if o.Enabled != nil {
		t.Enabled = o.Enabled
	}
	if o.Style != nil {
		t.Style = o.Style
	}
	if o.Color != nil {
		t.Color = o.Color
	}
	if o.Title != nil {
		t.Title = o.Title
	}
	if o.Description != nil {
		t.Description = o.Description
	}
	if o.Thumbnail != nil {
		t.Thumbnail = o.Thumbnail
	}
	if o.Image != nil {
		t.Image = o.Image
	}
	if o.Footer != nil {
		t.Footer = o.Footer
	}
	if o.Ping != nil {
		t.Ping = o.Ping
	}
	if o.Fields != nil {
		t.Fields = o.Fields
	}
	if o.PlayerLine != nil {
		t.PlayerLine = o.PlayerLine
	}
	if o.PlayerLineOut != nil {
		t.PlayerLineOut = o.PlayerLineOut
	}
	if o.TeamEmpty != nil {
		t.TeamEmpty = o.TeamEmpty
	}
	if o.PlayerSeparator != nil {
		t.PlayerSeparator = o.PlayerSeparator
	}
	return t
}

func (t embedTemplate) on() bool { return t.Enabled == nil || *t.Enabled }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ============================================================================
// The guild's flow config (embeds and custom stats)
// ============================================================================

type statLabel struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Announce bool   `json:"announce"`
}

type guildFlow struct {
	Embeds      map[string]embedTemplate
	Stats       []statLabel
	Announced   map[string]bool // embeds the pipeline posts itself with an Announce step
	PartyInvite string          // the profile's party invite command, with {players}
}

func (f guildFlow) announces(id string) bool {
	for _, s := range f.Stats {
		if s.ID == id {
			return s.Announce
		}
	}
	return false
}

func (f guildFlow) label(id string) string {
	for _, s := range f.Stats {
		if s.ID == id && strings.TrimSpace(s.Label) != "" {
			return s.Label
		}
	}
	return id
}

// embedFor is the embed as configured: the default with the guild's changes on top.
func (f guildFlow) embedFor(key string) (embedTemplate, bool) {
	def, ok := defaultEmbeds[key]
	if !ok {
		return embedTemplate{}, false
	}
	if o, ok := f.Embeds[key]; ok {
		def = def.merge(o)
	}
	return def, true
}

var (
	flowMu    sync.Mutex
	flowCache = map[string]struct {
		at   time.Time
		flow guildFlow
	}{}

	// flowLoader reads the server profile's flow config; tests replace it.
	flowLoader = loadGuildFlow
)

const flowTTL = 15 * time.Second

// flowOf returns the guild's flow config, cached briefly. Any failure falls back to the defaults.
func flowOf(guildID string) guildFlow {
	flowMu.Lock()
	defer flowMu.Unlock()
	if hit, ok := flowCache[guildID]; ok && time.Since(hit.at) < flowTTL {
		return hit.flow
	}
	f := flowLoader(guildID)
	flowCache[guildID] = struct {
		at   time.Time
		flow guildFlow
	}{time.Now(), f}
	return f
}

func loadGuildFlow(guildID string) guildFlow {
	raw, err := database.Must().GameConfig.Field(guildID, "server")
	if err != nil || len(raw) == 0 {
		return guildFlow{}
	}
	return parseGuildFlow(raw)
}

// parseGuildFlow reads the `server` game config: {"profile": {"embeds": {...}, "stats": [...]}}.
func parseGuildFlow(raw json.RawMessage) guildFlow {
	var server struct {
		Profile struct {
			Embeds   map[string]json.RawMessage `json:"embeds"`
			Stats    []statLabel                `json:"stats"`
			Commands struct {
				PartyInvite string `json:"partyInvite"`
			} `json:"commands"`
			Pipeline []struct {
				Type    string `json:"type"`
				Embed   string `json:"embed"`
				Enabled *bool  `json:"enabled"`
			} `json:"pipeline"`
		} `json:"profile"`
	}
	if json.Unmarshal(raw, &server) != nil {
		return guildFlow{}
	}
	f := guildFlow{Embeds: map[string]embedTemplate{}, Stats: server.Profile.Stats, Announced: map[string]bool{}, PartyInvite: strings.TrimSpace(server.Profile.Commands.PartyInvite)}
	if len(server.Profile.Pipeline) == 0 {
		f.Announced["introduction"] = true
	}
	for _, st := range server.Profile.Pipeline {
		if st.Type == "announce" && (st.Enabled == nil || *st.Enabled) {
			f.Announced[st.Embed] = true
		}
	}
	for key, body := range server.Profile.Embeds {
		var t embedTemplate
		if json.Unmarshal(body, &t) == nil { // one bad template doesn't take the others down
			f.Embeds[key] = t
		}
	}
	return f
}

// ============================================================================
// Rendering
// ============================================================================

// embedData is everything a template can draw on.
type embedData struct {
	Game       Game
	Vars       map[string]string // extra variables for this embed
	Joined     []string          // Discord ids that joined the party / their team
	Waiting    []string          // ... that are still missing
	Eliminated map[string]bool   // user ids out of the match (live tracker)
	BedLost    map[string]bool   // team name -> bed gone (live tracker)
	Flow       guildFlow
	TeamRows   []map[string]string // per-team values the caller supplies (the captain draft's rosters) instead of the game's teams
	MapRows    []map[string]string // per-map values of the map vote ({mapName}, {mapIcon}, {mapScore}, {winnerTag})
	Invited    []string            // ... invited to the party but not joined yet
	Force      bool                // draw it even when the guild switched the embed off: the vote and the draft cannot be skipped
}

func fill(text string, vars map[string]string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(text, '{')
		if i < 0 {
			break
		}
		j := strings.IndexByte(text[i:], '}')
		if j < 0 {
			break
		}
		name := text[i+1 : i+j]
		if name == "" || strings.ContainsAny(name, " \n{") || !isWord(name) {
			b.WriteString(text[:i+1])
			text = text[i+1:]
			continue
		}
		b.WriteString(text[:i])
		b.WriteString(vars[name]) // unknown names are empty, so a field built only from one disappears
		text = text[i+j+1:]
	}
	b.WriteString(text)
	return b.String()
}

func isWord(s string) bool {
	for _, r := range s {
		if !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

func (d embedData) common() map[string]string {
	g := d.Game
	m := g.Map
	if m == "" || m == MapUndecided {
		m = "Not chosen"
	}
	vars := map[string]string{
		"game": strconv.Itoa(g.Number), "mode": g.Mode.Name, "map": m, "bot": g.Bot, "host": "", "hostMention": "—",
		"winner": g.Winner, "mvp": g.MVP, "mvpMention": "—", "playerCount": strconv.Itoa(len(g.Players)),
	}
	if p, ok := g.Player(g.Host); ok {
		vars["host"], vars["hostMention"] = p.Username, "<@"+p.UserID+">"
	}
	if g.MVP != "" {
		vars["mvpMention"] = "<@" + g.MVP + ">"
	}
	if len(g.Results) > 0 {
		vars["winners"], vars["losers"] = resultLines(g)
		vars["winnerMentions"], vars["loserMentions"] = resultMentions(g)
	}
	vars["statTable"] = statTable(g, d.Flow)
	vars["joined"], vars["joinedCount"] = namesList(g, d.Joined)
	vars["waiting"], vars["waitingCount"] = namesList(g, d.Waiting)
	vars["invited"], vars["invitedCount"] = namesList(g, d.Invited)
	vars["flow"], vars["teamClause"] = "Standard", "teams have been automatically assigned"
	switch {
	case g.Custom != nil:
		vars["flow"], vars["teamClause"] = "Custom", "with teams chosen by staff"
	case g.Captain:
		vars["flow"], vars["teamClause"] = "Captain", "teams will be drafted by team captains"
	case len(g.Parties) > 0:
		vars["teamClause"] = "teams have been automatically assigned with party groups kept together"
	}
	for k, v := range d.Vars {
		vars[k] = v
	}
	return vars
}

// namesList is who is in the list as the old party embeds wrote it: a line with the Minecraft name each, "None" when empty.
func namesList(g Game, ids []string) (string, string) {
	if len(ids) == 0 {
		return "None", "0"
	}
	lines := make([]string, len(ids))
	for i, id := range ids {
		name := id
		if p, ok := g.Player(id); ok && p.Username != "" {
			name = p.Username
		}
		lines[i] = "- `" + name + "`"
	}
	return strings.Join(lines, "\n"), strconv.Itoa(len(ids))
}

// playersExcept lists the Discord ids of the game's players who are not in skip.
func playersExcept(g Game, skip []string) []string {
	var out []string
	for _, p := range g.Players {
		if !slices.Contains(skip, p.UserID) {
			out = append(out, p.UserID)
		}
	}
	return out
}

func resultLines(g Game) (winners, losers string) {
	name := func(id string) string {
		if p, ok := g.Player(id); ok {
			return p.Username
		}
		return id
	}
	var w, l []string
	for _, r := range g.Results {
		mvp := ""
		if r.MVP {
			mvp = " ⭐"
		}
		line := fmt.Sprintf("%s%s `%d → %d (%+d)`", name(r.UserID), mvp, r.Before, r.After, r.Change)
		if r.Win {
			w = append(w, line)
		} else {
			l = append(l, line)
		}
	}
	return strings.Join(w, "\n"), strings.Join(l, "\n")
}

// resultMentions lists the winners and the losers as plain mentions, the way the old bot did.
func resultMentions(g Game) (winners, losers string) {
	var w, l []string
	for _, r := range g.Results {
		if r.Win {
			w = append(w, "<@"+r.UserID+">")
		} else {
			l = append(l, "<@"+r.UserID+">")
		}
	}
	return strings.Join(w, " "), strings.Join(l, " ")
}

// statTable is one line per player with the built-in stats and any custom stats, best first. It stops before
// Discord's field limit.
func statTable(g Game, f guildFlow) string {
	players := slices.Clone(g.Players)
	sort.SliceStable(players, func(i, j int) bool {
		if players[i].Kills != players[j].Kills {
			return players[i].Kills > players[j].Kills
		}
		return players[i].BedsDestroyed > players[j].BedsDestroyed
	})
	var lines []string
	size := 0
	for _, p := range players {
		if p.Kills+p.Deaths+p.FinalKills+p.BedsDestroyed+p.BedsLost == 0 && len(p.Custom) == 0 {
			continue
		}
		parts := []string{fmt.Sprintf("K %d", p.Kills), fmt.Sprintf("D %d", p.Deaths), fmt.Sprintf("FK %d", p.FinalKills), fmt.Sprintf("B %d", p.BedsDestroyed)}
		ids := make([]string, 0, len(p.Custom))
		for id := range p.Custom {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			parts = append(parts, fmt.Sprintf("%s %g", f.label(id), p.Custom[id]))
		}
		line := fmt.Sprintf("`%s` %s", p.Username, strings.Join(parts, " · "))
		if size+len(line)+1 > 1000 {
			break
		}
		size += len(line) + 1
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// defaultPlayerLines are how each kind of embed writes a player of a team (and one who is out of the match), and an
// empty team, unless the guild's template says otherwise.
var defaultPlayerLines = map[string]struct{ line, out, empty string }{
	"plain":  {"- {mention}", "- {mention}", "`Empty`"},
	"status": {"{dot} `{partyName}`", "{dot} `{partyName}`", "No players"},
	"locked": {"{dot} `{partyName}`", "{dot} `{partyName}`", "No players"},
	"tbd":    {"{dot} `{partyName}`", "{dot} `{partyName}`", ""},
	"alive":  {"{dot} `{name}`", "{dot} ~~`{name}`~~", "No players"},
	"draft":  {"{name} {captain}", "{name} {captain}", "No players"},
}

func orDefault(v *string, def string) string {
	if v != nil && strings.TrimSpace(*v) != "" {
		return *v
	}
	return def
}

// teamVars builds the per-team values for fields repeated for every team. Each player is written with the template's
// PlayerLine (or the kind's default), so a guild chooses what a player's line shows.
// teamRowsFor is the per-team values of fields repeated for every team: the caller's own, or the game's teams.
func (d embedData) teamRowsFor(lines string, t embedTemplate) []map[string]string {
	if d.TeamRows != nil {
		return d.TeamRows
	}
	return d.teamVars(lines, t)
}

func (d embedData) teamVars(lines string, t embedTemplate) []map[string]string {
	waiting := map[string]bool{}
	for _, id := range d.Waiting {
		waiting[id] = true
	}
	def := defaultPlayerLines[lines]
	if def.line == "" {
		def = defaultPlayerLines["plain"]
	}
	line, out, empty := orDefault(t.PlayerLine, def.line), orDefault(t.PlayerLineOut, def.out), orDefault(t.TeamEmpty, def.empty)
	sep := "\n"
	if t.PlayerSeparator != nil && *t.PlayerSeparator != "" {
		sep = strings.ReplaceAll(*t.PlayerSeparator, `\n`, "\n")
	}
	var teams []map[string]string
	for _, team := range d.Game.Teams {
		var rows []string
		for _, p := range d.Game.TeamPlayers(team) {
			dot, tpl := "", line
			switch lines {
			case "status":
				dot = DotGreen
				if waiting[p.UserID] {
					dot = DotRed
				} else if len(d.Joined) == 0 && len(d.Waiting) == 0 {
					dot = DotGray
				}
			case "tbd": // the bot just joined: nobody is placed yet
				dot = DotGray
			case "locked": // the match began: every player is on their team
				dot = DotGreen
			case "alive":
				dot = DotGreen
				if d.Eliminated[p.UserID] {
					dot, tpl = DotRed, out
				}
			}
			captain := ""
			if p.Captain {
				captain = "👑"
			}
			partyName := p.Username
			if d.Game.inParty(p.UserID) {
				partyName += " 👥"
			}
			rows = append(rows, strings.TrimSpace(fill(tpl, map[string]string{
				"mention": "<@" + p.UserID + ">", "name": p.Username, "partyName": partyName, "id": p.UserID, "dot": dot, "captain": captain, "team": team,
				"elo": strconv.Itoa(p.EloBefore), "kills": strconv.Itoa(p.Kills), "deaths": strconv.Itoa(p.Deaths),
				"finalKills": strconv.Itoa(p.FinalKills), "beds": strconv.Itoa(p.BedsDestroyed), "assists": strconv.Itoa(p.Assists),
			})))
		}
		if len(rows) == 0 {
			rows = []string{empty}
		}
		bed := "🛏️"
		if d.BedLost[team] {
			bed = "❌"
		}
		teams = append(teams, map[string]string{"team": team, "teamPlayers": strings.Join(rows, sep), "bed": bed})
	}
	return teams
}

var styleToPreset = map[string]string{"success": "success", "pending": "loading", "info": "info", "caution": "caution", "error": "error", "default": "default"}

// renderEmbed fills a template. It returns nil when the embed is switched off.
func renderEmbed(key string, t embedTemplate, d embedData) *discordgo.MessageEmbed {
	if !t.on() && !d.Force {
		return nil
	}
	vars := d.common()
	var fields []*discordgo.MessageEmbedField
	for _, f := range deref2(t.Fields) {
		rows := []map[string]string{vars}
		if f.Each == "team" && key == "introduction" && d.Game.Captain {
			continue // the teams are not drafted yet
		}
		if f.Each == "map" {
			rows = nil
			for _, mv := range d.MapRows {
				merged := make(map[string]string, len(vars)+4)
				for k, v := range vars {
					merged[k] = v
				}
				for k, v := range mv {
					merged[k] = v
				}
				rows = append(rows, merged)
			}
		}
		if f.Each == "team" {
			rows = nil
			for _, tv := range d.teamRowsFor(embedKinds[key].TeamLines, t) {
				merged := make(map[string]string, len(vars)+3)
				for k, v := range vars {
					merged[k] = v
				}
				for k, v := range tv {
					merged[k] = v
				}
				rows = append(rows, merged)
			}
		}
		for _, v := range rows {
			name, value := strings.TrimSpace(fill(f.Name, v)), strings.TrimSpace(fill(f.Value, v))
			if name == "" || value == "" {
				continue
			}
			fields = append(fields, &discordgo.MessageEmbedField{Name: clip(name, 256), Value: clip(value, 1024), Inline: f.Inline})
			if len(fields) == 25 {
				break
			}
		}
	}

	opts := types.PresetOptions{
		Title:       clip(strings.TrimSpace(fill(deref(t.Title), vars)), 256),
		Description: clip(strings.TrimSpace(fill(deref(t.Description), vars)), 4096),
		Fields:      fields,
		Thumbnail:   strings.TrimSpace(fill(deref(t.Thumbnail), vars)),
		Image:       strings.TrimSpace(fill(deref(t.Image), vars)),
	}
	if footer := strings.TrimSpace(fill(deref(t.Footer), vars)); footer != "" {
		opts.FooterText = clip(footer, 2048)
	}
	if c := strings.TrimPrefix(strings.TrimSpace(deref(t.Color)), "#"); len(c) == 6 {
		if n, err := strconv.ParseInt(c, 16, 32); err == nil {
			opts.Color = int(n)
		}
	}
	style := styleToPreset[deref(t.Style)]
	return Preset(style, opts)
}

func deref2(f *[]embedField) []embedField {
	if f == nil {
		return nil
	}
	return *f
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// pingsOf lists the mentions that go with a message when the template pings the players.
func pingsOf(g Game) string {
	ids := make([]string, 0, len(g.Players))
	for _, p := range g.Players {
		ids = append(ids, "<@"+p.UserID+">")
	}
	return strings.Join(ids, " ")
}

// Embeds shared by the commands that act on a game through the GameManager
// (pick, voidmatch, score, rescore, forcevoid, partialvoid, unvoid,
// submitresult). Known refusals get their own embed; anything else is an
// Unexpected Error so internal details never reach the channel.

// ============================================================================
// Embed Builders
// ============================================================================

// ============================================================================
// Game logs
// ============================================================================

// When a game is released, its two chats are written to Vercel Blob and their
// URLs are recorded in the game's runtime metadata ("logs"), where the
// dashboard's game results page picks them up:
//
//	discord: the messages of the game's text channel or thread (read just
//	         before the channels are deleted)
//	game:    the Minecraft chat the engine's bot saw (sent as a "game.chat" frame)

const (
	gameLogVersion     = 1
	discordLogPages    = 20 // 100 messages each
	gameChatLineLimit  = 20000
	blobUploadDeadline = 90 * time.Second
)

// GameChatLine is defined in package types (games.go).
type GameChatLine = types.GameChatLine

// DiscordLogAuthor is defined in package types (games.go).
type DiscordLogAuthor = types.DiscordLogAuthor

// DiscordLogEmbed and its field and attachment types are defined in package types.
type DiscordLogEmbed = types.DiscordLogEmbed
type DiscordLogEmbedField = types.DiscordLogEmbedField
type DiscordLogAttachment = types.DiscordLogAttachment

// DiscordLogMessage is defined in package types (games.go).
type DiscordLogMessage = types.DiscordLogMessage

// GameLogRefs is defined in package types (games.go).
type GameLogRefs = types.GameLogRefs

// gameLogFile is the JSON document written to the blob store.
type gameLogFile struct {
	Version    int    `json:"version"`
	Kind       string `json:"kind"` // "discord" | "game"
	GameID     uint   `json:"game_id"`
	GameNumber int    `json:"game_number"`
	GuildID    string `json:"guild_id"`
	CreatedAt  string `json:"created_at"`
	Messages   any    `json:"messages"`
}

// StashChat keeps the engine's Minecraft chat for a game until it is archived.
func (m *GameManager) StashChat(id uint, lines []GameChatLine) {
	if len(lines) == 0 {
		return
	}
	m.chatMu.Lock()
	defer m.chatMu.Unlock()
	if m.chat == nil {
		m.chat = map[uint][]GameChatLine{}
	}
	m.chat[id] = append(m.chat[id], lines...)
	if over := len(m.chat[id]) - gameChatLineLimit; over > 0 {
		m.chat[id] = m.chat[id][over:]
	}
}

func (m *GameManager) takeChat(id uint) []GameChatLine {
	m.chatMu.Lock()
	defer m.chatMu.Unlock()
	lines := m.chat[id]
	delete(m.chat, id)
	return lines
}

// archiveLogs writes both logs of a finished game to the blob store and records
// where they are. It runs before the game's channels are deleted, and does
// nothing when blob storage isn't configured.
func (m *GameManager) archiveLogs(g Game) {
	chat := m.takeChat(g.ID)
	if !Blobs.Enabled() {
		return
	}
	var discord []DiscordLogMessage
	if collect := m.fx().CollectChat; collect != nil && g.Channels.Text != "" {
		discord = collect(g.Channels)
	}
	if len(discord) == 0 && len(chat) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), blobUploadDeadline)
	defer cancel()
	put := func(kind string, messages any) string {
		file := gameLogFile{Version: gameLogVersion, Kind: kind, GameID: g.ID, GameNumber: g.Number, GuildID: g.GuildID, CreatedAt: time.Now().UTC().Format(time.RFC3339), Messages: messages}
		url, err := Blobs.PutJSON(ctx, fmt.Sprintf("games/%s/%d-%s.json", g.GuildID, g.ID, kind), file)
		if err != nil {
			m.logf("archive %s log of game %d: %v", kind, g.ID, err)
		}
		return url
	}
	var refs GameLogRefs
	if len(discord) > 0 {
		refs.Discord = put("discord", discord)
	}
	if len(chat) > 0 {
		refs.Game = put("game", chat)
	}
	if refs == (GameLogRefs{}) {
		return
	}
	m.recordLogs(g, refs)
}

// recordLogs merges the log URLs into the stored game's runtime metadata.
func (m *GameManager) recordLogs(g Game, refs GameLogRefs) {
	rec, err := m.api().GetGame(g.GuildID, g.ID)
	if err != nil {
		m.logf("record logs of game %d: %v", g.ID, err)
		return
	}
	meta := map[string]any{}
	if !rec.RuntimeMetadata.IsEmpty() {
		_ = rec.RuntimeMetadata.Unmarshal(&meta)
	}
	meta["logs"] = refs
	raw, err := json.Marshal(meta)
	if err != nil {
		return
	}
	if _, err := m.api().UpdateGame(actorSystem, g.GuildID, g.ID, map[string]any{"runtime_metadata": json.RawMessage(raw)}); err != nil {
		m.logf("record logs of game %d: %v", g.ID, err)
	}
}

// ============================================================================
// Reading the Discord chat
// ============================================================================

// NewGameChatCollector returns the CollectChat effect: it reads the game's text
// channel (or thread), oldest message first. session returns the live session.
func NewGameChatCollector(session func() *discordgo.Session) func(ch GameChannels) []DiscordLogMessage {
	return func(ch GameChannels) []DiscordLogMessage {
		s := session()
		if s == nil || ch.Text == "" {
			return nil
		}
		var out []DiscordLogMessage
		before := ""
		for range discordLogPages {
			page, err := s.ChannelMessages(ch.Text, 100, before, "", "")
			if err != nil || len(page) == 0 {
				break
			}
			for _, msg := range page { // newest first
				out = append(out, logMessage(msg))
			}
			before = page[len(page)-1].ID
			if len(page) < 100 {
				break
			}
		}
		slices.Reverse(out)
		return out
	}
}

func logMessage(msg *discordgo.Message) DiscordLogMessage {
	out := DiscordLogMessage{ID: msg.ID, At: msg.Timestamp}
	if msg.EditedTimestamp != nil {
		out.EditedAt = msg.EditedTimestamp
	}
	out.Pinned = msg.Pinned
	if a := msg.Author; a != nil {
		out.Author = DiscordLogAuthor{ID: a.ID, Name: firstNonEmpty(a.GlobalName, a.Username), Username: a.Username, Avatar: a.AvatarURL("64"), Bot: a.Bot}
	}
	if msg.Member != nil && msg.Member.Nick != "" {
		out.Author.Name = msg.Member.Nick
	}

	out.Content = strings.TrimSpace(msg.Content)
	for _, e := range msg.Embeds {
		embed := DiscordLogEmbed{Title: e.Title, Description: e.Description, URL: e.URL, Color: e.Color, Timestamp: e.Timestamp}
		if e.Author != nil {
			embed.Author = e.Author.Name
		}
		if e.Footer != nil {
			embed.Footer = e.Footer.Text
		}
		if e.Image != nil {
			embed.ImageURL = e.Image.URL
		}
		if e.Thumbnail != nil {
			embed.ThumbnailURL = e.Thumbnail.URL
		}
		for _, f := range e.Fields {
			if f != nil {
				embed.Fields = append(embed.Fields, DiscordLogEmbedField{Name: f.Name, Value: f.Value, Inline: f.Inline})
			}
		}
		out.Embeds = append(out.Embeds, embed)
	}
	out.Components = componentText(msg.Components)
	for _, a := range msg.Attachments {
		out.Attachments = append(out.Attachments, DiscordLogAttachment{Name: a.Filename, URL: a.URL, ContentType: a.ContentType, Size: a.Size})
	}
	for _, reaction := range msg.Reactions {
		if reaction == nil || reaction.Emoji == nil {
			continue
		}
		out.Reactions = append(out.Reactions, types.DiscordLogReaction{
			Name: reaction.Emoji.Name, ID: reaction.Emoji.ID, Count: reaction.Count, Me: reaction.Me,
		})
	}
	if len(msg.Mentions) > 0 {
		out.Mentions = make(map[string]types.DiscordLogMention, len(msg.Mentions))
		for _, u := range msg.Mentions {
			out.Mentions[u.ID] = types.DiscordLogMention{
				ID:       u.ID,
				Name:     firstNonEmpty(u.GlobalName, u.Username),
				Username: u.Username,
			}
		}
	}
	return out
}

// ============================================================================
// Types
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
	ID       uint         `json:"id"`
	Number   int          `json:"number"`          // within its season
	Total    int          `json:"total,omitempty"` // among all the guild's games; only filled in when a channel name asks for it
	GuildID  string       `json:"guild_id"`
	SeasonID uint         `json:"season_id"`
	Mode     types.Mode   `json:"mode"`
	Map      string       `json:"map"`
	State    GameState    `json:"state"`
	End      MatchEnd     `json:"end,omitempty"`
	Players  []GamePlayer `json:"players"`
	Teams    []string     `json:"teams,omitempty"` // team names, in order
	Parties  []PartyGroup `json:"parties,omitempty"`
	Host     string       `json:"host,omitempty"`
	Winner   string       `json:"winner,omitempty"`
	MVP      string       `json:"mvp,omitempty"`
	Captain  bool         `json:"captain,omitempty"` // teams come from a captain draft
	Bot      string       `json:"bot,omitempty"`
	Custom   *CustomGame  `json:"custom,omitempty"` // set for a staff-built game (/hostgame)

	Channels        GameChannels `json:"channels"`
	QueueChannelID  string       `json:"queue_channel_id"`
	QueueCategoryID string       `json:"queue_category_id"`
	StaffCategoryID string       `json:"staff_category_id,omitempty"`

	VoidReason string `json:"void_reason,omitempty"`
	VoidedBy   string `json:"voided_by,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	StateAt   time.Time `json:"state_at"`
	StartedAt time.Time `json:"started_at,omitempty"`

	Results []EloResult `json:"-"` // set once scored
}

func (g Game) clone() Game {
	g.Players = slices.Clone(g.Players)
	g.Teams = slices.Clone(g.Teams)
	g.Results = slices.Clone(g.Results)
	parties := make([]PartyGroup, len(g.Parties))
	for i, p := range g.Parties {
		parties[i] = PartyGroup{Leader: p.Leader, Members: slices.Clone(p.Members)}
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
	return teams, RequiredSlots(m) / teams
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
	Substitutes func(g Game, need int) []QueueMember
	// ChannelsExist reports whether the game's Discord channels still exist (recovery).
	ChannelsExist func(g Game) bool
	// Released is called when a game ends so the queue can be re-swept.
	Released func(guildID, queueChannelID string)
	// SelectMap votes from configured maps, optionally limited to maps read from the mode's live server menu.
	SelectMap func(g Game, available []engineMapOption, mapMenuRead bool) (*engineMapSelection, error)
	// CollectChat reads the game's Discord chat (oldest first) so it can be archived before the channels go.
	CollectChat func(ch GameChannels) []DiscordLogMessage
	// Finished is called once a game reached an end (scored, finished or voided) and was released.
	Finished func(g Game)
	// Host hands the match to the Minecraft side (the engine) once teams are final. The engine asks selectMap to vote after it
	// opens the selected mode and reads that mode's map menu. handled=true means the engine now runs the match; false keeps hosting
	// manual. A non-nil error voids the game, its text is the reason.
	Host func(g Game, selectMap func([]engineMapOption, bool) (*engineMapSelection, error)) (handled bool, err error)
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
			OneCaptainPerParty: true, DefaultPickingType: "one_per_person", AlternatingMinPlayers: 8},
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
	Elo          *EloManager
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

type liveGame struct {
	mu        sync.Mutex
	g         Game
	draft     *Draft
	submitted bool
	busy      bool // scoring in progress
	syncMu    sync.Mutex
}

// GameManager: see the package comment above.
type GameManager struct {
	deps GameDeps

	mu        sync.RWMutex
	games     map[uint]*liveGame
	byPlayer  map[string]uint // guild:user -> id, only while the game is active
	byChannel map[string]uint

	fxMu    sync.RWMutex
	effects GameEffects

	cleanupMu sync.Mutex
	cleanups  map[uint]func()

	chatMu sync.Mutex
	chat   map[uint][]GameChatLine // the engine's Minecraft chat per game, until it is archived
}

// Games is the process-wide manager.
var Games = NewGameManager(GameDeps{})

func init() {
	// Wire "is this player in a game" into the managers that need it. Done here,
	// not in their constructors, so they don't depend on this file.
	Queues.deps.InGame = Games.InGame
	Queues.deps.Flows = Games.deps.Flows
	Parties.deps.InGame = Games.InGame
	Elo.deps.InGame = Games.InGame
	Games.deps.Parties = Parties
}

// NewGameManager builds a manager, filling unset deps with the defaults.
func NewGameManager(d GameDeps) *GameManager {
	if d.Elo == nil {
		d.Elo = Elo
	}
	if d.ReleaseGroup == nil {
		d.ReleaseGroup = func(g, id string) { Queues.RemoveGroup(g, id) }
	}
	if d.Flows == nil {
		cache := newTTLCache[FlowSettings](eloConfigCacheTTL)
		d.Flows = func(g string) FlowSettings {
			f, _ := cache.get(g, func() (FlowSettings, error) { return loadFlows(g) })
			return f
		}
	}
	if d.RankIssues == nil {
		d.RankIssues = func(g string) []string {
			ranks, err := d.Elo.deps.Ranks(g)
			if err != nil {
				return []string{"rank ladder could not be loaded"}
			}
			return ValidateLadder(ranks)
		}
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.After == nil {
		d.After = func(dur time.Duration, f func()) func() {
			t := time.AfterFunc(dur, f)
			return func() { t.Stop() }
		}
	}
	if d.SubmitMin == 0 {
		d.SubmitMin = 3 * time.Minute
	}
	if d.PreGameLimit == 0 {
		d.PreGameLimit = 30 * time.Minute
	}
	if d.CleanupDelay == 0 {
		d.CleanupDelay = 30 * time.Second
	}
	return &GameManager{
		deps: d, games: map[uint]*liveGame{}, byPlayer: map[string]uint{}, byChannel: map[string]uint{},
		cleanups: map[uint]func(){},
	}
}

// FlowSettings returns the guild's team-formation settings.
func (m *GameManager) FlowSettings(guildID string) FlowSettings { return m.deps.Flows(guildID) }

// SetEffects wires the Discord side effects.
func (m *GameManager) SetEffects(e GameEffects) {
	m.fxMu.Lock()
	m.effects = e
	m.fxMu.Unlock()
}

func (m *GameManager) fx() GameEffects {
	m.fxMu.RLock()
	defer m.fxMu.RUnlock()
	return m.effects
}

func (m *GameManager) api() GameAPI {
	if m.deps.API != nil {
		return m.deps.API
	}
	return sdkGameAPI{database.Must()}
}

func (m *GameManager) logf(format string, a ...any) {
	appLog.EngineLogger.Error("[GameManager] " + fmt.Sprintf(format, a...))
}

func (m *GameManager) notify(g Game, ev GameEvent) {
	if fn := m.fx().Notify; fn != nil {
		fn(g, ev)
	}
}

func playerKey(guildID, userID string) string { return guildID + ":" + userID }

// ---- registry ---------------------------------------------------------------

func (m *GameManager) live(id uint) *liveGame {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.games[id]
}

// liveIn is live(id) restricted to one guild, so a command in one server can't touch another's game.
func (m *GameManager) liveIn(guildID string, id uint) *liveGame {
	lg := m.live(id)
	if lg == nil || lg.g.GuildID != guildID { // GuildID never changes after creation
		return nil
	}
	return lg
}

// indexLocked adds/removes lg's players and channels from the lookups. The
// player index only holds players of ACTIVE games: once a game is over its
// players may queue again even while it awaits staff scoring.
func (m *GameManager) indexLocked(lg *liveGame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range lg.g.Players {
		k := playerKey(lg.g.GuildID, p.UserID)
		if lg.g.State.active() {
			m.byPlayer[k] = lg.g.ID
		} else if m.byPlayer[k] == lg.g.ID {
			delete(m.byPlayer, k)
		}
	}
	for _, c := range lg.g.Channels.ids() {
		m.byChannel[c] = lg.g.ID
	}
}

func (m *GameManager) unregister(lg *liveGame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range lg.g.Players {
		if k := playerKey(lg.g.GuildID, p.UserID); m.byPlayer[k] == lg.g.ID {
			delete(m.byPlayer, k)
		}
	}
	for c, id := range m.byChannel {
		if id == lg.g.ID {
			delete(m.byChannel, c)
		}
	}
	delete(m.games, lg.g.ID)
}

// Get returns a snapshot of a live game.
func (m *GameManager) Get(id uint) (Game, bool) {
	lg := m.live(id)
	if lg == nil {
		return Game{}, false
	}
	lg.mu.Lock()
	defer lg.mu.Unlock()
	return lg.g.clone(), true
}

// ByChannel finds the live game that owns a Discord channel (game chat or team voice).
func (m *GameManager) ByChannel(channelID string) (Game, bool) {
	m.mu.RLock()
	id, ok := m.byChannel[channelID]
	m.mu.RUnlock()
	if !ok {
		return Game{}, false
	}
	return m.Get(id)
}

// PlayerGame is the active game a player is in.
func (m *GameManager) PlayerGame(guildID, userID string) (Game, bool) {
	m.mu.RLock()
	id, ok := m.byPlayer[playerKey(guildID, userID)]
	m.mu.RUnlock()
	if !ok {
		return Game{}, false
	}
	return m.Get(id)
}

// InGame reports whether the player is in an active (not yet finished) game.
// The queue, party and decay code use it.
func (m *GameManager) InGame(guildID, userID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.byPlayer[playerKey(guildID, userID)]
	return ok
}

// GuildGames lists the live games of a guild (including ones awaiting scoring).
func (m *GameManager) GuildGames(guildID string) []Game {
	m.mu.RLock()
	ids := make([]uint, 0, len(m.games))
	for id, lg := range m.games {
		if lg.g.GuildID == guildID {
			ids = append(ids, id)
		}
	}
	m.mu.RUnlock()
	slices.Sort(ids)
	var out []Game
	for _, id := range ids {
		if g, ok := m.Get(id); ok {
			out = append(out, g)
		}
	}
	return out
}

type forceVoidGamesAPI interface {
	ListForceVoidGames(guildID string) ([]models.GamesModel, error)
}

// ForceVoidCandidates returns open or completed non-voided games in the active
// season, preferring live snapshots when a game is also in the runtime registry.
func (m *GameManager) ForceVoidCandidates(guildID string) ([]Game, error) {
	api, ok := m.api().(forceVoidGamesAPI)
	if !ok {
		return nil, errors.New("game API does not support force-void game listing")
	}
	records, err := api.ListForceVoidGames(guildID)
	if err != nil {
		return nil, fmt.Errorf("list games for forcevoid: %w", err)
	}

	liveByID := make(map[uint]Game)
	for _, game := range m.GuildGames(guildID) {
		liveByID[game.ID] = game
	}

	seen := make(map[uint]struct{}, len(records))
	candidates := make([]Game, 0, len(records))
	for _, record := range records {
		switch record.Status {
		case string(types.GameStatusWaiting), string(types.GameStatusInProgress), string(types.GameStatusCompleted):
		default:
			continue
		}
		if _, exists := seen[record.ID]; exists {
			continue
		}
		seen[record.ID] = struct{}{}

		game := Game{
			ID:       record.ID,
			Number:   record.GameNumber,
			GuildID:  guildID,
			SeasonID: record.SeasonID,
			Map:      record.MapName,
			Mode:     types.Mode{Name: record.ModeName},
		}
		switch record.Status {
		case string(types.GameStatusWaiting):
			game.State = GameWaiting
		case string(types.GameStatusInProgress):
			game.State = GameInGame
		case string(types.GameStatusCompleted):
			game.State = GameCleanup
		}
		if live, exists := liveByID[record.ID]; exists {
			game = live
		}
		candidates = append(candidates, game)
	}
	return candidates, nil
}

// ---- persistence ------------------------------------------------------------

// sync writes the game's status and runtime metadata. Syncs of one game are
// serialised and always snapshot the current state, so a late one can't
// overwrite a newer state.
func (m *GameManager) sync(lg *liveGame) {
	lg.syncMu.Lock()
	defer lg.syncMu.Unlock()
	lg.mu.Lock()
	g := lg.g.clone()
	lg.mu.Unlock()
	raw, err := json.Marshal(g)
	if err != nil {
		m.logf("marshal game %d: %v", g.ID, err)
		return
	}
	fields := map[string]any{"status": g.State.dbStatus(), "map_name": g.Map, "runtime_metadata": json.RawMessage(raw)}
	if g.Bot != "" {
		fields["bot_name"] = g.Bot
	}
	if _, err := m.api().UpdateGame(actorSystem, g.GuildID, g.ID, fields); err != nil {
		m.logf("sync game %d: %v", g.ID, err)
	}
}

// transition moves lg to a new state (caller holds lg.mu).
func (m *GameManager) transitionLocked(lg *liveGame, to GameState) {
	lg.g.State, lg.g.StateAt = to, m.deps.Now()
	m.indexLocked(lg)
}

// ============================================================================
// Launching a game (the queue hand-off)
// ============================================================================

// Launch turns a formed lobby into a game and starts its pre-game pipeline in
// the background. The lobby is released from the queue either way, so on error
// its players are free to queue again.
func (m *GameManager) Launch(group *QueueGroup) (Game, error) {
	g, err := m.Start(group)
	if err != nil {
		return Game{}, err
	}
	go m.RunPreGame(g.ID)
	return g, nil
}

// Start validates the lobby, creates the game and its participant rows, and
// registers it in the WAITING state. Nothing happens in Discord yet.
func (m *GameManager) Start(group *QueueGroup) (Game, error) { return m.start(group, nil) }

// start is Start for a queue lobby (custom nil) or a staff-built game (see StartCustom).
func (m *GameManager) start(group *QueueGroup, custom *CustomGame) (Game, error) {
	if group == nil || len(group.Players) == 0 || group.ChannelID == "" || group.CategoryID == "" {
		return Game{}, ErrInvalidGroup
	}
	defer m.deps.ReleaseGroup(group.GuildID, group.ID)
	if !GuildPlanActive(group.GuildID) {
		return Game{}, ErrPlanInactive
	}

	if custom == nil { // a staff-built game sets its own team sizes
		teams, perTeam := TeamLayout(group.Mode)
		slots := RequiredSlots(group.Mode)
		if slots <= 0 || slots%teams != 0 || len(group.Players) != slots || perTeam < 1 {
			return Game{}, fmt.Errorf("%w: %d players for a %d-slot mode", ErrBadRoster, len(group.Players), slots)
		}
	}

	seen := map[string]bool{}
	for _, p := range group.Players {
		if seen[p.UserID] {
			return Game{}, fmt.Errorf("%w: duplicate player %s", ErrInvalidGroup, p.UserID)
		}
		seen[p.UserID] = true
	}
	// Claim every player up front (all or nothing) so two launches racing for
	// the same player can't both get past the check; the claim becomes the
	// real index entry once the game exists, or is dropped on failure.
	if busy := m.reserve(group.GuildID, group.Players); busy != "" {
		return Game{}, &BusyError{busy}
	}
	registered := false
	defer func() {
		if !registered {
			m.unreserve(group.GuildID, group.Players)
		}
	}()

	season, err := m.api().ActiveSeason(group.GuildID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return Game{}, ErrNoSeason
		}
		return Game{}, err
	}

	ids := make([]string, len(group.Players))
	for i, p := range group.Players {
		ids[i] = p.UserID
	}
	ratings := m.deps.Elo.Ratings(group.GuildID, ids)

	now := m.deps.Now()
	g := Game{
		GuildID: group.GuildID, SeasonID: season, Mode: group.Mode, Map: MapUndecided, State: GameWaiting,
		Captain:        custom == nil && m.deps.Flows(group.GuildID).IsCaptain(group.Mode),
		Custom:         custom,
		QueueChannelID: group.ChannelID, QueueCategoryID: group.CategoryID,
		CreatedAt: now, StateAt: now, Channels: GameChannels{Voice: map[string]string{}},
	}
	for _, pg := range group.Parties {
		g.Parties = append(g.Parties, PartyGroup{Leader: pg.Leader, Members: slices.Clone(pg.Members)})
	}

	var missing []string
	rows := make([]models.GamePlayersModel, 0, len(group.Players))
	for _, p := range group.Players {
		pid, err := m.api().PlayerID(group.GuildID, p.UserID)
		if err != nil {
			if sdk.IsNotFound(err) {
				missing = append(missing, p.UserID)
				continue
			}
			return Game{}, err
		}
		g.Players = append(g.Players, GamePlayer{UserID: p.UserID, Username: p.Username, PlayerID: pid, Team: teamUnassigned, EloBefore: ratings[p.UserID]})
		rows = append(rows, models.GamePlayersModel{PlayerID: pid, Team: teamUnassigned, InParty: g.inParty(p.UserID)})
	}
	if len(missing) > 0 {
		return Game{}, &NotRegisteredError{missing}
	}

	rec, err := m.api().CreateGame(actorSystem, group.GuildID, sdk.CreateGameRequest{SeasonID: &season, MapName: g.Map, ModeName: group.Mode.Name, Participants: rows})
	if err != nil {
		return Game{}, err
	}
	g.ID, g.Number = rec.ID, rec.GameNumber

	lg := &liveGame{g: g}
	m.mu.Lock()
	m.games[g.ID] = lg
	m.mu.Unlock()
	registered = true
	lg.mu.Lock()
	m.indexLocked(lg)
	snap := lg.g.clone()
	lg.mu.Unlock()
	m.sync(lg)
	return snap, nil
}

// reserved marks a player claimed by a launch that has no game ID yet.
const reserved = 0

// reserve claims all players for a launch, or none: it returns the first
// player already in (or claimed for) a game.
func (m *GameManager) reserve(guildID string, players []QueueMember) (busy string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range players {
		if _, in := m.byPlayer[playerKey(guildID, p.UserID)]; in {
			return p.UserID
		}
	}
	for _, p := range players {
		m.byPlayer[playerKey(guildID, p.UserID)] = reserved
	}
	return ""
}

func (m *GameManager) unreserve(guildID string, players []QueueMember) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range players {
		if k := playerKey(guildID, p.UserID); m.byPlayer[k] == reserved {
			delete(m.byPlayer, k)
		}
	}
}

const (
	// MapUndecided is the map name of a game before its map is chosen.
	MapUndecided   = "TBD"
	teamUnassigned = "Unassigned"
)

func isCaptainMode(m types.Mode) bool {
	t := strings.ToLower(strings.TrimSpace(m.Type))
	return t == "captains" || t == "captain" || t == "elo" // "captain" and "elo" are legacy names of the captain flow
}

// isRandomMode reports whether the mode's type is "random": a plain shuffle when it picks no formation.
func isRandomMode(m types.Mode) bool {
	return strings.EqualFold(strings.TrimSpace(m.Type), "random")
}

func (g Game) inParty(userID string) bool {
	for _, pg := range g.Parties {
		if slices.Contains(pg.Members, userID) {
			return true
		}
	}
	return false
}

// ============================================================================
// Pre-game
// ============================================================================

// RunPreGame runs the setup pipeline: roster check, rank-ladder gate,
// channels, then teams (automatic now, or the captain draft, which pauses the
// pipeline until it finishes). Any failure voids the game.
func (m *GameManager) RunPreGame(id uint) {
	lg := m.live(id)
	if lg == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			m.logf("pre-game panic in game %d: %v", id, r)
			_ = m.Void(actorSystem, lg.g.GuildID, id, "An unexpected error occurred during setup.", VoidOptions{})
		}
	}()
	if err := m.preGame(lg); err != nil {
		if errors.Is(err, errGameGone) {
			return
		}
		m.logf("pre-game for game %d: %v", id, err)
		_ = m.Void(actorSystem, lg.g.GuildID, id, "An unexpected error occurred during setup.", VoidOptions{})
	}
}

var errGameGone = errors.New("game was voided")

func (m *GameManager) preGame(lg *liveGame) error {
	lg.mu.Lock()
	if lg.g.State != GameWaiting {
		lg.mu.Unlock()
		return nil
	}
	m.transitionLocked(lg, GamePreGame)
	lg.mu.Unlock()
	m.sync(lg)

	if voided, err := m.verifyRoster(lg); err != nil || voided {
		if voided {
			return errGameGone
		}
		return err
	}

	g := m.snapshot(lg)
	if g.Mode.IsEloGainEnabled {
		if issues := m.deps.RankIssues(g.GuildID); len(issues) > 0 {
			_ = m.Void(actorSystem, g.GuildID, g.ID, "Competitive rank configuration incomplete: "+strings.Join(issues, "; "), VoidOptions{})
			return errGameGone
		}
	}

	if fn := m.fx().CreateChannels; fn != nil {
		ch, err := fn(g)
		if err != nil {
			return fmt.Errorf("create channels: %w", err)
		}
		lg.mu.Lock()
		if ch.Voice == nil {
			ch.Voice = map[string]string{}
		}
		lg.g.Channels = ch
		m.indexLocked(lg)
		lg.mu.Unlock()
		m.sync(lg)
	}

	if g.Custom != nil {
		return m.assignCustomTeams(lg)
	}
	flows := m.deps.Flows(g.GuildID)
	if captains := flows.CaptainFor(g.Mode); g.Captain && captains.IsEnabled {
		return m.beginDraft(lg, captains)
	}
	return m.assignStandardTeams(lg, flows.StandardFor(g.Mode))
}

func (m *GameManager) snapshot(lg *liveGame) Game {
	lg.mu.Lock()
	defer lg.mu.Unlock()
	return lg.g.clone()
}

// verifyRoster replaces players who left the queue channel with eligible
// waiting players, or voids the game when it can't be filled.
func (m *GameManager) verifyRoster(lg *liveGame) (voided bool, err error) {
	g := m.snapshot(lg)
	present := m.fx().PresentInQueue
	if present == nil {
		return false, nil
	}
	here := present(g)
	var missing []GamePlayer
	for _, p := range g.Players {
		if !here[p.UserID] {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return false, nil
	}

	var subs []QueueMember
	if fn := m.fx().Substitutes; fn != nil {
		for _, s := range fn(g, len(missing)) {
			if len(subs) == len(missing) {
				break
			}
			if _, in := g.Player(s.UserID); in || m.InGame(g.GuildID, s.UserID) || slices.ContainsFunc(subs, func(o QueueMember) bool { return o.UserID == s.UserID }) {
				continue
			}
			if _, e := m.api().PlayerID(g.GuildID, s.UserID); e != nil {
				continue
			}
			subs = append(subs, s)
		}
	}
	if len(subs) < len(missing) {
		_ = m.Void(actorSystem, g.GuildID, g.ID, "A player left before the game could start and no replacement was available.", VoidOptions{})
		return true, nil
	}

	ratings := m.deps.Elo.Ratings(g.GuildID, memberIDs(subs))
	for i, old := range missing {
		sub := subs[i]
		pid, e := m.api().PlayerID(g.GuildID, sub.UserID)
		if e != nil {
			return false, e
		}
		if e := m.api().RemoveParticipant(actorSystem, g.GuildID, g.ID, old.PlayerID); e != nil {
			return false, e
		}
		if e := m.api().AddParticipant(actorSystem, g.GuildID, g.ID, models.GamePlayersModel{PlayerID: pid, Team: teamUnassigned}); e != nil {
			return false, e
		}
		lg.mu.Lock()
		for j := range lg.g.Players {
			if lg.g.Players[j].UserID == old.UserID {
				lg.g.Players[j] = GamePlayer{UserID: sub.UserID, Username: sub.Username, PlayerID: pid, Team: teamUnassigned, EloBefore: ratings[sub.UserID]}
			}
		}
		// A party that lost a member can't be assumed intact: drop groups touching the leaver.
		lg.g.Parties = slices.DeleteFunc(lg.g.Parties, func(pg PartyGroup) bool { return slices.Contains(pg.Members, old.UserID) })
		m.mu.Lock()
		delete(m.byPlayer, playerKey(g.GuildID, old.UserID))
		m.mu.Unlock()
		m.indexLocked(lg)
		lg.mu.Unlock()
	}
	m.sync(lg)
	return false, nil
}

func memberIDs(ms []QueueMember) []string {
	out := make([]string, len(ms))
	for i, x := range ms {
		out[i] = x.UserID
	}
	return out
}

func (m *GameManager) assignStandardTeams(lg *liveGame, cfg StandardFlow) error {
	g := m.snapshot(lg)
	teams, perTeam := TeamLayout(g.Mode)
	ids := g.userIDs()

	var assigned [][]string
	if !cfg.IsEnabled { // plain queue order
		for t := 0; t < teams; t++ {
			assigned = append(assigned, slices.Clone(ids[t*perTeam:(t+1)*perTeam]))
		}
	} else {
		opts := TeamOptions{
			Teams: teams, PerTeam: perTeam, Randomness: cfg.RandomnessPercent,
			Ratings: m.deps.Elo.Ratings(g.GuildID, ids), BalanceParties: cfg.BalancePartyGroups, AllowSplit: cfg.AllowSplitParties,
			Method: m.deps.Elo.deps.Party(g.GuildID).EloAggregationMethod, Rand: m.deps.Rand,
		}
		if cfg.KeepPartyTogether {
			opts.Parties = g.Parties
		}
		var err error
		if assigned, err = AssignTeams(ids, opts); err != nil {
			return err
		}
	}
	return m.setTeams(lg, assigned, nil)
}

// setTeams stores the final teams (captains are marked), persists each
// player's team, applies them in Discord, picks the host and starts the match.
func (m *GameManager) setTeams(lg *liveGame, teams [][]string, captains []string) error {
	lg.mu.Lock()
	if lg.g.State != GamePreGame {
		lg.mu.Unlock()
		return errGameGone
	}
	names := make([]string, len(teams))
	for t, members := range teams {
		names[t] = TeamName(t)
		for _, id := range members {
			for i := range lg.g.Players {
				if lg.g.Players[i].UserID == id {
					lg.g.Players[i].Team = names[t]
					lg.g.Players[i].Captain = slices.Contains(captains, id)
				}
			}
		}
	}
	lg.g.Teams = names
	lg.g.Host = m.pickHost(lg.g)
	lg.draft = nil
	g := lg.g.clone()
	lg.mu.Unlock()

	for _, p := range g.Players {
		if err := m.api().UpdateParticipant(actorSystem, g.GuildID, g.ID, p.PlayerID, map[string]any{"team": p.Team, "is_captain": p.Captain, "in_party": g.inParty(p.UserID)}); err != nil {
			return fmt.Errorf("save team of %s: %w", p.UserID, err)
		}
	}
	m.sync(lg)
	if fn := m.fx().ApplyTeams; fn != nil {
		if err := fn(g); err != nil {
			return fmt.Errorf("apply teams: %w", err)
		}
	}
	m.notify(g, GameEvent{Kind: EventTeams})

	selectMap := m.fx().SelectMap
	chooseMap := func(available []engineMapOption, mapMenuRead bool) (*engineMapSelection, error) {
		if selectMap == nil {
			return nil, nil
		}
		selection, err := selectMap(g, available, mapMenuRead)
		if err == nil && selection != nil && strings.TrimSpace(selection.Name) != "" {
			if setErr := m.SetMap(g.ID, selection.Name); setErr != nil {
				return nil, setErr
			}
			g.Map = selection.Name
		}
		return selection, err
	}
	if fn := m.fx().Host; fn != nil {
		handled, err := fn(g, chooseMap)
		if err != nil {
			_ = m.Void(actorSystem, g.GuildID, g.ID, err.Error(), VoidOptions{})
			return errGameGone
		}
		if handled {
			return nil // the engine starts the match
		}
	}
	if selectMap != nil {
		if _, err := chooseMap(nil, false); err != nil {
			m.logf("select map for game %d: %v", g.ID, err)
		}
	}
	return m.BeginMatch(g.ID)
}

// Notify posts an out-of-band event (the engine bridge's progress) to the game's chat.
func (m *GameManager) Notify(id uint, ev GameEvent) {
	if g, ok := m.Get(id); ok {
		m.notify(g, ev)
	}
}

// pickHost: the first party leader in the game, else the highest-rated player.
func (m *GameManager) pickHost(g Game) string {
	if g.Custom != nil {
		if _, ok := g.Player(g.Custom.HostedBy); ok {
			return g.Custom.HostedBy
		}
	}
	for _, pg := range g.Parties {
		if _, ok := g.Player(pg.Leader); ok {
			return pg.Leader
		}
	}
	best := ""
	bestElo := -1
	for _, p := range g.Players {
		if p.EloBefore > bestElo {
			best, bestElo = p.UserID, p.EloBefore
		}
	}
	return best
}

// BeginMatch starts play: PRE_GAME -> IN_GAME.
func (m *GameManager) BeginMatch(id uint) error {
	lg := m.live(id)
	if lg == nil {
		return ErrGameNotFound
	}
	lg.mu.Lock()
	if lg.g.State != GamePreGame || len(lg.g.Teams) == 0 {
		lg.mu.Unlock()
		return ErrWrongState
	}
	m.transitionLocked(lg, GameInGame)
	lg.g.StartedAt = m.deps.Now()
	g := lg.g.clone()
	lg.mu.Unlock()

	if _, err := m.api().StartGame(actorSystem, g.GuildID, g.ID); err != nil {
		m.logf("start game %d: %v", g.ID, err)
	}
	m.sync(lg)
	m.notify(g, GameEvent{Kind: EventStarted})
	return nil
}

// SetBot records the hosting bot's Minecraft account.
func (m *GameManager) SetBot(id uint, name string) error {
	lg := m.live(id)
	if lg == nil {
		return ErrGameNotFound
	}
	lg.mu.Lock()
	lg.g.Bot = strings.TrimSpace(name)
	lg.mu.Unlock()
	m.sync(lg)
	return nil
}

// SetMap records the map the match is played on.
func (m *GameManager) SetMap(id uint, name string) error {
	lg := m.live(id)
	if lg == nil {
		return ErrGameNotFound
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("map name is empty")
	}
	lg.mu.Lock()
	lg.g.Map = name
	lg.mu.Unlock()
	m.sync(lg)
	return nil
}

// ---- captain draft ----------------------------------------------------------

func (m *GameManager) beginDraft(lg *liveGame, cfg CaptainFlow) error {
	g := m.snapshot(lg)
	teams, perTeam := TeamLayout(g.Mode)
	d, err := NewDraft(g.userIDs(), teams, perTeam, m.deps.Elo.Ratings(g.GuildID, g.userIDs()), g.Parties, DraftConfig{
		PickingType: cfg.DefaultPickingType, TopEloChance: cfg.TopEloCaptainChance, OneCaptainPerParty: cfg.OneCaptainPerParty,
		AlternatingMinPlayers: cfg.AlternatingMinPlayers, AutoAssignParties: cfg.AutoAssignParties,
	}, m.deps.Rand)
	if err != nil {
		return err
	}
	lg.mu.Lock()
	lg.draft = d
	lg.mu.Unlock()
	m.notify(g, GameEvent{Kind: EventDraftNeeded})
	return m.finishDraftIfDone(lg)
}

// DraftView is defined in package types (games.go).
type DraftView = types.DraftView

// Draft returns the running captain draft of a game.
func (m *GameManager) Draft(id uint) (DraftView, error) {
	lg := m.live(id)
	if lg == nil {
		return DraftView{}, ErrGameNotFound
	}
	lg.mu.Lock()
	defer lg.mu.Unlock()
	if lg.draft == nil {
		return DraftView{}, ErrNoDraftYet
	}
	d := lg.draft
	view := DraftView{Pool: d.Pool(), Type: d.Type, Captains: d.Captains()}
	view.Turn = d.Turn()
	for _, t := range d.Teams {
		view.Teams = append(view.Teams, slices.Clone(t))
	}
	return view, nil
}

// DraftPick makes a captain's pick; when it completes the draft the teams are applied and the match starts.
func (m *GameManager) DraftPick(id uint, captain, player string) error {
	lg := m.live(id)
	if lg == nil {
		return ErrGameNotFound
	}
	lg.mu.Lock()
	if lg.draft == nil {
		lg.mu.Unlock()
		return ErrNoDraftYet
	}
	_, err := lg.draft.Pick(captain, player)
	lg.mu.Unlock()
	if err != nil {
		return err
	}
	m.finishDraftAsync(lg)
	return nil
}

// finishDraftAsync completes a finished draft in the background: applying the teams runs the map vote and
// hands the game to the engine, which takes far longer than the 3 seconds a Discord interaction may wait.
func (m *GameManager) finishDraftAsync(lg *liveGame) {
	go func() { _ = m.finishDraftIfDone(lg) }()
}

// DraftAutoPick picks for the captain whose turn it is (their pick timed out).
func (m *GameManager) DraftAutoPick(id uint) (captain, player string, err error) {
	lg := m.live(id)
	if lg == nil {
		return "", "", ErrGameNotFound
	}
	lg.mu.Lock()
	if lg.draft == nil {
		lg.mu.Unlock()
		return "", "", ErrNoDraftYet
	}
	captain, player, err = lg.draft.AutoPick()
	lg.mu.Unlock()
	if err != nil {
		return "", "", err
	}
	m.finishDraftAsync(lg)
	return captain, player, nil
}

func (m *GameManager) finishDraftIfDone(lg *liveGame) error {
	lg.mu.Lock()
	d := lg.draft
	if d == nil || d.Turn() >= 0 { // Turn also auto-fills the last team in the 4-captain pattern
		lg.mu.Unlock()
		return nil
	}
	teams := make([][]string, len(d.Teams))
	for i, t := range d.Teams {
		teams[i] = slices.Clone(t)
	}
	captains := d.Captains()
	lg.mu.Unlock()
	if err := m.setTeams(lg, teams, captains); err != nil && !errors.Is(err, errGameGone) {
		m.logf("finish draft for game %d: %v", lg.g.ID, err)
		_ = m.Void(actorSystem, lg.g.GuildID, lg.g.ID, "An unexpected error occurred while finishing the draft.", VoidOptions{})
		return err
	}
	return nil
}

// ============================================================================
// Ending a match
// ============================================================================

// ResolveTeam maps user input ("a", "Team B") to a team name of the game.
func (g Game) ResolveTeam(input string) (string, bool) {
	if i := TeamIndex(input, len(g.Teams)); i >= 0 {
		return g.Teams[i], true
	}
	for _, t := range g.Teams {
		if strings.EqualFold(strings.TrimSpace(input), t) {
			return t, true
		}
	}
	return "", false
}

// SubmitOutcome is what Submit did.
type SubmitOutcome struct {
	End  MatchEnd // EndStaffPending or EndCasual
	Team string   // the team the submitter claimed won (ranked)
	Game Game
}

// Submit is a player reporting that the match is over (with their proof). In
// a ranked mode the claim goes to staff scoring (their team is the claimed
// winner); in a casual mode the match just ends. It is refused before the
// minimum match length, twice, and from non-participants.
func (m *GameManager) Submit(id uint, userID string) (SubmitOutcome, error) {
	lg := m.live(id)
	if lg == nil {
		return SubmitOutcome{}, ErrGameNotFound
	}
	lg.mu.Lock()
	g := lg.g
	p, ok := g.Player(userID)
	switch {
	case !ok:
		lg.mu.Unlock()
		return SubmitOutcome{}, ErrNotParticipant
	case g.State != GameInGame:
		lg.mu.Unlock()
		return SubmitOutcome{}, ErrWrongState
	case lg.submitted || lg.busy:
		lg.mu.Unlock()
		return SubmitOutcome{}, ErrAlreadySubmitted
	}
	if left := m.deps.SubmitMin - m.deps.Now().Sub(g.StartedAt); left > 0 {
		lg.mu.Unlock()
		return SubmitOutcome{}, &TooEarlyError{left}
	}
	if !g.Mode.IsEloGainEnabled {
		lg.submitted = true
		lg.mu.Unlock()
		snap, err := m.finishCasual(lg, actorSystem)
		return SubmitOutcome{End: EndCasual, Game: snap}, err
	}
	team, ok := g.ResolveTeam(p.Team)
	if !ok {
		lg.mu.Unlock()
		return SubmitOutcome{}, ErrUnknownTeam
	}
	lg.submitted = true
	snap := g.clone()
	lg.mu.Unlock()

	category := ""
	if fn := m.fx().StaffScoring; fn != nil {
		var err error
		if category, err = fn(snap); err != nil {
			lg.mu.Lock()
			lg.submitted = false // a transient Discord failure must not block a retry
			lg.mu.Unlock()
			return SubmitOutcome{}, fmt.Errorf("move to scoring: %w", err)
		}
	}
	lg.mu.Lock()
	lg.g.End, lg.g.Winner, lg.g.StaffCategoryID = EndStaffPending, team, category
	m.transitionLocked(lg, GamePostGame)
	snap = lg.g.clone()
	lg.mu.Unlock()
	m.sync(lg)
	m.notify(snap, GameEvent{Kind: EventStaffPending})
	return SubmitOutcome{End: EndStaffPending, Team: team, Game: snap}, nil
}

// ScoreOptions is defined in package types (games.go).
type ScoreOptions = types.ScoreOptions

// ScoreResult is the outcome of scoring a match.
type ScoreResult struct {
	Game    Game
	Results []EloResult
}

// Score finishes a live ranked match with the given winning team: staff
// answering a submission, or an automated tracker reporting the result.
// It works on a match that is in progress or awaiting scoring. If scoring
// fails nothing was written and it can simply be retried.
func (m *GameManager) Score(actor, guildID string, id uint, winningTeam string, o ScoreOptions) (ScoreResult, error) {
	lg := m.liveIn(guildID, id)
	if lg == nil {
		return ScoreResult{}, ErrGameNotFound
	}
	lg.mu.Lock()
	if lg.busy {
		lg.mu.Unlock()
		return ScoreResult{}, ErrBusy
	}
	if s := lg.g.State; s != GameInGame && s != GamePostGame {
		lg.mu.Unlock()
		return ScoreResult{}, ErrWrongState
	}
	team, ok := lg.g.ResolveTeam(winningTeam)
	if !ok {
		lg.mu.Unlock()
		return ScoreResult{}, ErrUnknownTeam
	}
	if strings.TrimSpace(o.Map) != "" {
		lg.g.Map = strings.TrimSpace(o.Map)
	}
	if !lg.g.Mode.IsEloGainEnabled {
		lg.g.Winner = team
		lg.mu.Unlock()
		snap, err := m.finishCasual(lg, actor)
		return ScoreResult{Game: snap}, err
	}
	lg.busy = true
	g := lg.g.clone()
	lg.mu.Unlock()
	defer func() {
		lg.mu.Lock()
		lg.busy = false
		lg.mu.Unlock()
	}()

	results, err := m.commitRanked(actor, g, team, o.MVP, false)
	if err != nil {
		return ScoreResult{}, err
	}
	lg.mu.Lock()
	lg.g.Winner, lg.g.End, lg.g.Results = team, EndRanked, results
	if mv := mvpOf(results); mv != "" {
		lg.g.MVP = mv
	}
	m.transitionLocked(lg, GameCleanup)
	snap := lg.g.clone()
	lg.mu.Unlock()

	m.sync(lg)
	m.notify(snap, GameEvent{Kind: EventScored})
	m.finish(lg, snap)
	return ScoreResult{Game: snap, Results: results}, nil
}

func mvpOf(rs []EloResult) string {
	for _, r := range rs {
		if r.MVP {
			return r.UserID
		}
	}
	return ""
}

// mvpFor decides the MVP: the staff pick if it is a winner, else the best
// winner by recorded performance, else nobody (no evidence, no bonus).
func mvpFor(g Game, winners []string, staffPick string) string {
	if staffPick != "" && slices.Contains(winners, staffPick) {
		return staffPick
	}
	var cands []MVPCandidate
	any := false
	for _, p := range g.Players {
		any = any || p.Kills > 0 || p.BedsDestroyed > 0 || p.FinalKills > 0
		cands = append(cands, MVPCandidate{ID: p.UserID, Kills: p.Kills, Deaths: p.Deaths, Beds: p.BedsDestroyed})
	}
	if !any {
		return ""
	}
	return SelectMVP(cands, winners, int64(g.ID))
}

// commitRanked scores g with winningTeam, writes it in one CompleteGame
// transaction, and runs the follow-ups (ranks, party counters). The
// results are only committed if CompleteGame succeeds; the follow-ups are
// best effort because the scoring itself is already durable.
func (m *GameManager) commitRanked(actor string, g Game, winningTeam, staffMVP string, rescore bool) ([]EloResult, error) {
	var winners, losers []string
	for _, p := range g.Players {
		if strings.EqualFold(p.Team, winningTeam) {
			winners = append(winners, p.UserID)
		} else {
			losers = append(losers, p.UserID)
		}
	}
	if len(winners) == 0 || len(losers) == 0 {
		return nil, fmt.Errorf("%w: %s has %d winners and %d losers", ErrUnknownTeam, winningTeam, len(winners), len(losers))
	}
	mvp := mvpFor(g, winners, staffMVP)
	mode := g.Mode
	results, err := m.deps.Elo.ScoreGame(ScoreInput{GuildID: g.GuildID, SeasonID: g.SeasonID, Winners: winners, Losers: losers, MVP: mvp, Parties: g.Parties, Mode: &mode})
	if err != nil {
		return nil, fmt.Errorf("score: %w", err)
	}
	byUser := make(map[string]EloResult, len(results))
	for _, r := range results {
		byUser[r.UserID] = r
	}

	rows := make([]models.GamePlayersModel, 0, len(g.Players))
	for _, p := range g.Players {
		r := byUser[p.UserID]
		row := participantRow(g, p)
		row.IsWinner, row.IsMvp = r.Win, r.MVP
		row.EloBefore, row.EloAfter, row.EloChange = r.Before, r.After, r.Change
		rows = append(rows, row)
	}
	if _, err := m.completeGame(actor, g, sdk.CompleteGameRequest{Status: string(types.GameStatusCompleted), Scoring: "ranked", Participants: rows}); err != nil {
		return nil, fmt.Errorf("complete game: %w", err)
	}

	m.deps.Elo.RecordStartingGrants(actor, g.GuildID, results)
	m.deps.Elo.ApplyRanks(actor, g.GuildID, results)
	if !rescore && m.deps.Parties != nil {
		for _, pg := range g.Parties {
			m.deps.Parties.RecordGame(g.GuildID, pg.Leader)
		}
	}
	return results, nil
}

func participantRow(g Game, p GamePlayer) models.GamePlayersModel {
	return models.GamePlayersModel{
		PlayerID: p.PlayerID, Team: p.Team, IsCaptain: p.Captain, InParty: g.inParty(p.UserID),
		Kills: p.Kills, Deaths: p.Deaths, Assists: p.Assists, FinalKills: p.FinalKills,
		BedsDestroyed: p.BedsDestroyed, BedsLost: p.BedsLost, FirstBloods: p.FirstBloods,
	}
}

type customStatsGameCompleter interface {
	CompleteGameWithCustomStats(actor, guildID string, id uint, in sdk.CompleteGameRequest, customStats map[uint]map[string]float64) (*models.GamesModel, error)
}

func (m *GameManager) completeGame(actor string, g Game, in sdk.CompleteGameRequest) (*models.GamesModel, error) {
	customStats := make(map[uint]map[string]float64)
	for _, player := range g.Players {
		if len(player.Custom) != 0 {
			customStats[player.PlayerID] = player.Custom
		}
	}
	if len(customStats) != 0 {
		api := m.api()
		if customAPI, ok := api.(customStatsGameCompleter); ok {
			return customAPI.CompleteGameWithCustomStats(actor, g.GuildID, g.ID, in, customStats)
		}
		return nil, errors.New("database API does not support custom game statistics")
	}
	return m.api().CompleteGame(actor, g.GuildID, g.ID, in)
}

// commitCasual writes a finished match of a mode that awards no ELO. Nobody
// won, so no wins/losses are recorded; gameplay counters are kept when the
// mode tracks stats.
func (m *GameManager) commitCasual(actor string, g Game) error {
	scoring := "none"
	if g.Mode.IsStatsTrackingEnabled {
		scoring = "casual"
	}
	rows := make([]models.GamePlayersModel, 0, len(g.Players))
	for _, p := range g.Players {
		rows = append(rows, participantRow(g, p))
	}
	_, err := m.completeGame(actor, g, sdk.CompleteGameRequest{Status: string(types.GameStatusCompleted), Scoring: scoring, Participants: rows})
	if err != nil {
		return fmt.Errorf("complete game: %w", err)
	}
	return nil
}

// finishCasual ends a live casual match.
func (m *GameManager) finishCasual(lg *liveGame, actor string) (Game, error) {
	lg.mu.Lock()
	if lg.busy {
		lg.mu.Unlock()
		return Game{}, ErrBusy
	}
	lg.busy = true
	g := lg.g.clone()
	lg.mu.Unlock()
	defer func() {
		lg.mu.Lock()
		lg.busy = false
		lg.mu.Unlock()
	}()

	if err := m.commitCasual(actor, g); err != nil {
		lg.mu.Lock()
		lg.submitted = false
		lg.mu.Unlock()
		return Game{}, err
	}
	lg.mu.Lock()
	lg.g.End = EndCasual
	m.transitionLocked(lg, GameCleanup)
	snap := lg.g.clone()
	lg.mu.Unlock()
	m.sync(lg)
	m.notify(snap, GameEvent{Kind: EventCasualDone})
	m.finish(lg, snap)
	return snap, nil
}

// finish releases a game that reached a terminal state: channels are deleted
// after a delay, the registry forgets it, and the queue is re-swept.
func (m *GameManager) finish(lg *liveGame, g Game) {
	Engine.Cancel(g.ID, "The game ended.") // a no-op unless the engine is still hosting it
	m.scheduleChannelCleanup(g)
	m.unregister(lg)
	if fn := m.fx().Finished; fn != nil {
		fn(g)
	}
	if fn := m.fx().Released; fn != nil {
		fn(g.GuildID, g.QueueChannelID)
	}
}

func (m *GameManager) scheduleChannelCleanup(g Game) {
	fn := m.fx().DeleteChannels
	if fn == nil || len(g.Channels.ids()) == 0 && g.Channels.Category == "" {
		go m.archiveLogs(g)
		return
	}
	cancel := m.deps.After(m.deps.CleanupDelay, func() {
		m.cleanupMu.Lock()
		delete(m.cleanups, g.ID)
		m.cleanupMu.Unlock()
		m.archiveLogs(g) // the chat is read while the channels still exist
		fn(g.GuildID, g.Channels)
	})
	m.cleanupMu.Lock()
	if old := m.cleanups[g.ID]; old != nil {
		old()
	}
	m.cleanups[g.ID] = cancel
	m.cleanupMu.Unlock()
}

// ============================================================================
// Voiding
// ============================================================================

// VoidOptions is defined in package types (games.go).
type VoidOptions = types.VoidOptions

// Void cancels a game. A live game is stopped and its channels cleaned up; no
// stats were applied so none are touched. A game that was already completed
// has its results reversed (stats, ELO, ranks). Voiding twice is an error.
func (m *GameManager) Void(actor, guildID string, id uint, reason string, o VoidOptions) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "No reason provided"
	}
	if lg := m.liveIn(guildID, id); lg != nil {
		return m.voidLive(lg, actor, reason, o)
	}
	return m.voidStored(actor, guildID, id, reason)
}

func (m *GameManager) voidLive(lg *liveGame, actor, reason string, o VoidOptions) error {
	lg.mu.Lock()
	if lg.busy {
		lg.mu.Unlock()
		return ErrBusy
	}
	if lg.g.State == GameVoided {
		lg.mu.Unlock()
		return ErrAlreadyVoided
	}
	guildID, id := lg.g.GuildID, lg.g.ID
	lg.busy = true
	lg.mu.Unlock()

	if _, err := m.api().EndGame(actor, guildID, id, string(types.GameStatusVoided)); err != nil {
		lg.mu.Lock()
		lg.busy = false
		lg.mu.Unlock()
		return fmt.Errorf("end game: %w", err)
	}

	lg.mu.Lock()
	lg.g.End, lg.g.VoidReason, lg.g.VoidedBy = EndVoided, reason, actor
	m.transitionLocked(lg, GameVoided)
	lg.draft = nil
	lg.busy = false
	g := lg.g.clone()
	lg.mu.Unlock()

	m.logf("game %d voided by %s: %s", g.ID, actor, reason)
	m.sync(lg)
	m.notify(g, GameEvent{Kind: EventVoided, Reason: reason})
	if o.KeepChannels {
		g.Channels = GameChannels{}
	}
	m.finish(lg, g)
	return nil
}

// voidStored voids a game that is not live: one that was scored (reverse it),
// or an orphaned open row left by a crash.
func (m *GameManager) voidStored(actor, guildID string, id uint, reason string) error {
	rec, err := m.storedGame(guildID, id)
	if err != nil {
		return err
	}
	if rec.Status == string(types.GameStatusVoided) {
		return ErrAlreadyVoided
	}
	if rec.EndedAt != nil { // completed: take the results back first
		if err := m.reverseStored(actor, guildID, rec, nil); err != nil {
			return err
		}
	}
	if rec.EndedAt == nil {
		if _, err := m.api().EndGame(actor, guildID, id, string(types.GameStatusVoided)); err != nil {
			return err
		}
	} else if _, err := m.api().UpdateGame(actor, guildID, id, map[string]any{"status": string(types.GameStatusVoided)}); err != nil {
		return err
	}
	m.stampVoid(guildID, rec, reason, actor)
	return nil
}

// stampVoid records why/by whom in the game's runtime metadata (merged into what is there).
func (m *GameManager) stampVoid(guildID string, rec *models.GamesModel, reason, by string) {
	meta := map[string]any{}
	if !rec.RuntimeMetadata.IsEmpty() {
		_ = rec.RuntimeMetadata.Unmarshal(&meta)
	}
	meta["state"], meta["end"], meta["void_reason"], meta["voided_by"] = string(GameVoided), string(EndVoided), reason, by
	raw, err := json.Marshal(meta)
	if err != nil {
		return
	}
	if _, err := m.api().UpdateGame(actorSystem, guildID, rec.ID, map[string]any{"runtime_metadata": json.RawMessage(raw)}); err != nil {
		m.logf("stamp void on game %d: %v", rec.ID, err)
	}
}

// storedGame loads a game row from the database, 404 -> ErrGameNotFound.
func (m *GameManager) storedGame(guildID string, id uint) (*models.GamesModel, error) {
	rec, err := m.api().GetGame(guildID, id)
	if err != nil {
		if sdk.IsNotFound(err) {
			return nil, ErrGameNotFound
		}
		return nil, err
	}
	return rec, nil
}

// reverseStored reverses a completed game's results for playerIDs (nil = all)
// and repairs everything derived from them.
func (m *GameManager) reverseStored(actor, guildID string, rec *models.GamesModel, playerIDs []uint) error {
	holders := m.startingGrantHolders(guildID, rec, playerIDs)
	if _, err := m.api().ReverseGame(actor, guildID, rec.ID, playerIDs, false); err != nil {
		return fmt.Errorf("reverse results: %w", err)
	}
	m.deps.Elo.ReopenStartingGrants(actor, guildID, holders)
	m.afterReverse(actor, guildID, rec.ID, playerIDs)
	return nil
}

// startingGrantHolders lists the players (Discord IDs) whose starting-ELO grant was paid by this game's scoring.
// Call it BEFORE ReverseGame: the reversal takes the grant's ELO back (it is part of the game's change) but leaves
// the claim flag set, so without ReopenStartingGrants the player would lose the grant for good (a rescore would
// not repay it and /claimelo would refuse). A grant claimed by /claimelo comes before the game ended, so it is
// told apart by its timestamp (RecordStartingGrants stamps it right after the game completes).
func (m *GameManager) startingGrantHolders(guildID string, rec *models.GamesModel, playerIDs []uint) []string {
	if rec.EndedAt == nil {
		return nil
	}
	rows, err := m.api().Participants(guildID, rec.ID)
	if err != nil {
		m.logf("load participants of game %d: %v", rec.ID, err)
		return nil
	}
	var out []string
	for _, p := range rows {
		if p.Scoring != "ranked" || len(playerIDs) > 0 && !slices.Contains(playerIDs, p.PlayerID) {
			continue
		}
		uid, err := m.api().UserID(guildID, p.PlayerID)
		if err != nil {
			continue
		}
		st, err := m.deps.Elo.api().Stats(guildID, uid)
		if err == nil && st.StartingEloClaimed && st.StartingEloClaimedAt != nil && !st.StartingEloClaimedAt.Before(*rec.EndedAt) {
			out = append(out, uid)
		}
	}
	return out
}

// afterReverse repairs what ReverseGame can't: the rank names / roles of the players whose ELO changed.
func (m *GameManager) afterReverse(actor, guildID string, gameID uint, playerIDs []uint) {
	rows, err := m.api().Participants(guildID, gameID)
	if err != nil {
		m.logf("load participants of game %d: %v", gameID, err)
		return
	}
	for _, p := range rows {
		if len(playerIDs) > 0 && !slices.Contains(playerIDs, p.PlayerID) {
			continue
		}
		if uid, err := m.api().UserID(guildID, p.PlayerID); err == nil {
			if _, err := m.deps.Elo.SyncRank(actor, guildID, uid); err != nil {
				m.logf("rank sync %s: %v", uid, err)
			}
		}
	}
}

// ============================================================================
// Stored Games
// ============================================================================

// Operations on games that are not (or no longer) in the live registry: staff
// rescoring, partial voids, un-voiding, crash recovery and the watchdog. They
// work from the database rows, which are the truth for teams and stats.

// ErrRescoreIncomplete means a rescore took the old results back but could not
// apply the new ones; the game is open and ScoreStored finishes the job.
var ErrRescoreIncomplete = errors.New("old results were reversed but the new ones could not be applied; run the score again")

// loadStored rebuilds a Game from a database row: the archived runtime
// metadata when present, the participant rows always (they are the truth for
// teams and stats).
func (m *GameManager) loadStored(guildID string, rec *models.GamesModel) (Game, []models.GamePlayersModel, error) {
	rows, err := m.api().Participants(guildID, rec.ID)
	if err != nil {
		return Game{}, nil, err
	}
	var g Game
	if !rec.RuntimeMetadata.IsEmpty() {
		_ = rec.RuntimeMetadata.Unmarshal(&g)
	}
	names := map[string]string{}
	for _, p := range g.Players {
		names[p.UserID] = p.Username
	}
	g.ID, g.Number, g.GuildID, g.SeasonID, g.Map = rec.ID, rec.GameNumber, guildID, rec.SeasonID, rec.MapName
	if g.Mode.Name == "" {
		modes, err := GuildMetaModes(guildID)
		if err != nil {
			return Game{}, nil, err
		}
		md := FindMode(modes, rec.ModeName)
		if md == nil {
			return Game{}, nil, fmt.Errorf("mode %q of game %d is no longer configured", rec.ModeName, rec.ID)
		}
		g.Mode = *md
	}
	g.Players = nil
	var teams []string
	for _, r := range rows {
		uid, err := m.api().UserID(guildID, r.PlayerID)
		if err != nil {
			return Game{}, nil, fmt.Errorf("player %d: %w", r.PlayerID, err)
		}
		name := names[uid]
		if name == "" {
			name = uid
		}
		g.Players = append(g.Players, GamePlayer{
			UserID: uid, Username: name, PlayerID: r.PlayerID, Team: r.Team, Captain: r.IsCaptain,
			Kills: r.Kills, Deaths: r.Deaths, Assists: r.Assists, FinalKills: r.FinalKills,
			BedsDestroyed: r.BedsDestroyed, BedsLost: r.BedsLost, FirstBloods: r.FirstBloods,
		})
		if r.Team != teamUnassigned && !slices.Contains(teams, r.Team) {
			teams = append(teams, r.Team)
		}
	}
	slices.Sort(teams)
	g.Teams = teams
	return g, rows, nil
}

// storedWinner is the team named, else the team the stored rows say won.
func storedWinner(g Game, rows []models.GamePlayersModel, named string) (string, error) {
	if strings.TrimSpace(named) != "" {
		if t, ok := g.ResolveTeam(named); ok {
			return t, nil
		}
		return "", ErrUnknownTeam
	}
	for _, r := range rows {
		if r.IsWinner {
			return r.Team, nil
		}
	}
	return "", ErrNothingToRestore
}

func anyScored(rows []models.GamePlayersModel) bool {
	return slices.ContainsFunc(rows, func(r models.GamePlayersModel) bool { return r.Scoring != "" })
}

// ScoreStored scores an open game that is not in the registry (an orphan left
// by a crash, or one reopened by a rescore/un-void). winningTeam "" uses the
// winner recorded on the rows.
func (m *GameManager) ScoreStored(actor, guildID string, id uint, winningTeam, mvp string) (ScoreResult, error) {
	if m.liveIn(guildID, id) != nil {
		return ScoreResult{}, ErrWrongState // it is live: use Score
	}
	rec, err := m.storedGame(guildID, id)
	if err != nil {
		return ScoreResult{}, err
	}
	if rec.EndedAt != nil {
		return ScoreResult{}, ErrWrongState
	}
	g, rows, err := m.loadStored(guildID, rec)
	if err != nil {
		return ScoreResult{}, err
	}
	team, err := storedWinner(g, rows, winningTeam)
	if err != nil {
		return ScoreResult{}, err
	}
	return m.commitStored(actor, g, team, mvp)
}

func (m *GameManager) commitStored(actor string, g Game, team, mvp string) (ScoreResult, error) {
	if !g.Mode.IsEloGainEnabled {
		if err := m.commitCasual(actor, g); err != nil {
			return ScoreResult{}, err
		}
		g.End, g.State = EndCasual, GameCleanup
		m.stampState(g)
		return ScoreResult{Game: g}, nil
	}
	results, err := m.commitRanked(actor, g, team, mvp, true)
	if err != nil {
		return ScoreResult{}, err
	}
	g.Winner, g.End, g.State, g.Results, g.MVP = team, EndRanked, GameCleanup, results, mvpOf(results)
	m.stampState(g)
	return ScoreResult{Game: g, Results: results}, nil
}

// stampState rewrites the game's runtime metadata to reflect g's final state.
func (m *GameManager) stampState(g Game) {
	raw, err := json.Marshal(g)
	if err != nil {
		return
	}
	if _, err := m.api().UpdateGame(actorSystem, g.GuildID, g.ID, map[string]any{"status": g.State.dbStatus(), "runtime_metadata": json.RawMessage(raw)}); err != nil {
		m.logf("stamp game %d: %v", g.ID, err)
	}
}

// Rescore changes the winner of a scored game: the old results are reversed
// and the new ones applied from the players' current ratings. mvp may be "".
// The winner must be named (the recorded one is what is being replaced).
func (m *GameManager) Rescore(actor, guildID string, id uint, winningTeam, mvp string) (ScoreResult, error) {
	if m.liveIn(guildID, id) != nil {
		return ScoreResult{}, ErrWrongState
	}
	rec, err := m.storedGame(guildID, id)
	if err != nil {
		return ScoreResult{}, err
	}
	g, rows, err := m.loadStored(guildID, rec)
	if err != nil {
		return ScoreResult{}, err
	}
	if !anyScored(rows) {
		return ScoreResult{}, ErrNotScored
	}
	team, err := storedWinner(g, rows, winningTeam)
	if err != nil {
		return ScoreResult{}, err
	}
	holders := m.startingGrantHolders(guildID, rec, nil)
	if _, err := m.api().ReverseGame(actor, guildID, id, nil, true); err != nil {
		return ScoreResult{}, fmt.Errorf("reverse results: %w", err)
	}
	m.deps.Elo.ReopenStartingGrants(actor, guildID, holders)
	m.afterReverse(actor, guildID, id, nil)
	res, err := m.commitStored(actor, g, team, mvp)
	if err != nil {
		return ScoreResult{}, fmt.Errorf("%w: %v", ErrRescoreIncomplete, err)
	}
	return res, nil
}

// PartialVoid reverses only the losing team of a scored game and leaves the
// winners' gains alone. winningTeam "" uses the recorded winner. It returns
// the losers' Discord IDs.
func (m *GameManager) PartialVoid(actor, guildID string, id uint, winningTeam string) ([]string, error) {
	if m.liveIn(guildID, id) != nil {
		return nil, ErrWrongState
	}
	rec, err := m.storedGame(guildID, id)
	if err != nil {
		return nil, err
	}
	g, rows, err := m.loadStored(guildID, rec)
	if err != nil {
		return nil, err
	}
	if !anyScored(rows) {
		return nil, ErrNotScored
	}
	team, err := storedWinner(g, rows, winningTeam)
	if err != nil {
		return nil, err
	}
	var ids []uint
	var losers []string
	for _, p := range g.Players {
		if !strings.EqualFold(p.Team, team) {
			ids = append(ids, p.PlayerID)
			losers = append(losers, p.UserID)
		}
	}
	if len(ids) == 0 {
		return nil, ErrUnknownTeam
	}
	holders := m.startingGrantHolders(guildID, rec, ids)
	if _, err := m.api().ReverseGame(actor, guildID, id, ids, false); err != nil {
		return nil, fmt.Errorf("reverse results: %w", err)
	}
	m.deps.Elo.ReopenStartingGrants(actor, guildID, holders)
	m.afterReverse(actor, guildID, id, ids)
	return losers, nil
}

// Unvoid brings back a game that was voided after it had been scored: it is
// reopened and scored again from the players' CURRENT ratings with the
// recorded winner (the old deltas can't be replayed once other games moved
// the ratings). A ranked game voided before it was ever scored has no
// recorded winner and nothing to restore.
func (m *GameManager) Unvoid(actor, guildID string, id uint) (ScoreResult, error) {
	if m.liveIn(guildID, id) != nil {
		return ScoreResult{}, ErrWrongState
	}
	rec, err := m.storedGame(guildID, id)
	if err != nil {
		return ScoreResult{}, err
	}
	if rec.Status != "voided" {
		return ScoreResult{}, ErrWrongState
	}
	g, rows, err := m.loadStored(guildID, rec)
	if err != nil {
		return ScoreResult{}, err
	}
	team := ""
	if g.Mode.IsEloGainEnabled {
		if team, err = storedWinner(g, rows, ""); err != nil {
			return ScoreResult{}, err // decided before reopening anything
		}
	}
	if _, err := m.api().ReverseGame(actor, guildID, id, nil, true); err != nil {
		return ScoreResult{}, fmt.Errorf("reopen: %w", err)
	}
	res, err := m.commitStored(actor, g, team, "")
	if err != nil {
		return ScoreResult{}, fmt.Errorf("%w: %v", ErrRescoreIncomplete, err)
	}
	return res, nil
}

// ============================================================================
// Recovery & watchdog
// ============================================================================

// RecoverSummary is defined in package types (games.go).
type RecoverSummary = types.RecoverSummary

// Recover rebuilds the registry after a restart from the games the database
// still lists as open. A running game stays running; a game awaiting staff
// scoring keeps waiting; a game that was still being set up is voided (its
// pipeline died with the process; its players are free to queue again); a
// row with no usable metadata is cancelled; a row that is already scored is
// just closed. Call it once the bot is connected.
func (m *GameManager) Recover(guildIDs []string) RecoverSummary {
	var sum RecoverSummary
	for _, guildID := range guildIDs {
		for _, status := range []string{string(types.GameStatusWaiting), string(types.GameStatusInProgress)} {
			recs, err := m.api().ListGames(guildID, status)
			if err != nil {
				m.logf("recover: list %s games of %s: %v", status, guildID, err)
				continue
			}
			for i := range recs {
				m.recoverOne(guildID, &recs[i], &sum)
			}
		}
	}
	return sum
}

func (m *GameManager) recoverOne(guildID string, rec *models.GamesModel, sum *RecoverSummary) {
	if m.live(rec.ID) != nil {
		return
	}
	var g Game
	if rec.RuntimeMetadata.IsEmpty() || rec.RuntimeMetadata.Unmarshal(&g) != nil || len(g.Players) == 0 {
		if _, err := m.api().EndGame(actorSystem, guildID, rec.ID, string(types.GameStatusCancelled)); err != nil {
			m.logf("recover: cancel game %d: %v", rec.ID, err)
		}
		sum.Cancelled++
		return
	}
	g.ID, g.Number, g.GuildID, g.SeasonID = rec.ID, rec.GameNumber, guildID, rec.SeasonID
	if g.Channels.Voice == nil {
		g.Channels.Voice = map[string]string{}
	}

	if rows, err := m.api().Participants(guildID, rec.ID); err == nil && anyScored(rows) {
		// Scored, but the row still says open: close it and let the channels go.
		g.State, g.End = GameCleanup, EndRanked
		if _, err := m.api().UpdateGame(actorSystem, guildID, rec.ID, map[string]any{"status": string(types.GameStatusCompleted)}); err != nil {
			m.logf("recover: close scored game %d: %v", rec.ID, err)
		}
		m.scheduleChannelCleanup(g)
		sum.Closed++
		return
	}

	lg := &liveGame{g: g, submitted: g.End == EndStaffPending}
	m.mu.Lock()
	m.games[g.ID] = lg
	m.mu.Unlock()
	lg.mu.Lock()
	lg.g.StateAt = m.deps.Now()
	m.indexLocked(lg)
	lg.mu.Unlock()

	switch g.State {
	case GamePostGame: // awaiting staff scoring: keep, whether or not the chat survived
		sum.Kept++
	case GameInGame:
		if exists := m.fx().ChannelsExist; exists != nil && g.Channels.Text != "" && !exists(g) {
			_ = m.Void(actorSystem, guildID, g.ID, "The game channels were deleted while the bot was offline.", VoidOptions{})
			sum.Voided++
			return
		}
		sum.Kept++
	default: // waiting / pre-game / anything unexpected
		_ = m.Void(actorSystem, guildID, g.ID, "The bot restarted while the game was being set up.", VoidOptions{})
		sum.Voided++
	}
}

// Sweep voids games stuck in setup past PreGameLimit (and running games past
// MaxInGame, when set). It returns how many it voided.
func (m *GameManager) Sweep() int {
	now := m.deps.Now()
	type stale struct {
		guild  string
		id     uint
		reason string
	}
	var todo []stale
	// Copy the registry first: game locks are taken before the registry lock
	// elsewhere, so they must not be taken while holding it.
	m.mu.RLock()
	live := make([]*liveGame, 0, len(m.games))
	for _, lg := range m.games {
		live = append(live, lg)
	}
	m.mu.RUnlock()
	for _, lg := range live {
		lg.mu.Lock()
		g := lg.g
		lg.mu.Unlock()
		switch {
		case (g.State == GameWaiting || g.State == GamePreGame) && now.Sub(g.StateAt) > m.deps.PreGameLimit:
			todo = append(todo, stale{g.GuildID, g.ID, "Game setup timed out."})
		case g.State == GameInGame && m.deps.MaxInGame > 0 && now.Sub(g.StartedAt) > m.deps.MaxInGame:
			todo = append(todo, stale{g.GuildID, g.ID, "The game ran past the maximum match length."})
		}
	}
	n := 0
	for _, t := range todo {
		if m.Void(actorSystem, t.guild, t.id, t.reason, VoidOptions{}) == nil {
			n++
		}
	}
	return n
}

// StartSweeper runs Sweep every interval (default a minute) until ctx ends.
func (m *GameManager) StartSweeper(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							m.logf("sweep panic: %v", r)
						}
					}()
					m.Sweep()
				}()
			}
		}
	}()
}

// AddStats adds tracked performance to a player of a live game (a tracker or
// staff feeding kills/deaths/beds); it feeds the season counters and the MVP pick.
func (m *GameManager) AddStats(id uint, userID string, d GamePlayer) error {
	lg := m.live(id)
	if lg == nil {
		return ErrGameNotFound
	}
	lg.mu.Lock()
	defer lg.mu.Unlock()
	for i := range lg.g.Players {
		p := &lg.g.Players[i]
		if p.UserID != userID {
			continue
		}
		p.Kills += d.Kills
		p.Deaths += d.Deaths
		p.Assists += d.Assists
		p.FinalKills += d.FinalKills
		p.BedsDestroyed += d.BedsDestroyed
		p.BedsLost += d.BedsLost
		p.FirstBloods += d.FirstBloods
		for id, n := range d.Custom {
			if p.Custom == nil {
				p.Custom = map[string]float64{}
			}
			p.Custom[id] += n
		}
		return nil
	}
	return ErrNotParticipant
}

// ============================================================================
// Lookups for commands
// ============================================================================

// Record loads a game's database row by its per-season game number.
func (m *GameManager) Record(guildID string, number int) (*models.GamesModel, error) {
	rec, err := m.api().GameByNumber(guildID, number)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrGameNotFound
		}
		return nil, err
	}
	return rec, nil
}

// RecordByID loads a game's database row by ID.
func (m *GameManager) RecordByID(guildID string, id uint) (*models.GamesModel, error) {
	return m.storedGame(guildID, id)
}

// Recent lists the newest games with any of the statuses (all statuses when none
// are given), newest first, at most limit.
func (m *GameManager) Recent(guildID string, limit int, statuses ...string) ([]models.GamesModel, error) {
	if len(statuses) == 0 {
		statuses = []string{"waiting", "in_progress", "completed", "voided", "cancelled"}
	}
	var out []models.GamesModel
	for _, st := range statuses {
		rows, err := m.api().RecentGames(guildID, st, limit)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	slices.SortFunc(out, func(a, b models.GamesModel) int { return int(b.ID) - int(a.ID) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Rows returns a game's participant rows.
func (m *GameManager) Rows(guildID string, id uint) ([]models.GamePlayersModel, error) {
	return m.api().Participants(guildID, id)
}

// UserOf maps a database player ID to its Discord ID.
func (m *GameManager) UserOf(guildID string, playerID uint) (string, error) {
	return m.api().UserID(guildID, playerID)
}

// ByNumber finds a live game by its per-season number.
func (m *GameManager) ByNumber(guildID string, number int) (Game, bool) {
	for _, g := range m.GuildGames(guildID) {
		if g.Number == number {
			return g, true
		}
	}
	return Game{}, false
}

// FindByNumber finds a live game or, for staff recovery actions, a stored game
// in the active season.
func (m *GameManager) FindByNumber(guildID string, number int) (Game, bool, error) {
	if game, ok := m.ByNumber(guildID, number); ok {
		return game, true, nil
	}
	rec, err := m.api().GameByNumber(guildID, number)
	if err != nil {
		if isNotFound(err) {
			return Game{}, false, nil
		}
		return Game{}, false, err
	}
	return Game{ID: rec.ID, Number: rec.GameNumber, GuildID: guildID, SeasonID: rec.SeasonID}, true, nil
}

func isNotFound(err error) bool { return sdk.IsNotFound(err) }

// ============================================================================
// Team Formation
// ============================================================================

// Team formation for a match: automatic assignment (random / ELO-balanced,
// party-aware) and the captain draft. Everything here is pure: players are
// Discord user IDs, ratings come in as a map, randomness comes from a
// *rand.Rand so tests can pin it. The old bot shuffled with
// sort(() => random - 0.5), which is a biased shuffle; this uses real
// Fisher-Yates via rand.Shuffle, and a textbook snake instead of the old
// direction-flipping loop.

var (
	ErrBadRoster    = errors.New("roster does not fit the team layout")
	ErrNotYourTurn  = errors.New("it is not that captain's turn")
	ErrNotInPool    = errors.New("player is not available to pick")
	ErrDraftOver    = errors.New("the draft is complete")
	ErrNoDraftYet   = errors.New("no draft is running")
	ErrNotACaptain  = errors.New("not a captain")
	ErrDraftTooFew  = errors.New("not enough players for a draft")
	teamLetterStrip = strings.NewReplacer("team", "", " ", "", "-", "", "_", "")
)

// TeamName is the display name of team i: "Team A", "Team B", ...
func TeamName(i int) string { return "Team " + string(rune('A'+i)) }

// TeamIndex resolves user input ("A", "team b", "Team-C") to a team index in
// [0, teamCount), or -1.
func TeamIndex(input string, teamCount int) int {
	s := teamLetterStrip.Replace(strings.ToLower(strings.TrimSpace(input)))
	if len(s) != 1 || s[0] < 'a' {
		return -1
	}
	if i := int(s[0] - 'a'); i < teamCount {
		return i
	}
	return -1
}

func rngOr(r *rand.Rand) *rand.Rand {
	if r != nil {
		return r
	}
	return rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
}

// ============================================================================
// Automatic assignment
// ============================================================================

// TeamOptions is defined in package types (games.go).
type TeamOptions = types.TeamOptions

// AssignTeams splits players into o.Teams teams of o.PerTeam. Parties that fit
// stay together (each goes on the team with the lowest total rating that still
// has room); solos then fill the rest, ELO-balanced with probability
// 1 - randomness, otherwise randomly. Without parties it snake-drafts a
// noise-perturbed rating order (randomness 100 = a plain shuffle).
func AssignTeams(players []string, o TeamOptions) ([][]string, error) {
	if o.Teams < 2 || o.PerTeam < 1 || len(players) != o.Teams*o.PerTeam {
		return nil, ErrBadRoster
	}
	r := rngOr(o.Rand)
	rnd := float64(min(max(o.Randomness, 0), 100)) / 100
	rating := func(id string) int { return o.Ratings[id] }

	inRoster := make(map[string]bool, len(players))
	for _, id := range players {
		inRoster[id] = true
	}
	inGroup := map[string]bool{}
	var groups [][]string
	for _, pg := range o.Parties {
		var g []string
		for _, id := range pg.Members {
			if inRoster[id] && !inGroup[id] {
				g = append(g, id)
			}
		}
		if len(g) >= 2 {
			for _, id := range g {
				inGroup[id] = true
			}
			groups = append(groups, g)
		}
	}

	teams := make([][]string, o.Teams)
	if len(groups) == 0 {
		ordered := orderByNoise(players, rating, rnd, r)
		if rnd >= 1 {
			for i := range teams {
				teams[i] = slices.Clone(ordered[i*o.PerTeam : (i+1)*o.PerTeam])
			}
			return teams, nil
		}
		for i, id := range ordered { // snake: A B C C B A A B C ...
			round, pos := i/o.Teams, i%o.Teams
			t := pos
			if round%2 == 1 {
				t = o.Teams - 1 - pos
			}
			teams[t] = append(teams[t], id)
		}
		return teams, nil
	}

	teamElo := func(t int) int {
		sum := 0
		for _, id := range teams[t] {
			sum += rating(id)
		}
		return sum
	}
	// lowest picks the team with room for n that has the lowest total rating
	// (ties: fewer players, then earlier), or -1.
	lowest := func(n int) int {
		best := -1
		for t := range teams {
			if o.PerTeam-len(teams[t]) < n {
				continue
			}
			if best < 0 || teamElo(t) < teamElo(best) || (teamElo(t) == teamElo(best) && len(teams[t]) < len(teams[best])) {
				best = t
			}
		}
		return best
	}

	groupElo := func(g []string) float64 {
		elos := make([]int, len(g))
		for i, id := range g {
			elos[i] = rating(id)
		}
		return AggregateElo(elos, o.Method, elos[0])
	}
	if o.BalanceParties {
		sort.SliceStable(groups, func(i, j int) bool { return groupElo(groups[i]) > groupElo(groups[j]) })
	} else {
		r.Shuffle(len(groups), func(i, j int) { groups[i], groups[j] = groups[j], groups[i] })
	}

	var solos []string
	for _, id := range players {
		if !inGroup[id] {
			solos = append(solos, id)
		}
	}
	for _, g := range groups {
		if t := lowest(len(g)); t >= 0 {
			teams[t] = append(teams[t], g...)
			continue
		}
		if !o.AllowSplit { // can't stay together: they fill in as solos
			solos = append(solos, g...)
			continue
		}
		for _, id := range g {
			if t := lowest(1); t >= 0 {
				teams[t] = append(teams[t], id)
			}
		}
	}

	if r.Float64() >= rnd { // balanced fill
		slices.SortStableFunc(solos, func(a, b string) int { return rating(b) - rating(a) })
		for _, id := range solos {
			if t := lowest(1); t >= 0 {
				teams[t] = append(teams[t], id)
			}
		}
	} else {
		r.Shuffle(len(solos), func(i, j int) { solos[i], solos[j] = solos[j], solos[i] })
		for _, id := range solos {
			var open []int
			for t := range teams {
				if len(teams[t]) < o.PerTeam {
					open = append(open, t)
				}
			}
			if len(open) > 0 {
				t := open[r.IntN(len(open))]
				teams[t] = append(teams[t], id)
			}
		}
	}
	return teams, nil
}

// orderByNoise sorts players by rating plus noise scaled by rnd (0 = exact
// rating order, 1 = a uniform shuffle).
func orderByNoise(players []string, rating func(string) int, rnd float64, r *rand.Rand) []string {
	out := slices.Clone(players)
	if rnd >= 1 {
		r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		return out
	}
	score := make(map[string]float64, len(out))
	for _, id := range out {
		score[id] = float64(rating(id)) + (r.Float64()-0.5)*rnd*400
	}
	sort.SliceStable(out, func(i, j int) bool {
		if score[out[i]] != score[out[j]] {
			return score[out[i]] > score[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// ============================================================================
// Captain draft
// ============================================================================

// DraftConfig is defined in package types (games.go).
type DraftConfig = types.DraftConfig

// Draft is a captain draft in progress. Teams[i][0] is team i's captain.
type Draft struct {
	Teams   [][]string
	PerTeam int
	Type    string // effective picking type
	pool    []string
	ratings map[string]int
	partyOf map[string][]string
	order   []int // team indexes, weakest team first
	auto    bool
	rng     *rand.Rand
}

// NewDraft elects captains and returns the draft. Captains come from the top
// 2*teams players by rating: the top `teams` of them with probability
// TopEloChance, otherwise a random `teams` of them. With OneCaptainPerParty two
// captains never share a party.
func NewDraft(players []string, teams, perTeam int, ratings map[string]int, parties []PartyGroup, cfg DraftConfig, r *rand.Rand) (*Draft, error) {
	if teams < 2 || perTeam < 1 || len(players) != teams*perTeam {
		return nil, ErrBadRoster
	}
	if len(players) < 2 {
		return nil, ErrDraftTooFew
	}
	r = rngOr(r)
	d := &Draft{PerTeam: perTeam, ratings: ratings, partyOf: map[string][]string{}, auto: cfg.AutoAssignParties, rng: r}
	roster := map[string]bool{}
	for _, id := range players {
		roster[id] = true
	}
	hasGroups := false
	for _, pg := range parties {
		var g []string
		for _, id := range pg.Members {
			if roster[id] {
				g = append(g, id)
			}
		}
		if len(g) >= 2 {
			hasGroups = true
			for _, id := range g {
				d.partyOf[id] = g
			}
		}
	}

	byElo := slices.Clone(players)
	sort.SliceStable(byElo, func(i, j int) bool {
		if ratings[byElo[i]] != ratings[byElo[j]] {
			return ratings[byElo[i]] > ratings[byElo[j]]
		}
		return byElo[i] < byElo[j]
	})
	top := slices.Clone(byElo[:min(teams*2, len(byElo))])
	chance := cfg.TopEloChance
	if chance > 1 { // tolerate a 0-100 percentage
		chance /= 100
	}
	chance = min(max(chance, 0), 1)
	if r.Float64() >= chance {
		r.Shuffle(len(top), func(i, j int) { top[i], top[j] = top[j], top[i] })
	}
	captains := slices.Clone(top[:teams])

	if cfg.OneCaptainPerParty && hasGroups {
		sharing := func(a, b string) bool { return slices.Contains(d.partyOf[a], b) }
		for i := 0; i < len(captains); i++ {
			clash := false
			for j := 0; j < i; j++ {
				clash = clash || sharing(captains[i], captains[j])
			}
			if !clash {
				continue
			}
			for _, cand := range byElo { // best remaining player outside every captain's party
				if slices.Contains(captains, cand) {
					continue
				}
				ok := true
				for k, c := range captains {
					ok = ok && (k == i || !sharing(cand, c))
				}
				if ok {
					captains[i] = cand
					break
				}
			}
		}
	}
	r.Shuffle(len(captains), func(i, j int) { captains[i], captains[j] = captains[j], captains[i] })

	d.Teams = make([][]string, teams)
	for i, c := range captains {
		d.Teams[i] = []string{c}
	}
	for _, id := range players {
		if !slices.Contains(captains, id) {
			d.pool = append(d.pool, id)
		}
	}
	captainInParty := false
	for _, c := range captains {
		captainInParty = captainInParty || len(d.partyOf[c]) > 0
	}
	d.Type = cfg.PickingType
	if d.Type == "" {
		d.Type = "one_per_person"
	}
	if hasGroups && captainInParty && len(players) >= cfg.AlternatingMinPlayers && cfg.AlternatingMinPlayers > 0 {
		d.Type = "alternating"
	}
	if d.auto {
		for t := range d.Teams {
			d.pull(t, d.Teams[t][0])
		}
	}
	d.order = make([]int, teams)
	for i := range d.order {
		d.order[i] = i
	}
	sort.SliceStable(d.order, func(a, b int) bool { return d.teamElo(d.order[a]) < d.teamElo(d.order[b]) })
	return d, nil
}

func (d *Draft) teamElo(t int) int {
	sum := 0
	for _, id := range d.Teams[t] {
		sum += d.ratings[id]
	}
	return sum
}

// Pool is who can still be picked.
func (d *Draft) Pool() []string { return slices.Clone(d.pool) }

// Captains lists each team's captain, by team index.
func (d *Draft) Captains() []string {
	out := make([]string, len(d.Teams))
	for i, t := range d.Teams {
		out[i] = t[0]
	}
	return out
}

// pull moves id's party mates from the pool onto team t while it has room.
func (d *Draft) pull(t int, id string) {
	for _, mate := range d.partyOf[id] {
		if len(d.Teams[t]) >= d.PerTeam {
			return
		}
		if i := slices.Index(d.pool, mate); i >= 0 {
			d.pool = slices.Delete(d.pool, i, i+1)
			d.Teams[t] = append(d.Teams[t], mate)
		}
	}
}

func (d *Draft) open() []int {
	var open []int
	for t := range d.Teams {
		if len(d.Teams[t]) < d.PerTeam {
			open = append(open, t)
		}
	}
	return open
}

// Done reports whether every team is full.
func (d *Draft) Done() bool { return len(d.open()) == 0 }

// alternatingQuota is how many picks the team at weakness rank i makes in the
// 4-captain alternating pattern; the last team is filled automatically.
var alternatingQuota = [...]int{1, 2, 2, 0}

// Turn returns the team that picks next, or -1 when the draft is over. In the
// 4-team alternating pattern the quota-0 team is filled from the pool as soon
// as the other teams have made their picks.
func (d *Draft) Turn() int {
	for {
		open := d.open()
		if len(open) == 0 {
			return -1
		}
		isOpen := func(t int) bool { return slices.Contains(d.open(), t) }

		if d.Type == "alternating" && len(d.Teams) == 4 {
			for rank, t := range d.order {
				if q := alternatingQuota[rank]; q > 0 && len(d.Teams[t])-1 < q && isOpen(t) {
					return t
				}
			}
			filled := false
			for rank, t := range d.order {
				for alternatingQuota[rank] == 0 && isOpen(t) && len(d.pool) > 0 {
					d.take(t, d.pool[d.rng.IntN(len(d.pool))])
					filled = true
				}
			}
			if filled {
				continue
			}
		}
		if len(open) == 1 {
			return open[0]
		}
		if d.Type == "alternating" && len(d.Teams) == 2 {
			lo, hi := d.order[0], d.order[1]
			pick := hi
			switch ls, hs := len(d.Teams[lo]), len(d.Teams[hi]); {
			case ls == hs && ls%2 != 0, ls < hs:
				pick = lo
			}
			if isOpen(pick) {
				return pick
			}
			return open[0]
		}
		best := open[0]
		for _, t := range open[1:] {
			if len(d.Teams[t]) < len(d.Teams[best]) {
				best = t
			}
		}
		return best
	}
}

func (d *Draft) take(t int, id string) {
	if i := slices.Index(d.pool, id); i >= 0 {
		d.pool = slices.Delete(d.pool, i, i+1)
	}
	d.Teams[t] = append(d.Teams[t], id)
	if d.auto {
		d.pull(t, id)
	}
}

// Pick makes captain pick player for their team. It must be that team's turn.
func (d *Draft) Pick(captain, player string) (team int, err error) {
	turn := d.Turn()
	if turn < 0 {
		return -1, ErrDraftOver
	}
	t := slices.IndexFunc(d.Teams, func(tm []string) bool { return tm[0] == captain })
	if t < 0 {
		return -1, ErrNotACaptain
	}
	if t != turn {
		return -1, ErrNotYourTurn
	}
	if !slices.Contains(d.pool, player) {
		return -1, ErrNotInPool
	}
	d.take(t, player)
	return t, nil
}

// AutoPick makes the current turn's pick at random (the captain timed out).
func (d *Draft) AutoPick() (captain, player string, err error) {
	t := d.Turn()
	if t < 0 {
		return "", "", ErrDraftOver
	}
	if len(d.pool) == 0 {
		return "", "", ErrNotInPool
	}
	player = d.pool[d.rng.IntN(len(d.pool))]
	d.take(t, player)
	return d.Teams[t][0], player, nil
}

const (
	hostGameIDPrefix  = "hostgame1|"
	hostGameMaxTeams  = 4
	hostGameSessionTT = 20 * time.Minute
)

// The panel runs as four steps: mode → options → teams → review.
const (
	pageMode    = "mode"
	pageOptions = "options"
	pageTeams   = "teams"
	pageReview  = "review"
)

var hostGamePages = []string{pageMode, pageOptions, pageTeams, pageReview}

type hostMode struct {
	Mode       types.Mode
	ChannelID  string // a queue channel bound to the mode
	CategoryID string
}

type HostSession struct {
	ID, GuildID, UserID string
	Page                string
	Modes               []hostMode
	ModeIdx             int
	TeamCount           int
	Ranked, Stats       bool
	UseBot              bool
	Active              int
	Teams               [][]string
	Voice               []string
	Notice              string
	Created             time.Time
}

var hostSessions = struct {
	sync.Mutex
	byID map[string]*HostSession
}{byID: map[string]*HostSession{}}

func NewHostSession(s *discordgo.Session, guildID, userID string) (*HostSession, *discordgo.MessageEmbed) {
	snap, err := Queues.Snapshot(guildID)
	if err != nil || snap == nil {
		return nil, HostGameErrorEmbed(errors.New("the queue configuration could not be loaded"))
	}

	// One entry per mode that an enabled queue channel is bound to: that queue supplies the game's category.
	channels := make([]string, 0, len(snap.Bindings))
	for id := range snap.Bindings {
		channels = append(channels, id)
	}
	sort.Strings(channels)
	seen := map[string]bool{}
	var modes []hostMode
	for _, id := range channels {
		b := snap.Bindings[id]
		key := strings.ToLower(b.Mode.Name)
		if seen[key] || len(modes) >= 25 {
			continue
		}
		seen[key] = true
		category := strings.TrimSpace(b.Queue.CategoryID)
		if category == "" {
			if ch, err := s.State.Channel(id); err == nil {
				category = ch.ParentID
			}
		}
		if category == "" {
			continue // nowhere to create the game's channels
		}
		modes = append(modes, hostMode{Mode: b.Mode, ChannelID: id, CategoryID: category})
	}
	sort.Slice(modes, func(a, b int) bool { return modes[a].Mode.Name < modes[b].Mode.Name })
	if len(modes) == 0 {
		return nil, HostGameErrorEmbed(errors.New("no game mode is attached to an enabled queue, so there is nothing to host. Configure a queue first"))
	}

	session := &HostSession{
		ID: strconv.FormatInt(time.Now().UnixNano(), 36), GuildID: guildID, UserID: userID, Page: pageMode, Modes: modes,
		TeamCount: 2, Ranked: modes[0].Mode.IsEloGainEnabled, Stats: modes[0].Mode.IsStatsTrackingEnabled, UseBot: true,
		Teams: make([][]string, hostGameMaxTeams), Voice: make([]string, hostGameMaxTeams), Created: time.Now(),
	}
	if len(modes) == 1 {
		session.Page = pageOptions
	}

	hostSessions.Lock()
	for id, old := range hostSessions.byID {
		if time.Since(old.Created) > hostGameSessionTT {
			delete(hostSessions.byID, id)
		}
	}
	hostSessions.byID[session.ID] = session
	hostSessions.Unlock()
	return session, nil
}

func (h *HostSession) customID(action string, arg ...string) string {
	return strings.Join(append([]string{strings.TrimSuffix(hostGameIDPrefix, "|"), h.ID, action}, arg...), "|")
}

func (h *HostSession) Render(s *discordgo.Session) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	step := slices.Index(hostGamePages, h.Page) + 1
	embed := Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Host Game • %s (%d/%d)", hostGamePageTitle(h.Page), step, len(hostGamePages)),
		Description: h.describe(),
		FooterText:  "Ranked Bedwars • this panel expires after 20 minutes",
	})
	if h.Notice != "" {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: "Notice", Value: h.Notice})
	}

	switch h.Page {
	case pageMode:
		return embed, []discordgo.MessageComponent{h.modeMenu(), h.buttons(false, h.ModeIdx >= 0)}
	case pageOptions:
		return embed, []discordgo.MessageComponent{h.optionButtons(), h.teamCountMenu(), h.buttons(len(h.Modes) > 1, true)}
	case pageTeams:
		return embed, []discordgo.MessageComponent{h.teamMenu(), h.playerMenu(), h.voiceMenu(), h.buttons(true, true)}
	default:
		return embed, []discordgo.MessageComponent{h.reviewButtons()}
	}
}

func hostGamePageTitle(page string) string {
	switch page {
	case pageMode:
		return "Game Mode"
	case pageOptions:
		return "Options"
	case pageTeams:
		return "Teams & Voice"
	}
	return "Review & Start"
}

func (h *HostSession) describe() string {
	mode := h.Modes[h.ModeIdx].Mode
	switch h.Page {
	case pageMode:
		return "Pick the mode this game is played in. Its queue decides where the game's channels are created."
	case pageOptions:
		return fmt.Sprintf("**Mode:** `%s`\n\nChoose how many teams play, whether the match counts toward ELO and season stats, and whether the hosting bot runs it. Without the bot, staff finish the game with `/host finish`.", mode.Name)
	case pageTeams:
		return fmt.Sprintf("Pick a team, then choose its players and, optionally, an existing voice channel for it. Every player must be registered.\n\n%s", h.teamLines())
	}
	return fmt.Sprintf("**Mode:** `%s`\n**Ranks:** %s · **Stats:** %s · **Bot:** %s\n\n%s", mode.Name, HostOnOff(h.Ranked), HostOnOff(h.Stats), HostOnOff(h.UseBot), h.teamLines())
}

func HostOnOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func (h *HostSession) teamLines() string {
	var lines []string
	for t := 0; t < h.TeamCount; t++ {
		line := fmt.Sprintf("**%s** (%d)", TeamName(t), len(h.Teams[t]))
		if len(h.Teams[t]) > 0 {
			line += ": <@" + strings.Join(h.Teams[t], "> <@") + ">"
		}
		if h.Voice[t] != "" {
			line += " · <#" + h.Voice[t] + ">"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (h *HostSession) modeMenu() discordgo.MessageComponent {
	options := make([]discordgo.SelectMenuOption, len(h.Modes))
	for i, m := range h.Modes {
		options[i] = discordgo.SelectMenuOption{Label: m.Mode.Name, Value: strconv.Itoa(i), Default: i == h.ModeIdx}
	}
	return &discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.SelectMenu{CustomID: h.customID("mode"), Placeholder: "Game mode", Options: options}}}
}

func (h *HostSession) optionButtons() discordgo.MessageComponent {
	style := func(on bool) discordgo.ButtonStyle {
		if on {
			return discordgo.SuccessButton
		}
		return discordgo.SecondaryButton
	}
	return &discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		&discordgo.Button{Label: "Ranks: " + HostOnOff(h.Ranked), Style: style(h.Ranked), CustomID: h.customID("ranked")},
		&discordgo.Button{Label: "Stats: " + HostOnOff(h.Stats), Style: style(h.Stats), CustomID: h.customID("stats")},
		&discordgo.Button{Label: "Bot: " + HostOnOff(h.UseBot), Style: style(h.UseBot), CustomID: h.customID("bot")},
	}}
}

func (h *HostSession) teamCountMenu() discordgo.MessageComponent {
	var options []discordgo.SelectMenuOption
	for n := 2; n <= hostGameMaxTeams; n++ {
		options = append(options, discordgo.SelectMenuOption{Label: fmt.Sprintf("%d teams", n), Value: strconv.Itoa(n), Default: n == h.TeamCount})
	}
	return &discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.SelectMenu{CustomID: h.customID("count"), Placeholder: "Number of teams", Options: options}}}
}

func (h *HostSession) teamMenu() discordgo.MessageComponent {
	var options []discordgo.SelectMenuOption
	for t := 0; t < h.TeamCount; t++ {
		options = append(options, discordgo.SelectMenuOption{Label: TeamName(t), Value: strconv.Itoa(t), Default: t == h.Active, Description: fmt.Sprintf("%d player(s)", len(h.Teams[t]))})
	}
	return &discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.SelectMenu{CustomID: h.customID("team"), Placeholder: "Team to edit", Options: options}}}
}

func (h *HostSession) playerMenu() discordgo.MessageComponent {
	zero := 0
	menu := &discordgo.SelectMenu{
		MenuType: discordgo.UserSelectMenu, CustomID: h.customID("players"), Placeholder: "Players of " + TeamName(h.Active),
		MinValues: &zero, MaxValues: 25,
	}
	for _, id := range h.Teams[h.Active] {
		menu.DefaultValues = append(menu.DefaultValues, discordgo.SelectMenuDefaultValue{ID: id, Type: discordgo.SelectMenuDefaultValueUser})
	}
	return &discordgo.ActionsRow{Components: []discordgo.MessageComponent{menu}}
}

func (h *HostSession) voiceMenu() discordgo.MessageComponent {
	zero := 0
	menu := &discordgo.SelectMenu{
		MenuType: discordgo.ChannelSelectMenu, CustomID: h.customID("voice"), Placeholder: "Voice channel for " + TeamName(h.Active) + " (optional: one is created)",
		MinValues: &zero, MaxValues: 1, ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildVoice},
	}
	if id := h.Voice[h.Active]; id != "" {
		menu.DefaultValues = []discordgo.SelectMenuDefaultValue{{ID: id, Type: discordgo.SelectMenuDefaultValueChannel}}
	}
	return &discordgo.ActionsRow{Components: []discordgo.MessageComponent{menu}}
}

func (h *HostSession) buttons(back, next bool) discordgo.MessageComponent {
	row := &discordgo.ActionsRow{}
	row.Components = append(row.Components, &discordgo.Button{Label: "Back", Style: discordgo.SecondaryButton, Disabled: !back, CustomID: h.customID("back")})
	row.Components = append(row.Components, &discordgo.Button{Label: "Next", Style: discordgo.PrimaryButton, Disabled: !next, CustomID: h.customID("next")})
	row.Components = append(row.Components, &discordgo.Button{Label: "Cancel", Style: discordgo.DangerButton, CustomID: h.customID("cancel")})
	return row
}

func (h *HostSession) reviewButtons() discordgo.MessageComponent {
	return &discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		&discordgo.Button{Label: "Back", Style: discordgo.SecondaryButton, CustomID: h.customID("back")},
		&discordgo.Button{Label: "Start game", Style: discordgo.SuccessButton, CustomID: h.customID("start")},
		&discordgo.Button{Label: "Cancel", Style: discordgo.DangerButton, CustomID: h.customID("cancel")},
	}}
}

func HandleHostGameComponent(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, strings.TrimSuffix(hostGameIDPrefix, "|")+"|") {
		return false
	}
	parts := strings.Split(customID, "|")
	if len(parts) < 3 {
		return true
	}

	hostSessions.Lock()
	h := hostSessions.byID[parts[1]]
	hostSessions.Unlock()
	user := InteractionUserID(i)
	switch {
	case h == nil || time.Since(h.Created) > hostGameSessionTT:
		respondHostGame(s, i, HostGameErrorEmbed(errors.New("this panel has expired, run `/host create` again")), nil)
		return true
	case user != h.UserID:
		_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "Only the staff member who opened this panel can use it.", Flags: discordgo.MessageFlagsEphemeral}})
		return true
	}

	values := i.MessageComponentData().Values
	h.Notice = ""
	switch parts[2] {
	case "mode":
		if len(values) == 1 {
			h.ModeIdx, _ = strconv.Atoi(values[0])
			h.Ranked, h.Stats = h.Modes[h.ModeIdx].Mode.IsEloGainEnabled, h.Modes[h.ModeIdx].Mode.IsStatsTrackingEnabled
		}
	case "ranked":
		h.Ranked = !h.Ranked
	case "stats":
		h.Stats = !h.Stats
	case "bot":
		h.UseBot = !h.UseBot
	case "count":
		if len(values) == 1 {
			h.TeamCount, _ = strconv.Atoi(values[0])
			h.Active = min(h.Active, h.TeamCount-1)
		}
	case "team":
		if len(values) == 1 {
			h.Active, _ = strconv.Atoi(values[0])
		}
	case "players":
		h.setPlayers(s, values)
	case "voice":
		h.Voice[h.Active] = ""
		if len(values) == 1 {
			h.Voice[h.Active] = values[0]
		}
	case "back":
		h.Page = hostGamePages[max(0, slices.Index(hostGamePages, h.Page)-1)]
		if h.Page == pageMode && len(h.Modes) == 1 {
			h.Page = pageOptions
		}
	case "next":
		h.Page = hostGamePages[min(len(hostGamePages)-1, slices.Index(hostGamePages, h.Page)+1)]
		if h.Page == pageReview {
			h.Notice = h.problem()
		}
	case "cancel":
		hostSessions.Lock()
		delete(hostSessions.byID, h.ID)
		hostSessions.Unlock()
		respondHostGame(s, i, HostGameDoneEmbed("Setup cancelled."), nil)
		return true
	case "start":
		h.start(s, i)
		return true
	}
	embed, components := h.Render(s)
	respondHostGame(s, i, embed, components)
	return true
}

func respondHostGame(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) {
	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Components: components},
	})
}

// setPlayers sets the active team's players. Bots are dropped and a player can only be on one team.
func (h *HostSession) setPlayers(s *discordgo.Session, ids []string) {
	var kept []string
	for _, id := range ids {
		if u, err := s.User(id); err == nil && u.Bot {
			h.Notice = "Bots can't play."
			continue
		}
		kept = append(kept, id)
	}
	for t := range h.Teams {
		if t == h.Active {
			continue
		}
		h.Teams[t] = slices.DeleteFunc(h.Teams[t], func(id string) bool { return slices.Contains(kept, id) })
	}
	h.Teams[h.Active] = kept
}

// problem is why the game can't start yet, or "".
func (h *HostSession) problem() string {
	for t := 0; t < h.TeamCount; t++ {
		if len(h.Teams[t]) == 0 {
			return fmt.Sprintf("%s has no players yet.", TeamName(t))
		}
	}
	return ""
}

func (h *HostSession) start(s *discordgo.Session, i *discordgo.InteractionCreate) {
	fail := func(notice string) {
		h.Notice = notice
		embed, components := h.Render(s)
		respondHostGame(s, i, embed, components)
	}
	if p := h.problem(); p != "" {
		fail(p)
		return
	}

	chosen := h.Modes[h.ModeIdx]
	teams := make([][]QueueMember, h.TeamCount)
	for t := 0; t < h.TeamCount; t++ {
		for _, id := range h.Teams[t] {
			name := id
			if m, err := s.GuildMember(h.GuildID, id); err == nil {
				name = m.Nick
				if name == "" && m.User != nil {
					name = m.User.Username
				}
			}
			teams[t] = append(teams[t], QueueMember{UserID: id, Username: name})
		}
	}

	game, err := Games.StartCustom(CustomSpec{
		GuildID: h.GuildID, HostedBy: h.UserID, QueueChannelID: chosen.ChannelID, CategoryID: chosen.CategoryID, Mode: chosen.Mode,
		Ranked: h.Ranked, Stats: h.Stats, UseBot: h.UseBot, Teams: teams, Voice: slices.Clone(h.Voice[:h.TeamCount]),
	})
	if err != nil {
		var busy *BusyError
		var unregistered *NotRegisteredError
		switch {
		case errors.As(err, &busy):
			fail(fmt.Sprintf("<@%s> is already in a game.", busy.UserID))
		case errors.As(err, &unregistered):
			fail("These players aren't registered: <@" + strings.Join(unregistered.UserIDs, "> <@") + ">.")
		case errors.Is(err, ErrNoSeason):
			fail("This server has no active season.")
		default:
			fail("The game could not be started: " + err.Error())
		}
		return
	}

	hostSessions.Lock()
	delete(hostSessions.byID, h.ID)
	hostSessions.Unlock()
	respondHostGame(s, i, HostGameDoneEmbed(fmt.Sprintf("Game **#%d** is being set up in `%s`. Its channels will appear in a moment.", game.Number, chosen.Mode.Name)), nil)
}

// ============================================================================
// Team formations
// ============================================================================

// A formation is one way of turning a queue's players into teams. A guild defines as many as it likes (the dashboard's
// Queue Config → Team formations) and each mode picks one by its ID. A mode that picks none keeps the older behaviour:
// captain modes use the guild's captain flow, every other mode its standard flow.
//
// kind is "balanced" (ELO-balanced teams, with a little randomness if wanted), "random" (a plain shuffle) or
// "captains" (two captains draft the rest). A setting a formation leaves out falls back to the guild's flow for it.

const (
	FormationBalanced = "balanced"
	FormationRandom   = "random"
	FormationCaptains = "captains"
)

// Formation is defined in package types (games.go).
type Formation = types.Formation

// formationOf is the formation the mode picked, if it picked one that still exists.
func (f FlowSettings) formationOf(m types.Mode) (Formation, bool) {
	id := strings.TrimSpace(m.FormationID)
	if id == "" {
		return Formation{}, false
	}
	for _, x := range f.Formations {
		if x.ID == id {
			return x, true
		}
	}
	return Formation{}, false
}

// IsCaptain reports whether games of the mode are drafted by captains.
func (f FlowSettings) IsCaptain(m types.Mode) bool {
	if x, ok := f.formationOf(m); ok {
		return x.Kind == FormationCaptains
	}
	return isCaptainMode(m)
}

// StandardFor is the automatic team assignment settings for the mode: its formation's on top of the guild's standard flow.
func (f FlowSettings) StandardFor(m types.Mode) StandardFlow {
	s := f.Standard
	x, ok := f.formationOf(m)
	if !ok && isRandomMode(m) {
		s.IsEnabled = true
		s.RandomnessPercent = 100
		return s
	}
	if !ok || x.Kind == FormationCaptains {
		return s
	}
	s.IsEnabled = true
	s.RandomnessPercent = 0
	if x.Kind == FormationRandom {
		s.RandomnessPercent = 100
	}
	if x.RandomnessPercent != nil && x.Kind != FormationRandom {
		s.RandomnessPercent = min(100, max(0, *x.RandomnessPercent))
	}
	if x.KeepPartyTogether != nil {
		s.KeepPartyTogether = *x.KeepPartyTogether
	}
	if x.BalancePartyGroups != nil {
		s.BalancePartyGroups = *x.BalancePartyGroups
	}
	if x.AllowSplitParties != nil {
		s.AllowSplitParties = *x.AllowSplitParties
	}
	if x.MaxPartySize != nil {
		s.MaxPartySize = x.MaxPartySize
	}
	return s
}

// CaptainFor is the draft settings for the mode: its formation's on top of the guild's captain flow.
func (f FlowSettings) CaptainFor(m types.Mode) CaptainFlow {
	c := f.Captain
	x, ok := f.formationOf(m)
	if !ok || x.Kind != FormationCaptains {
		return c
	}
	c.IsEnabled = true
	if x.PickTimeoutSeconds != nil {
		c.PickTimeoutSeconds = *x.PickTimeoutSeconds
	}
	if x.TopEloCaptainChance != nil {
		c.TopEloCaptainChance = *x.TopEloCaptainChance
	}
	if x.AutoAssignParties != nil {
		c.AutoAssignParties = *x.AutoAssignParties
	}
	if x.OneCaptainPerParty != nil {
		c.OneCaptainPerParty = *x.OneCaptainPerParty
	}
	if x.DefaultPickingType != "" {
		c.DefaultPickingType = x.DefaultPickingType
	}
	if x.AlternatingMinPlayers != nil {
		c.AlternatingMinPlayers = *x.AlternatingMinPlayers
	}
	if x.MaxPartySize != nil {
		c.MaxPartySize = x.MaxPartySize
	}
	return c
}
