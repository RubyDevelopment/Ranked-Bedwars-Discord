package match

import (
	"fmt"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/discord/helpers/queue"
	"ranked-bedwars/src/types"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
)

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
		for _, voice := range ch.Voice {
			base.ForgetVoiceStatus(voice) // the channel is going: nothing more to write there
		}
		if s := session(); s != nil {
			deleteGameChannels(s, ch)
		}
	}
	return create, remove
}

func createGameChannels(s *discordgo.Session, g Game) (GameChannels, error) {
	var queueCfg types.QueueConfig
	if snap, err := queue.Queues.Snapshot(g.GuildID); err == nil && snap != nil {
		if b, ok := snap.Bindings[g.QueueChannelID]; ok {
			queueCfg = b.Queue
		}
	}
	cfg := queueCfg.GameChannels

	// Voice channels (and the text channel in "channel" mode) live in this category.
	category := base.FirstNonEmpty(cfg.CategoryID, queueCfg.CategoryID, g.QueueCategoryID)
	if cfg.IsThread() {
		category = base.FirstNonEmpty(queueCfg.CategoryID, g.QueueCategoryID)
	}

	out := GameChannels{Voice: map[string]string{}, Waiting: strings.TrimSpace(queueCfg.WaitingVCID)}
	overwrites := gameOverwrites(s, g, cfg)
	voiceOW := voiceOverwrites(s, g, cfg)
	cleanup := func() { deleteGameChannels(s, out) }

	// Text: a thread of the chosen channel, or a text channel in the category.
	textName := expandGameName(base.FirstNonEmpty(cfg.TextNameTemplate, defaultTextTemplate), g, "")
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
	template := base.FirstNonEmpty(queueCfg.VoiceTeamTemplate, defaultTeamVoiceTemplate)
	for t := 0; t < teams; t++ {
		name := TeamName(t)
		if g.Custom != nil && t < len(g.Custom.Voice) && g.Custom.Voice[t] != "" {
			out.Voice[name] = g.Custom.Voice[t] // staff picked a voice channel for this team: use it as is
			out.Borrowed = append(out.Borrowed, g.Custom.Voice[t])
			continue
		}
		vc, err := s.GuildChannelCreateComplex(g.GuildID, discordgo.GuildChannelCreateData{
			Name: expandGameName(template, g, name), Type: discordgo.ChannelTypeGuildVoice, ParentID: category, PermissionOverwrites: voiceOW,
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

// Permission bits that make up "seeing" and "using" a game channel of each kind.
const (
	textUseBits  = discordgo.PermissionSendMessages
	voiceUseBits = discordgo.PermissionVoiceConnect | discordgo.PermissionVoiceSpeak
	textSeeBits  = discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory
	voiceSeeBits = discordgo.PermissionViewChannel
)

// gameOverwrites is the permissions of the game chat (see accessOverwrites).
func gameOverwrites(s *discordgo.Session, g Game, cfg types.GameChannelsConfig) []*discordgo.PermissionOverwrite {
	return accessOverwrites(s, g, cfg.Text(), textSeeBits, textUseBits)
}

// voiceOverwrites is the permissions of the team voice channels (see accessOverwrites).
func voiceOverwrites(s *discordgo.Session, g Game, cfg types.GameChannelsConfig) []*discordgo.PermissionOverwrite {
	return accessOverwrites(s, g, cfg.Voice(), voiceSeeBits, voiceUseBits)
}

// accessOverwrites turns a channel's access settings into permission overwrites. The bot and the game's players can
// always see and use the channel. Who else can see it is acc.View (nobody, registered players or everyone) plus
// acc.ViewRoleIDs; those who can see it can use it too only when acc.Use says viewers, otherwise the use bits are
// denied to them. acc.UseRoleIDs (staff) get full access.
func accessOverwrites(s *discordgo.Session, g Game, acc types.ChannelAccess, see, use int64) []*discordgo.PermissionOverwrite {
	var deny int64
	if acc.Use != types.UseViewers {
		deny = use
	}
	// One overwrite per role, however many of the settings name it.
	viewers := map[string]bool{}
	everyone := &discordgo.PermissionOverwrite{ID: g.GuildID, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel}
	switch acc.View {
	case types.ViewEveryone:
		everyone.Allow, everyone.Deny = see, deny
	case types.ViewRegistered:
		if id := helpers.RegisteredRoleID(g.GuildID); id != "" {
			viewers[id] = true
		} else {
			everyone.Allow, everyone.Deny = see, deny
		}
	}
	for _, id := range cleanIDs(acc.ViewRoleIDs) {
		viewers[id] = true
	}
	staff := map[string]bool{}
	for _, id := range cleanIDs(acc.UseRoleIDs) {
		staff[id] = true
	}

	out := []*discordgo.PermissionOverwrite{everyone}
	if s.State != nil && s.State.User != nil {
		out = append(out, &discordgo.PermissionOverwrite{ID: s.State.User.ID, Type: discordgo.PermissionOverwriteTypeMember, Allow: gameChannelAllow | discordgo.PermissionManageChannels})
	}
	for _, p := range g.Players {
		out = append(out, &discordgo.PermissionOverwrite{ID: p.UserID, Type: discordgo.PermissionOverwriteTypeMember, Allow: gameChannelAllow})
	}
	for id := range staff {
		out = append(out, &discordgo.PermissionOverwrite{ID: id, Type: discordgo.PermissionOverwriteTypeRole, Allow: gameChannelAllow})
	}
	for id := range viewers {
		if !staff[id] {
			out = append(out, &discordgo.PermissionOverwrite{ID: id, Type: discordgo.PermissionOverwriteTypeRole, Allow: see, Deny: deny})
		}
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
