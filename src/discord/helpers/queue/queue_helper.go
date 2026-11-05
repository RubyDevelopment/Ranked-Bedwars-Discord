package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/discord/helpers/partymgr"
	"ranked-bedwars/src/discord/helpers/punish"
	"ranked-bedwars/src/types"
	baseutility "ranked-bedwars/src/utility"
)

const (
	QueueEventName = "Queue"

	// Requirement key of the Queue event, assigned on the dashboard's Queue Config page.
	QueueRoleBanned = "queue_banned" // optional: members with this role can't queue (the ban command's bans are checked too)

	partyCmdName   = "party"
	partyAlertsKey = "party_alerts"
)

// queueRestrictionTypes are the active punishment types that block queueing
// (`queue_ban` is stored as "ban", see getPunishmentType).
var queueRestrictionTypes = []string{"ban", "server_ban"}

var (
	queueLogger = baseutility.CreateModuleLogger("QUEUE", "discord")

	// admitted holds "channelID:userID" for members that passed the join checks
	// while sitting in a queue channel; they are the only ones a lobby is built from.
	admittedMu sync.Mutex
	admitted   = map[string]bool{}
	// denied holds "channelID:userID" for members the join checks refused while they sit in a queue channel (the channel's status counts them
	// as ineligible); it is under admittedMu too.
	denied = map[string]bool{}

	// lastLaunch is when each queue channel last launched a game.
	launchMu   sync.Mutex
	lastLaunch = map[string]time.Time{}
)

// launchCooldown is how soon after a launch a release counts as a setup failure.
const launchCooldown = 15 * time.Second

func noteLaunch(channelID string) {
	launchMu.Lock()
	lastLaunch[channelID] = time.Now()
	launchMu.Unlock()
}

func recentLaunch(channelID string) bool {
	launchMu.Lock()
	defer launchMu.Unlock()
	return time.Since(lastLaunch[channelID]) < launchCooldown
}

// Released re-forms the lobby of a channel a game has freed, unless the game ended right after launching (re-forming at
// once from the same players would loop forever).
func Released(guildID, channelID string) {
	if recentLaunch(channelID) {
		queueLogger.Warn(fmt.Sprintf("A game from channel %s ended right after launching; not re-forming a lobby automatically (players must rejoin)", channelID))
		return
	}
	if s := base.Session.Load(); s != nil {
		go SweepQueueChannel(s, guildID, channelID)
		ScheduleVoiceStatus(s, guildID, channelID)
	}
}

func HandleQueue(s *discordgo.Session, v *discordgo.VoiceStateUpdate) {
	base.Session.Store(s)
	if v == nil || v.VoiceState == nil || v.GuildID == "" || v.UserID == "" {
		return
	}
	if v.Member != nil && v.Member.User != nil && v.Member.User.Bot {
		return
	}

	oldChannel := ""
	if v.BeforeUpdate != nil {
		oldChannel = v.BeforeUpdate.ChannelID
	}
	newChannel := v.ChannelID
	if oldChannel == newChannel {
		return
	}

	guildID, userID := v.GuildID, v.UserID
	if !helpers.EventEnabled(guildID, QueueEventName) {
		return
	}
	snap, err := Queues.Snapshot(guildID)
	if err != nil || snap == nil {
		if err != nil {
			queueLogger.Error(fmt.Sprintf("Failed to load queue config for guild %s: %v", guildID, err))
		}
		return
	}

	// The status under a queue channel's name follows who is in it: each join or leave starts its 3 second wait over (see ScheduleVoiceStatus).
	for _, channel := range []string{oldChannel, newChannel} {
		if _, isQueue := snap.Bindings[channel]; isQueue {
			defer ScheduleVoiceStatus(s, guildID, channel)
		}
	}

	// Leaving: drop the member's place in line and dismantle a pending lobby
	// they were part of. BeforeUpdate can be missing from the state cache, so the
	// pending group is checked on its own.
	if _, wasQueue := snap.Bindings[oldChannel]; wasQueue {
		Queues.RecordLeave(oldChannel, userID)
		forgetAdmitted(oldChannel, userID)
	}
	if group := Queues.GroupOf(guildID, userID); group != nil && group.ChannelID != newChannel {
		queueLogger.Warn(fmt.Sprintf("Player %s left pending lobby %s, dismantling it", userID, group.ID))
		Queues.RemoveGroup(guildID, group.ID)
		go SweepQueueChannel(s, guildID, group.ChannelID)
	}

	// Joining
	bind, isQueue := snap.Bindings[newChannel]
	if !isQueue || Queues.IsPaused(guildID) {
		return
	}
	if Queues.GroupOf(guildID, userID) != nil {
		return // already allocated to a pending lobby
	}
	// The place in line is taken the moment they join, before the checks: those take a moment, and someone who joined later but was checked faster
	// must not be put ahead. A member the checks refuse is left out of the lobby later on, whatever their place.
	Queues.RecordJoin(newChannel, userID)
	if !admitMember(s, guildID, userID, newChannel, snap, bind) {
		return
	}
	warnOversizedParty(s, guildID, userID, newChannel, bind)
	SweepQueueChannel(s, guildID, newChannel)
}

// Waiting lists the members sitting in a queue channel who passed its join checks and are not in a game: who could still be put into
// one (for instance as a substitute).
func Waiting(s *discordgo.Session, guildID, channelID string) []string {
	var out []string
	for _, userID := range voiceRoster(s, guildID, channelID) {
		admittedMu.Lock()
		ok := admitted[channelID+":"+userID]
		admittedMu.Unlock()
		if ok && !Queues.deps.InGame(guildID, userID) {
			out = append(out, userID)
		}
	}
	return out
}

// isDenied reports whether the join checks refused this member in this queue channel.
func isDenied(channelID, userID string) bool {
	admittedMu.Lock()
	defer admittedMu.Unlock()
	return denied[channelID+":"+userID]
}

// admitMember runs the join checks (registered, not restricted, not in a game, rank gate) and remembers the result. A member who fails
// stays in the channel, told why, and is not queued.
func admitMember(s *discordgo.Session, guildID, userID, channelID string, snap *QueueSnapshot, bind QueueBinding) bool {
	ok := checkAdmission(s, guildID, userID, channelID, snap, bind)
	admittedMu.Lock()
	if ok {
		delete(denied, channelID+":"+userID)
	} else {
		denied[channelID+":"+userID] = true
	}
	admittedMu.Unlock()
	return ok
}

func checkAdmission(s *discordgo.Session, guildID, userID, channelID string, snap *QueueSnapshot, bind QueueBinding) bool {
	key := channelID + ":" + userID
	admittedMu.Lock()
	ok := admitted[key]
	admittedMu.Unlock()
	if ok {
		return true
	}

	if Queues.deps.InGame(guildID, userID) {
		// While their game's void vote is open, players usually sit in a queue channel to talk: that is no attempt to queue, so no alert.
		if Queues.deps.VoidVoting == nil || !Queues.deps.VoidVoting(guildID, userID) {
			sendQueueAlert(s, guildID, userID, helpers.QueueDeniedEmbed("You cannot queue while you are inside an active game. Finish your current game first."))
		}
		return false
	}

	if user := memberUser(s, guildID, userID); user != nil {
		if msg := helpers.EnforceAccountAge(s, guildID, user); msg != "" {
			sendQueueAlert(s, guildID, userID, helpers.QueueDeniedEmbed(msg))
			return false
		}
	}

	bannedRoles := []string{
		helpers.EventRequirementID(guildID, QueueEventName, types.ReqRole, QueueRoleBanned),
		punish.LadderRole(guildID, "queue_ban"), // the role /ban hands out
	}
	for _, id := range bannedRoles {
		if id == "" || !memberHasRole(s, guildID, userID, id) {
			continue
		}
		sendQueueAlert(s, guildID, userID, helpers.QueueDeniedEmbed("You are banned from queueing."))
		return false
	}

	if msg := queueRoleGate(s, guildID, userID, bind.Channel); msg != "" {
		sendQueueAlert(s, guildID, userID, helpers.QueueDeniedEmbed(msg))
		return false
	}

	view, err := database.Must().Player.Get(guildID, userID)
	if err != nil {
		if sdk.IsNotFound(err) {
			sendQueueAlert(s, guildID, userID, helpers.QueueDeniedEmbed("You must be registered to queue. Register with `/register` to link a Minecraft account first."))
			return false
		}
		queueLogger.Error(fmt.Sprintf("Failed to load player %s in guild %s: %v", userID, guildID, err))
		return false
	}
	playerID := sdk.UintToString(view.Player.ID)

	if reason, restricted := queueRestriction(guildID, playerID); restricted {
		sendQueueAlert(s, guildID, userID, helpers.QueueDeniedEmbed(reason))
		return false
	}

	if HasRankGate(bind.Channel) {
		rank := ""
		if st, err := database.Must().Stats.Get(guildID, userID, 0); err == nil {
			rank = st.Rank
		} else if !sdk.IsNotFound(err) {
			queueLogger.Error(fmt.Sprintf("Failed to load stats of %s in guild %s: %v", userID, guildID, err))
			return false
		}
		if gate := EvaluateRankGate(rank, nil, bind.Channel); !gate.Allowed {
			sendQueueAlert(s, guildID, userID, helpers.QueueDeniedEmbed(rankDenyMessage(gate.Reason, rank)))
			return false
		}
	}

	admittedMu.Lock()
	admitted[key] = true
	admittedMu.Unlock()
	return true
}

// queueRestriction reports whether the player has an active queue/server ban.
// A lookup failure fails open so an outage doesn't eject everyone.
func queueRestriction(guildID, playerID string) (string, bool) {
	active, err := database.Must().Punishments.ByPlayer(guildID, playerID, true, "", sdk.Page{})
	if err != nil {
		queueLogger.Error(fmt.Sprintf("Failed to load punishments of player %s in guild %s: %v", playerID, guildID, err))
		return "", false
	}
	for _, row := range active.Data {
		if punish.PunishmentTypeIn(row.Type, queueRestrictionTypes) {
			if row.Type == "server_ban" {
				return "You are banned from the server and cannot queue.", true
			}
			return "You are banned from queueing. Reason: " + strings.TrimSpace(row.Reason), true
		}
	}
	return "", false
}

// queueRoleGate returns a denial message when the member's roles fail the channel's
// allowed/denied role lists ("" = admitted). Denied wins; empty allowed admits everyone.
func queueRoleGate(s *discordgo.Session, guildID, userID string, c types.QueueChannelSetting) string {
	for _, id := range c.DeniedRoles {
		if id != "" && memberHasRole(s, guildID, userID, id) {
			return "Your roles are not allowed to join this queue."
		}
	}
	if len(c.AllowedRoles) == 0 {
		return ""
	}
	for _, id := range c.AllowedRoles {
		if id != "" && memberHasRole(s, guildID, userID, id) {
			return ""
		}
	}
	return "You need one of the required roles to join this queue."
}

func rankDenyMessage(reason, rank string) string {
	if rank == "" {
		rank = "Unranked"
	}
	if reason == "denied" {
		return fmt.Sprintf("Your rank (**%s**) is not allowed to join this queue.", rank)
	}
	return fmt.Sprintf("Your rank (**%s**) does not meet the requirements to join this queue.", rank)
}

// A member refused a place stays in the queue channel (nothing is kicked) and is simply left out of the queue. alerted
// remembers what they were told during this stay, so staying or re-sweeping does not repeat the alert; leaving the queue
// channel forgets it.
var (
	alertedMu sync.Mutex
	alerted   = map[string]bool{}
)

func forgetAdmitted(channelID, userID string) {
	alertedMu.Lock()
	for k := range alerted {
		if strings.HasPrefix(k, userID+":") {
			delete(alerted, k)
		}
	}
	alertedMu.Unlock()
	admittedMu.Lock()
	delete(admitted, channelID+":"+userID)
	delete(denied, channelID+":"+userID)
	admittedMu.Unlock()
}

// SweepQueueChannel builds as many lobbies as the channel's roster allows.
// Sweeps of one channel never overlap; a request during a sweep re-runs it.
func SweepQueueChannel(s *discordgo.Session, guildID, channelID string) {
	Queues.Process(channelID, func() {
		for formQueueLobby(s, guildID, channelID) {
		}
	})
}

// formQueueLobby makes one lobby and launches its game. It reports whether it did.
func formQueueLobby(s *discordgo.Session, guildID, channelID string) bool {
	snap, err := Queues.Snapshot(guildID)
	if err != nil || snap == nil {
		return false
	}
	bind, ok := snap.Bindings[channelID]
	if !ok {
		return false
	}

	voice := admittedVoiceMembers(s, guildID, channelID, snap, bind)
	if len(voice) < RequiredSlots(bind.Mode) {
		return false
	}

	categoryID := ""
	if ch, err := s.State.Channel(channelID); err == nil {
		categoryID = ch.ParentID
	}
	group, notices := Queues.FormGroup(FormParams{
		GuildID: guildID, ChannelID: channelID, CategoryID: categoryID, Voice: voice, Snapshot: snap,
	})
	sendPartyNotices(s, guildID, channelID, notices)
	if group == nil {
		return false
	}

	stillPresent := func(userID string) bool { return inVoiceChannel(s, guildID, userID, channelID) }
	if !Queues.AddGroup(group, stillPresent) {
		queueLogger.Warn(fmt.Sprintf("Lobby %s was rejected in channel %s", group.ID, channelID))
		return false
	}

	if err := Queues.deps.Launch(group); err != nil {
		Queues.RemoveGroup(guildID, group.ID)
		queueLogger.Error(fmt.Sprintf("Failed to launch lobby %s in channel %s: %v", group.ID, channelID, err))
		return false
	}
	noteLaunch(channelID)
	ScheduleVoiceStatus(s, guildID, channelID) // the lobby's players are in a game now
	queueLogger.Info(fmt.Sprintf("Launched lobby %s (%d players) from channel %s", group.ID, len(group.Players), channelID))
	return true
}

// admittedVoiceMembers lists the non-bot members in the channel, screening anyone
// who was already there before the bot saw them join (startup, missed events).
func admittedVoiceMembers(s *discordgo.Session, guildID, channelID string, snap *QueueSnapshot, bind QueueBinding) []QueueMember {
	var out []QueueMember
	s.State.RLock()
	var userIDs []string
	if g, err := s.State.Guild(guildID); err == nil {
		for _, vs := range g.VoiceStates {
			if vs.ChannelID == channelID {
				userIDs = append(userIDs, vs.UserID)
			}
		}
	}
	s.State.RUnlock()

	for _, id := range userIDs {
		m, err := s.State.Member(guildID, id)
		if err != nil {
			if m, err = s.GuildMember(guildID, id); err != nil {
				continue
			}
		}
		if m.User == nil || m.User.Bot {
			continue
		}
		if Queues.GroupOf(guildID, id) == nil && !admitMember(s, guildID, id, channelID, snap, bind) {
			continue
		}
		name := m.Nick
		if name == "" {
			name = m.User.Username
		}
		out = append(out, QueueMember{UserID: id, Username: name})
	}
	return out
}

func inVoiceChannel(s *discordgo.Session, guildID, userID, channelID string) bool {
	vs, err := s.State.VoiceState(guildID, userID)
	return err == nil && vs.ChannelID == channelID
}

// warnOversizedParty tells a party's leader, when one of its members joins, that
// more of them are in the channel than this queue's party size allows: such a
// party is held back from games (see FormGroup), so they should know up front.
func warnOversizedParty(s *discordgo.Session, guildID, userID, channelID string, bind QueueBinding) {
	if !bind.Mode.IsPartyQueueEnabled {
		return
	}
	party := partymgr.Parties.PartyOf(guildID, userID)
	if party == nil || !party.IsEffective() {
		return
	}
	maxSize := min(Queues.PartyConfig(guildID, bind.Mode).MaxSize, RequiredSlots(bind.Mode))
	inChannel := 0
	for _, id := range party.Members {
		if inVoiceChannel(s, guildID, id, channelID) {
			inChannel++
		}
	}
	if len(party.Members) <= maxSize && inChannel <= maxSize {
		return
	}
	sendPartyNotices(s, guildID, channelID, []QueueNotice{
		{Kind: NoticePartyTooLarge, Leader: party.Leader, Present: max(len(party.Members), inChannel), Max: maxSize},
	})
}

func sendPartyNotices(s *discordgo.Session, guildID, channelID string, notices []QueueNotice) {
	for _, n := range notices {
		key := fmt.Sprintf("party:%d:%s:%s:%s", n.Kind, guildID, channelID, n.Leader)
		if !Queues.ShouldAlert(key) {
			continue
		}
		var embed *discordgo.MessageEmbed
		switch n.Kind {
		case NoticePartyIncomplete:
			embed = queuePartyEmbed("Party Incomplete", fmt.Sprintf("Only **%d** of **%d** party members are in the queue channel. Your party is waiting until everyone has joined.", n.Present, n.Total))
		case NoticePartyEloExceeded:
			embed = queuePartyEmbed("Party ELO Too High", fmt.Sprintf("Your party's %s ELO (**%d**) exceeds the limit of **%d**, so it was excluded from this match.", n.Elo.Method, n.Elo.Aggregated, n.Elo.Max))
		case NoticePartyTooLarge:
			embed = queuePartyEmbed("Party Too Large", fmt.Sprintf("**%d** party members are in the queue channel but this queue allows at most **%d**.", n.Present, n.Max))
		default:
			continue
		}
		msg := &discordgo.MessageSend{
			Content:         "<@" + n.Leader + ">",
			Embeds:          []*discordgo.MessageEmbed{embed},
			AllowedMentions: &discordgo.MessageAllowedMentions{Users: []string{n.Leader}},
		}
		if err := helpers.SendCommandChannelMessage(s, guildID, partyCmdName, partyAlertsKey, msg); err != nil {
			queueLogger.Warn(fmt.Sprintf("Failed to send party alert in guild %s: %v", guildID, err))
		}
	}
}

// memberUser is the Discord user behind a member id (from the cache when possible).
func memberUser(s *discordgo.Session, guildID, userID string) *discordgo.User {
	if m, err := s.State.Member(guildID, userID); err == nil && m.User != nil {
		return m.User
	}
	if u, err := s.User(userID); err == nil {
		return u
	}
	return nil
}

// memberHasRole reports whether the member holds the role (false when the member can't be loaded).
func memberHasRole(s *discordgo.Session, guildID, userID, roleID string) bool {
	m, err := s.State.Member(guildID, userID)
	if err != nil {
		if m, err = s.GuildMember(guildID, userID); err != nil {
			return false
		}
	}
	for _, id := range m.Roles {
		if id == roleID {
			return true
		}
	}
	return false
}

// sendQueueAlert tells a member why they were turned away: in the guild's alerts channel when one is set (see Alert), else by direct
// message (they stay in the queue channel, just not queued).
func sendQueueAlert(s *discordgo.Session, guildID, userID string, embed *discordgo.MessageEmbed) {
	// The member stays in the channel, so the same refusal is told once per stay, not on every sweep or rejoin.
	alertedMu.Lock()
	key := userID + ":" + guildID + ":" + embed.Description
	already := alerted[key]
	alerted[key] = true
	alertedMu.Unlock()
	if already {
		return
	}
	// The alerts channel pings them there already: a direct message as well would tell them twice.
	if posts := Queues.deps.AlertPosts; posts == nil || !posts(guildID, "queue_denied") {
		dm, err := s.UserChannelCreate(userID)
		if err == nil {
			_, err = s.ChannelMessageSendEmbed(dm.ID, embed)
		}
		if err != nil {
			queueLogger.Warn(fmt.Sprintf("Failed to send queue alert to %s in guild %s: %v", userID, guildID, err))
		}
	}
	// Staff hear about it too when the server asked for queue-denied alerts (Game phases > Post-game > Results and alerts).
	Queues.deps.Alert(s, guildID, "queue_denied", []string{userID}, fmt.Sprintf("<@%s> was refused a place in the queue.", userID), &discordgo.MessageEmbedField{Name: "Why", Value: base.FirstNonEmpty(embed.Description, "No reason given")})
}

// QueueManager holds everything about queues that isn't Discord I/O: the FIFO
// join order per queue channel, the pending (formed but not yet started)
// groups, match formation with parties, rank gates, the paused flag and the
// per-channel sweep serialization. The event layer feeds it the voice roster
// and acts on what it returns; starting the game is the game manager's job
// (call RemoveGroup once the game exists).

// ============================================================================
// Types
// ============================================================================

// QueueMember is defined in package types (queues.go).
type QueueMember = types.QueueMember

// QueueGroup is defined in package types (queues.go).
type QueueGroup = types.QueueGroup

// QueueBinding is defined in package types (queues.go).
type QueueBinding = types.QueueBinding

// QueueSnapshot is a guild's enabled queue channels, resolved.
type QueueSnapshot struct {
	Modes    []types.Mode
	Queues   []types.QueueConfig
	Bindings map[string]QueueBinding // by queue channel id
	// Disabled holds the queue channels that are set up but not live (their queue, the channel or its mode is switched off): they no
	// longer queue anyone, and their status says so.
	Disabled map[string]bool
}

// QueueNoticeKind is defined in package types (queues.go).
type QueueNoticeKind = types.QueueNoticeKind

const (
	NoticePartyIncomplete QueueNoticeKind = iota // members missing from the channel
	NoticePartyEloExceeded
	NoticePartyTooLarge
)

// QueueNotice is a party problem found while forming a group. Alert the party
// leader (rate-limit with ShouldAlert), don't fail the formation.
type QueueNotice struct {
	Kind    QueueNoticeKind
	Leader  string
	Present int
	Total   int
	Max     int
	Elo     *partymgr.EloDenial
}

// FormParams is one formation attempt for a queue channel.
type FormParams struct {
	GuildID    string
	ChannelID  string
	CategoryID string        // fallback when the queue config has none (the channel's parent)
	Voice      []QueueMember // non-bot members currently in the channel
	Snapshot   *QueueSnapshot
}

// QueueDeps are the manager's outside dependencies; nil fields use the database-backed defaults.
type QueueDeps struct {
	Parties         *partymgr.PartyManager
	InGame          func(guildID, userID string) bool // nil = nobody is in a game
	VoidVoting      func(guildID, userID string) bool // whether the member's game has a void vote open; nil = none do (players drift into queue channels meanwhile)
	HasActiveSeason func(guildID string) bool
	Paused          func(guildID string) bool
	FlowMaxParty    func(guildID string, mode types.Mode) *int // the game flow's cap on a party's size; nil = none
	Launch          func(group *QueueGroup) error              // starts the game of a full lobby
	Alert           func(s *discordgo.Session, guildID, key string, pingUserIDs []string, description string, fields ...*discordgo.MessageEmbedField)
	AlertPosts      func(guildID, key string) bool // whether Alert posts that kind in a channel; nil = never
}

// Wire sets dependencies that live in packages built on top of this one (games).
func (q *QueueManager) Wire(set func(*QueueDeps)) { set(&q.deps) }

// QueueManager owns queue state for every guild.
type QueueManager struct {
	deps QueueDeps

	mu          sync.Mutex
	joinOrder   map[string][]string               // channel -> user ids, oldest first
	groups      map[string]map[string]*QueueGroup // guild -> group id -> group
	playerGroup map[string]string                 // guild:user -> group id
	running     map[string]bool                   // channels being swept
	dirty       map[string]bool                   // channels that asked for another sweep
	alerts      map[string]time.Time

	snapshots *base.TTLCache[*QueueSnapshot]
	seasons   *base.TTLCache[bool]
	paused    *base.TTLCache[bool]
}

// Queues is the process-wide manager.
var Queues = NewQueueManager(QueueDeps{})

const (
	alertCooldown = time.Minute
	maxResweeps   = 8
	snapshotTTL   = 30 * time.Second
	seasonTTL     = time.Minute
	pausedTTL     = 5 * time.Second
)

func NewQueueManager(d QueueDeps) *QueueManager {
	q := &QueueManager{
		joinOrder:   map[string][]string{},
		groups:      map[string]map[string]*QueueGroup{},
		playerGroup: map[string]string{},
		running:     map[string]bool{},
		dirty:       map[string]bool{},
		alerts:      map[string]time.Time{},
		snapshots:   base.NewTTLCache[*QueueSnapshot](snapshotTTL),
		seasons:     base.NewTTLCache[bool](seasonTTL),
		paused:      base.NewTTLCache[bool](pausedTTL),
	}
	if d.Parties == nil {
		d.Parties = partymgr.Parties
	}
	if d.InGame == nil {
		d.InGame = func(string, string) bool { return false }
	}
	if d.HasActiveSeason == nil {
		d.HasActiveSeason = func(guildID string) bool {
			ok, _ := q.seasons.Get(guildID, func() (bool, error) { return dbHasActiveSeason(guildID), nil })
			return ok
		}
	}
	if d.Paused == nil {
		d.Paused = func(guildID string) bool {
			p, _ := q.paused.Get(guildID, func() (bool, error) {
				cfg, err := helpers.LoadLFGConfig(guildID)
				if err != nil {
					return false, err
				}
				return cfg.QueuesPaused, nil
			})
			return p
		}
	}
	if d.FlowMaxParty == nil {
		d.FlowMaxParty = func(string, types.Mode) *int { return nil }
	}
	if d.Launch == nil {
		d.Launch = func(*QueueGroup) error { return errors.New("games are not available") }
	}
	if d.Alert == nil {
		d.Alert = func(*discordgo.Session, string, string, []string, string, ...*discordgo.MessageEmbedField) {}
	}
	q.deps = d
	return q
}

// PartyConfig is the party rules that apply to a queue of mode. Priority is the
// mode's own party overrides, then the flow's max party size (standard or
// captain queues), then the guild's party limits.
func (q *QueueManager) PartyConfig(guildID string, mode types.Mode) types.PartiesConfig {
	cfg := q.deps.Parties.Config(guildID)
	if flowMax := q.deps.FlowMaxParty(guildID, mode); flowMax != nil {
		cfg.MaxSize = *flowMax
	}
	return partymgr.ResolveModePartyConfig(cfg, mode.Party)
}

func groupKey(guildID, userID string) string { return guildID + ":" + userID }

// ============================================================================
// Config: snapshot, bindings, modes, slots, rank gates
// ============================================================================

// FindMode looks a mode up by name or stable ID (the dashboard binds queue
// channels by stable ID), ignoring case and surrounding space.
func FindMode(modes []types.Mode, name string) *types.Mode {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil
	}
	for i := range modes {
		if strings.ToLower(strings.TrimSpace(modes[i].Name)) == name || strings.ToLower(strings.TrimSpace(modes[i].StableID)) == name {
			return &modes[i]
		}
	}
	return nil
}

// RequiredSlots is how many players a mode needs to start.
func RequiredSlots(m types.Mode) int {
	if m.MaxPlayers > 0 {
		return m.MaxPlayers
	}
	return m.TeamCount * m.PlayersPerTeam
}

// ResolveQueueBinding finds the enabled queue channel entry (and its mode) for channelID.
func ResolveQueueBinding(queues []types.QueueConfig, modes []types.Mode, channelID string) *QueueBinding {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil
	}
	for _, q := range queues {
		if !q.IsEnabled {
			continue
		}
		for _, ch := range q.Settings {
			if !ch.IsEnabled || strings.TrimSpace(ch.ChannelID) != channelID {
				continue
			}
			if mode := FindMode(modes, ch.Mode); mode != nil && mode.IsEnabled {
				return &QueueBinding{Queue: q, Channel: ch, Mode: *mode}
			}
		}
	}
	return nil
}

// Snapshot returns the guild's queue channels (cached briefly; see InvalidateSnapshot).
func (q *QueueManager) Snapshot(guildID string) (*QueueSnapshot, error) {
	return q.snapshots.Get(guildID, func() (*QueueSnapshot, error) {
		modes, err := helpers.GuildMetaModes(guildID)
		if err != nil {
			return nil, err
		}
		var queues []types.QueueConfig
		raw, err := database.Must().GameConfig.Field(guildID, "queues")
		if err != nil && !sdk.IsNotFound(err) {
			return nil, err
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &queues); err != nil {
				return nil, err
			}
		}
		return BuildQueueSnapshot(modes, queues), nil
	})
}

// BuildQueueSnapshot resolves every enabled channel of queues against modes.
func BuildQueueSnapshot(modes []types.Mode, queues []types.QueueConfig) *QueueSnapshot {
	s := &QueueSnapshot{Modes: modes, Queues: queues, Bindings: map[string]QueueBinding{}, Disabled: map[string]bool{}}
	for _, qc := range queues {
		for _, ch := range qc.Settings {
			id := strings.TrimSpace(ch.ChannelID)
			if b := ResolveQueueBinding(queues, modes, ch.ChannelID); b != nil {
				s.Bindings[id] = *b
			} else if id != "" {
				s.Disabled[id] = true
			}
		}
	}
	return s
}

// RankGateResult is defined in package types (queues.go).
type RankGateResult = types.RankGateResult

func cleanTags(in []string) []string {
	var out []string
	for _, t := range in {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// HasRankGate reports whether the channel restricts by rank at all.
func HasRankGate(c types.QueueChannelSetting) bool {
	return len(cleanTags(c.AllowedRanks)) > 0 || len(cleanTags(c.DeniedRanks)) > 0
}

// EvaluateRankGate checks a player's competitive rank name and division labels
// (e.g. "Premium") against the channel's lists. Denied wins over allowed; an
// empty allowed list admits everyone not denied.
func EvaluateRankGate(rank string, divisions []string, c types.QueueChannelSetting) RankGateResult {
	have := cleanTags(append([]string{rank}, divisions...))
	matches := func(tags []string) bool {
		for _, t := range tags {
			if slices.Contains(have, t) {
				return true
			}
		}
		return false
	}
	if matches(cleanTags(c.DeniedRanks)) {
		return RankGateResult{Reason: "denied"}
	}
	if allowed := cleanTags(c.AllowedRanks); len(allowed) > 0 && !matches(allowed) {
		return RankGateResult{Reason: "missing_allowed"}
	}
	return RankGateResult{Allowed: true}
}

// ============================================================================
// Paused flag & season
// ============================================================================

func (q *QueueManager) IsPaused(guildID string) bool { return q.deps.Paused(guildID) }

func dbHasActiveSeason(guildID string) bool {
	_, err := database.Must().Season.Active(guildID)
	return err == nil
}

// ============================================================================
// FIFO join order
// ============================================================================

// RecordJoin puts userID at the back of channelID's line.
func (q *QueueManager) RecordJoin(channelID, userID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.joinOrder[channelID] = append(slices.DeleteFunc(q.joinOrder[channelID], func(id string) bool { return id == userID }), userID)
}

func (q *QueueManager) RecordLeave(channelID, userID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.removeFromOrderLocked(channelID, userID)
}

func (q *QueueManager) removeFromOrderLocked(channelID, userID string) {
	q.joinOrder[channelID] = slices.DeleteFunc(q.joinOrder[channelID], func(id string) bool { return id == userID })
	if len(q.joinOrder[channelID]) == 0 {
		delete(q.joinOrder, channelID)
	}
}

func (q *QueueManager) syncLocked(channelID string, present []string) {
	if len(present) == 0 {
		delete(q.joinOrder, channelID)
		return
	}
	in := make(map[string]bool, len(present))
	for _, id := range present {
		in[id] = true
	}
	next := make([]string, 0, len(present))
	seen := make(map[string]bool, len(present))
	for _, id := range q.joinOrder[channelID] {
		if in[id] && !seen[id] {
			next = append(next, id)
			seen[id] = true
		}
	}
	for _, id := range present {
		if !seen[id] {
			next = append(next, id)
			seen[id] = true
		}
	}
	q.joinOrder[channelID] = next
}

func (q *QueueManager) orderLocked(channelID string, players []QueueMember) []QueueMember {
	pos := make(map[string]int, len(q.joinOrder[channelID]))
	for i, id := range q.joinOrder[channelID] {
		pos[id] = i
	}
	out := slices.Clone(players)
	// unindexed players sort after everyone in line, keeping their given order
	slices.SortStableFunc(out, func(a, b QueueMember) int {
		pa, oka := pos[a.UserID]
		pb, okb := pos[b.UserID]
		switch {
		case oka && okb:
			return pa - pb
		case oka:
			return -1
		case okb:
			return 1
		}
		return 0
	})
	return out
}

// ============================================================================
// Selection
// ============================================================================

type queueItem struct{ members []QueueMember }

// SelectPlayers picks exactly `slots` players in line order. Complete parties
// (2+ members, all present in players) stay whole; if taking the next item
// would leave the lobby unfillable (1 solo + two parties of 4 for 8 slots) it
// is skipped so a later combination can fill it. Returns nil when no
// combination fills every slot.
func SelectPlayers(players []QueueMember, parties []partymgr.PartyGroup, slots int) []QueueMember {
	if slots <= 0 {
		return nil
	}
	byID := make(map[string]QueueMember, len(players))
	for _, p := range players {
		byID[p.UserID] = p
	}
	partyOf := map[string]partymgr.PartyGroup{}
	for _, pg := range parties {
		if len(pg.Members) < 2 {
			continue
		}
		whole := true
		for _, id := range pg.Members {
			if _, ok := byID[id]; !ok {
				whole = false
			}
		}
		if whole {
			for _, id := range pg.Members {
				partyOf[id] = pg
			}
		}
	}

	var items []queueItem
	used := map[string]bool{}
	for _, p := range players {
		if used[p.UserID] {
			continue
		}
		if pg, ok := partyOf[p.UserID]; ok {
			var ms []QueueMember
			for _, id := range pg.Members {
				if !used[id] {
					used[id] = true
					ms = append(ms, byID[id])
				}
			}
			items = append(items, queueItem{ms})
			continue
		}
		used[p.UserID] = true
		items = append(items, queueItem{[]QueueMember{p}})
	}

	suffix := make([]int, len(items)+1)
	for i := len(items) - 1; i >= 0; i-- {
		suffix[i] = suffix[i+1] + len(items[i].members)
	}
	var picked []QueueMember
	var dfs func(i, filled int) bool
	dfs = func(i, filled int) bool {
		if filled == slots {
			return true
		}
		if i >= len(items) || filled+suffix[i] < slots {
			return false
		}
		if n := len(items[i].members); filled+n <= slots {
			start := len(picked)
			picked = append(picked, items[i].members...)
			if dfs(i+1, filled+n) {
				return true
			}
			picked = picked[:start]
		}
		return dfs(i+1, filled)
	}
	if !dfs(0, 0) {
		return nil
	}
	return picked
}

// ============================================================================
// Formation
// ============================================================================

// FormGroup tries to build a lobby from a queue channel's roster. It returns
// nil when the queue is paused/misconfigured or there aren't enough players.
// Notices report parties that were held back so the caller can alert them.
func (q *QueueManager) FormGroup(p FormParams) (*QueueGroup, []QueueNotice) {
	if p.Snapshot == nil || q.IsPaused(p.GuildID) {
		return nil, nil
	}
	bind, ok := p.Snapshot.Bindings[p.ChannelID]
	if !ok {
		return nil, nil
	}
	categoryID := strings.TrimSpace(bind.Queue.CategoryID)
	if categoryID == "" {
		categoryID = p.CategoryID
	}
	if categoryID == "" {
		return nil, nil // nowhere to create the game's channels
	}
	slots := RequiredSlots(bind.Mode)
	if slots <= 0 {
		return nil, nil
	}

	voiceIDs := make([]string, 0, len(p.Voice))
	seen := map[string]bool{}
	var candidates []QueueMember
	q.mu.Lock()
	for _, m := range p.Voice {
		if seen[m.UserID] {
			continue
		}
		seen[m.UserID] = true
		voiceIDs = append(voiceIDs, m.UserID)
		if _, pending := q.playerGroup[groupKey(p.GuildID, m.UserID)]; pending || q.deps.InGame(p.GuildID, m.UserID) {
			continue
		}
		candidates = append(candidates, m)
	}
	q.syncLocked(p.ChannelID, voiceIDs)
	ordered := q.orderLocked(p.ChannelID, candidates)
	q.mu.Unlock()

	if len(ordered) < slots {
		return nil, nil
	}
	inLine := make(map[string]bool, len(ordered))
	for _, m := range ordered {
		inLine[m.UserID] = true
	}

	var notices []QueueNotice
	skipped := map[string]bool{}
	var parties []partymgr.PartyGroup

	if cfg := q.PartyConfig(p.GuildID, bind.Mode); bind.Mode.IsPartyQueueEnabled && partymgr.PartiesEnabled(cfg) {
		maxParty := min(cfg.MaxSize, slots)
		handled := map[string]bool{}
		for _, m := range ordered {
			party := q.deps.Parties.PartyOf(p.GuildID, m.UserID)
			if party == nil || !party.IsEffective() || handled[party.Leader] {
				continue
			}
			handled[party.Leader] = true

			var here []string
			for _, id := range party.Members {
				if inLine[id] {
					here = append(here, id)
				}
			}
			q.deps.Parties.Touch(p.GuildID, party.Leader) // queueing counts as activity
			hold := func() {
				for _, id := range here {
					skipped[id] = true
				}
			}

			if len(here) < len(party.Members) && cfg.RequireAllMembersInQueue {
				notices = append(notices, QueueNotice{Kind: NoticePartyIncomplete, Leader: party.Leader, Present: len(here), Total: len(party.Members)})
				hold()
				continue
			}
			if denial := q.deps.Parties.ValidateExistingElo(p.GuildID, party, &cfg); denial != nil {
				notices = append(notices, QueueNotice{Kind: NoticePartyEloExceeded, Leader: party.Leader, Elo: denial})
				hold()
				continue
			}
			if len(here) < cfg.MinQueueSize {
				continue // too few present to count as a party: they queue as individuals
			}
			if len(here) > maxParty {
				notices = append(notices, QueueNotice{Kind: NoticePartyTooLarge, Leader: party.Leader, Present: len(here), Max: maxParty})
				hold()
				continue
			}
			parties = append(parties, partymgr.PartyGroup{Leader: party.Leader, Members: here})
		}
	}

	final := make([]QueueMember, 0, len(ordered))
	for _, m := range ordered {
		if !skipped[m.UserID] {
			final = append(final, m)
		}
	}
	picked := SelectPlayers(final, parties, slots)
	if picked == nil {
		return nil, notices
	}

	chosen := make(map[string]bool, len(picked))
	for _, m := range picked {
		chosen[m.UserID] = true
	}
	var whole []partymgr.PartyGroup
	for _, pg := range parties {
		if len(pg.Members) < 2 {
			continue
		}
		all := true
		for _, id := range pg.Members {
			all = all && chosen[id]
		}
		if all {
			whole = append(whole, pg)
		}
	}
	return &QueueGroup{
		ID: base.NewID(), GuildID: p.GuildID, ChannelID: p.ChannelID, CategoryID: categoryID,
		Mode: bind.Mode, Players: picked, Parties: whole, CreatedAt: time.Now(),
	}, notices
}

// ============================================================================
// Pending groups
// ============================================================================

// AddGroup registers a formed lobby, all-or-nothing. It refuses when there's
// no active season, when any player got into a game or another lobby in the
// meantime, or when stillPresent (a live voice-roster check, may be nil) says
// someone left. On success the players leave the join order.
func (q *QueueManager) AddGroup(g *QueueGroup, stillPresent func(userID string) bool) bool {
	if g == nil || len(g.Players) == 0 || !q.deps.HasActiveSeason(g.GuildID) {
		return false
	}
	for _, m := range g.Players {
		if q.deps.InGame(g.GuildID, m.UserID) || (stillPresent != nil && !stillPresent(m.UserID)) {
			return false
		}
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	for _, m := range g.Players {
		if _, taken := q.playerGroup[groupKey(g.GuildID, m.UserID)]; taken {
			return false
		}
	}
	if q.groups[g.GuildID] == nil {
		q.groups[g.GuildID] = map[string]*QueueGroup{}
	}
	if _, dup := q.groups[g.GuildID][g.ID]; dup {
		return false
	}
	q.groups[g.GuildID][g.ID] = g
	for _, m := range g.Players {
		q.playerGroup[groupKey(g.GuildID, m.UserID)] = g.ID
		q.removeFromOrderLocked(g.ChannelID, m.UserID)
	}
	return true
}

// GroupOf returns the pending lobby userID is allocated to, if any.
func (q *QueueManager) GroupOf(guildID, userID string) *QueueGroup {
	q.mu.Lock()
	defer q.mu.Unlock()
	if id, ok := q.playerGroup[groupKey(guildID, userID)]; ok {
		return q.groups[guildID][id]
	}
	return nil
}

// RemoveGroup dismantles a pending lobby (a player left, or its game has been created).
func (q *QueueManager) RemoveGroup(guildID, groupID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.removeGroupLocked(guildID, groupID)
}

func (q *QueueManager) removeGroupLocked(guildID, groupID string) {
	g := q.groups[guildID][groupID]
	if g == nil {
		return
	}
	delete(q.groups[guildID], groupID)
	for _, m := range g.Players {
		if q.playerGroup[groupKey(guildID, m.UserID)] == groupID {
			delete(q.playerGroup, groupKey(guildID, m.UserID))
		}
	}
}

// ============================================================================
// Sweep serialization & alert throttle
// ============================================================================

// Process runs sweep (one formation pass over channelID) so that only one
// runs per channel at a time. A request that arrives mid-sweep is coalesced
// into a re-run afterwards, at most maxResweeps times.
func (q *QueueManager) Process(channelID string, sweep func()) {
	q.mu.Lock()
	if q.running[channelID] {
		q.dirty[channelID] = true
		q.mu.Unlock()
		return
	}
	q.running[channelID] = true
	q.mu.Unlock()

	defer func() {
		q.mu.Lock()
		delete(q.running, channelID)
		delete(q.dirty, channelID)
		q.mu.Unlock()
	}()
	for range maxResweeps + 1 {
		sweep()
		q.mu.Lock()
		again := q.dirty[channelID]
		delete(q.dirty, channelID)
		q.mu.Unlock()
		if !again {
			return
		}
	}
}

// ShouldAlert rate-limits repeat alerts for the same key to one per minute.
func (q *QueueManager) ShouldAlert(key string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	if last, ok := q.alerts[key]; ok && now.Sub(last) < alertCooldown {
		return false
	}
	q.alerts[key] = now
	for k, t := range q.alerts { // keep the map from growing forever
		if now.Sub(t) > alertCooldown {
			delete(q.alerts, k)
		}
	}
	return true
}
