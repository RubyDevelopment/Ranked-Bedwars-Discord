package helpers

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
	appLog "ranked-bedwars/src/utility"
	apputil "ranked-bedwars/src/utility"
)

const (
	PartyCmdName   = "party_group"
	PartyAlertsKey = "party_alerts"
)

var partyButtonRe = regexp.MustCompile(`^party_(accept|deny)_(\d+)_(\d+)$`)

// BotSession is the live session, recorded as commands and events run, for effects that happen outside any interaction.
var BotSession atomic.Pointer[discordgo.Session]

func AnnouncePartyJoin(s *discordgo.Session, guildID string, party *Party, joined string) {
	e := partyPlayerJoinedEmbed(joined, len(party.Members), party.MaxSize)
	_ = SendPartyAlert(s, guildID, PartyCmdName, PartyAlertsKey, party.Leader, e, nil)
}

func deliverPartyNotice(guildID, userID string, n PartyNotice) {
	s := BotSession.Load()
	if s == nil {
		return
	}
	var e *discordgo.MessageEmbed
	switch n {
	case NoticeDisbandedByLeader:
		e = partyDisbandedByLeaderEmbed()
	case NoticeDisbandedLastMember:
		e = partyDisbandedLastMemberEmbed()
	case NoticeDisbandedInactivity:
		e = partyDisbandedInactivityEmbed()
	case NoticeDisbandedSystemOff:
		e = partyDisbandedSystemOffEmbed()
	case NoticeKicked:
		e = partyKickedEmbed()
	case NoticeLeaderChanged:
		e = partyLeaderChangedEmbed()
	default:
		return
	}
	if err := SendPartyAlert(s, guildID, PartyCmdName, PartyAlertsKey, userID, e, nil); err != nil {
		apputil.DiscordLogger.Error(fmt.Sprintf("[Party] could not deliver notice %d to %s in %s: %v", n, userID, guildID, err))
	}
}

func HandlePartyButton(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	m := partyButtonRe.FindStringSubmatch(customID)
	if m == nil {
		return false
	}
	action, leaderID, targetID := m[1], m[2], m[3]
	clickerID := InteractionUserID(i)

	if clickerID != targetID {
		_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral, Embeds: []*discordgo.MessageEmbed{partyNotYourInvitationEmbed()}},
		})
		return true
	}

	_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})

	if action == "accept" {
		party, err := Parties.Accept(i.GuildID, leaderID, targetID)
		if err != nil {
			_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{Embeds: []*discordgo.MessageEmbed{partyErrorEmbedOrGeneric(err)}, Flags: discordgo.MessageFlagsEphemeral})
			return true
		}
		AnnouncePartyJoin(s, i.GuildID, party, targetID)
		empty := []discordgo.MessageComponent{}
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{PartyJoinedEmbed(party.Leader, len(party.Members), party.MaxSize)}, Components: &empty})
		return true
	}

	if err := Parties.Decline(i.GuildID, leaderID, targetID); err != nil {
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{Embeds: []*discordgo.MessageEmbed{partyErrorEmbedOrGeneric(err)}, Flags: discordgo.MessageFlagsEphemeral})
		return true
	}
	_ = SendPartyAlert(s, i.GuildID, PartyCmdName, PartyAlertsKey, leaderID, PartyInvitationDeclinedEmbed(targetID), nil)
	empty := []discordgo.MessageComponent{}
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{PartyDeclinedEmbed()}, Components: &empty})
	return true
}

func SendPartyAlert(s *discordgo.Session, guildID, cmdName, key, userID string, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) error {
	msg := &discordgo.MessageSend{
		Embeds:          []*discordgo.MessageEmbed{embed},
		Components:      components,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}
	if pingsEnabled(guildID, userID) {
		msg.Content = "<@" + userID + ">"
		msg.AllowedMentions = &discordgo.MessageAllowedMentions{Users: []string{userID}}
	}
	return SendCommandChannelMessage(s, guildID, cmdName, key, msg)
}

func pingsEnabled(guildID, userID string) bool {
	v, err := database.Must().Player.Get(guildID, userID)
	if err != nil || v.Config == nil {
		return true
	}
	return v.Config.IsPingOnAlertsEnabled
}

func InviteButtons(prefix, subjectID, targetID string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: "Accept", Style: discordgo.SuccessButton, CustomID: fmt.Sprintf("%s_accept_%s_%s", prefix, subjectID, targetID)},
		discordgo.Button{Label: "Deny", Style: discordgo.DangerButton, CustomID: fmt.Sprintf("%s_deny_%s_%s", prefix, subjectID, targetID)},
	}}}
}

func MentionList(ids []string) string {
	if len(ids) == 0 {
		return "—"
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = "<@" + id + ">"
	}
	return strings.Join(out, "\n")
}

func LowerFirst(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]|0x20) + s[1:]
}

func UnexpectedErrorPartyEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while processing your party request. Please try again later or contact support.",
	})
}

func PartyInvalidSubcommandEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "You must specify a valid subcommand. Select one from the command options.",
	})
}

func PartyRegistrationRequiredEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You are not registered. Run `/player_register` to link a Minecraft account first.",
	})
}

func PartyPlayerNotFoundEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: "You must select a player to invite. Use the user selection menu to choose a registered player.",
	})
}

func PartyBotTargetEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You cannot invite bot accounts to a party. Please select a registered player.",
	})
}

func PartyTargetNotRegisteredEmbed(user *discordgo.User) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: fmt.Sprintf("<@%s> is not registered in this guild. Ask them to run `/player_register` to link their Minecraft account before inviting them to a party.", user.ID),
	})
}

func PartyCreatedEmbed(inactivity int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Party Created",
		Description: fmt.Sprintf("Your party is ready! Invite players with `/party_group invite` to build your team.\n\n> **Leader:** You\n> **Members:** 1/4\n> **Invites:** Open\n> **Disbands After:** %d minutes of inactivity", inactivity),
	})
}

func PartyInviteSentEmbed(head, body string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       head,
		Description: body + "\n\nThe player can accept or decline the invitation from the party alerts channel.",
	})
}

func PartyInviteSentNoAlertEmbed(head, body string) *discordgo.MessageEmbed {
	return Preset("caution", types.PresetOptions{
		Title:       head,
		Description: body + "\n\n> **Note:** I couldn't post the invitation to the party alerts channel. Ask the player to run `/party_group accept` with you as the leader to join manually.",
	})
}

func PartyInvitationEmbed(targetID, leaderID string, members, maxSize int, expires time.Time) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title: "Party Invitation",
		Description: fmt.Sprintf(
			"<@%s>, <@%s> invited you to join their party!\n\n> **Party Size:** %d/%d\n> **Leader:** <@%s>\n> **Expires:** %s\n\nClick **Accept** to join or **Deny** to decline the invitation.",
			targetID, leaderID, members, maxSize, leaderID, DiscordRelativeTimestamp(expires),
		),
	})
}

func PartyInvitationDeclinedEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Invitation Declined",
		Description: fmt.Sprintf("<@%s> declined your party invitation. You can invite another player or ask them to request a new invitation.", userID),
	})
}

func PartyDeclinedEmbed() *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Invitation Declined",
		Description: "You declined the party invitation. If you change your mind, ask the leader to send you a new invitation.",
	})
}

func PartyLeaderNotFoundEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: "Select the party leader who invited you.",
	})
}

func PartyJoinedEmbed(leaderID string, members, maxSize int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Party Joined",
		Description: fmt.Sprintf("You successfully joined <@%s>'s party!\n\n> **Party Size:** %d/%d\n> **Leader:** <@%s>\n\nYou can now queue together for matches.", leaderID, members, maxSize, leaderID),
	})
}

func PartyLeftEmbed() *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Left Party",
		Description: "You have successfully left the party. You can create a new party with `/party_group create` or join another party by accepting an invitation.",
	})
}

func PartyPlayerLeftEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Player Left",
		Description: fmt.Sprintf("<@%s> has left your party. You can invite a new player to fill the slot with `/party_group invite`.", userID),
	})
}

func PartyPlayerKickedEmbed(targetID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Player Removed",
		Description: fmt.Sprintf("<@%s> has been removed from your party. You can invite a new player to fill the slot with `/party_group invite`.", targetID),
	})
}

func PartyLeadershipTransferredEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "You Are the Party Leader",
		Description: fmt.Sprintf("<@%s> has transferred party leadership to you. You can now manage the party, invite members, kick players, and disband the party.", userID),
	})
}

func PartyLeadershipTransferredToLeaderEmbed(targetID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Leadership Transferred",
		Description: fmt.Sprintf("You have successfully transferred party leadership to <@%s>. They are now responsible for managing the party.", targetID),
	})
}

func PartyDisbandedEmbed() *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been successfully disbanded. All members have been notified.\n\nYou can create a new party at any time with `/party_group create`.",
	})
}

func PartyInvitesOpenedEmbed() *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Invites Open",
		Description: "Party invites have been reopened. Players can now be invited to your party using `/party_group invite`.",
	})
}

func PartyInvitesClosedEmbed() *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Invites Closed",
		Description: "Party invites have been paused. No new players can be invited until you reopen invites with `/party_group invites`.",
	})
}

func PartyNotInPartyEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in a Party",
		Description: "You are not currently in a party. Create a new party with `/party_group create` or accept a pending invitation from another player.",
	})
}

func PartyTargetNotInPartyEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in a Party",
		Description: fmt.Sprintf("<@%s> is not currently in a party. They can create one with `/party_group create` or may have pending invitations to accept.", userID),
	})
}

func partyPlayerJoinedEmbed(userID string, members, maxSize int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Player Joined",
		Description: fmt.Sprintf("<@%s> has joined your party!\n\n> **Party Size:** %d/%d\n\nYour party is now ready to queue for matches.", userID, members, maxSize),
	})
}

func partyDisbandedByLeaderEmbed() *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "The party leader has disbanded the party. You can create a new party with `/party_group create` at any time.",
	})
}

func partyDisbandedLastMemberEmbed() *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been disbanded because all other members have left. You can create a new party with `/party_group create` at any time.",
	})
}

func partyDisbandedInactivityEmbed() *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been disbanded due to inactivity. Parties automatically disband after a period of no activity. You can create a new party with `/party_group create` at any time.",
	})
}

func partyDisbandedSystemOffEmbed() *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been disbanded because parties have been disabled on this server. Contact an administrator for more information.",
	})
}

func partyKickedEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Removed from Party",
		Description: "You have been removed from the party by its leader. You can create a new party with `/party_group create` or accept invitations from other players.",
	})
}

func partyLeaderChangedEmbed() *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "You Are the Party Leader",
		Description: "The previous party leader has left, so you are now the party leader. You can manage the party, invite members, kick players, and disband the party.",
	})
}

func partyNotYourInvitationEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not Your Invitation",
		Description: "Only the invited player can answer this invitation.",
	})
}

var partyErrorTexts = []struct {
	err         error
	title, text string
}{
	{ErrPartiesDisabled, "Parties Disabled", "Parties are currently disabled on this server. If you think this is a mistake, contact an administrator."},
	{ErrAlreadyInParty, "Already in a Party", "You are already in a party. Use `/party_group leave` first."},
	{ErrTargetInParty, "Player Unavailable", "That player is already in a party. Please ask them to leave their current party before inviting them."},
	{ErrNotInParty, "Not in a Party", "You are not in a party. Use `/party_group create` to make one or ask a leader to invite you."},
	{ErrNotLeader, "Leader Only", "Only the party leader can do that. Use `/party_group info` to see who the leader is."},
	{ErrNotMember, "Not a Member", "That player is not in your party. Use `/party_group info` to see who is in your party."},
	{ErrSelfTarget, "Invalid Target", "You cannot do that to yourself. Please select another player."},
	{ErrPartyFull, "Party Full", "The party is full. Please remove a member before inviting another player."},
	{ErrPartyInvitesClosed, "Invites Closed", "This party is not accepting invites right now. Ask the leader to open invites with `/party_group invites` before inviting new players."},
	{ErrInvitesDisabled, "Invites Disabled", "That player has party invites turned off. Ask them to enable invites in their settings before inviting them."},
	{ErrNoInvite, "No Invitation", "There is no pending party invitation. Ask the leader to invite you again."},
	{ErrInviteExpired, "Invitation Expired", "That party invitation has expired. Ask the leader to invite you again."},
}

func PartyErrorEmbed(err error) *discordgo.MessageEmbed {
	var d *EloDenial
	if errors.As(err, &d) {
		return Preset("error", types.PresetOptions{
			Title:       "Party ELO Limit",
			Description: fmt.Sprintf("This party would exceed the ELO limit: the **%s** ELO would be **%d**, above the limit of **%d**.", d.Method, d.Aggregated, d.Max),
		})
	}
	for _, e := range partyErrorTexts {
		if errors.Is(err, e.err) {
			return Preset("error", types.PresetOptions{Title: e.title, Description: e.text})
		}
	}
	return nil
}

func partyErrorEmbedOrGeneric(err error) *discordgo.MessageEmbed {
	if e := PartyErrorEmbed(err); e != nil {
		return e
	}
	apputil.DiscordLogger.Error(fmt.Sprintf("[Party] button action failed: %v", err))
	return CommandErrorEmbed()
}

func PartyInfoEmbed(who string, fields []*discordgo.MessageEmbedField) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Party Information",
		Description: fmt.Sprintf("View the current party details for <@%s>.", who),
		Fields:      fields,
	})
}

func init() {
	Parties.SetNotify(deliverPartyNotice)
}

// partyWatch sweeps only guilds that have an active party. Create and Invite add a guild; a sweep drops it once
// no party is left.
var partyWatch = newGuildWatch("PartyManager", time.Minute, func(guildID string) bool {
	return Parties.sweepPending(guildID)
})

// StartPartySweeper tracks every guild the session is in that already has an active party; the ready event calls
// it. Guilds with none are not swept until a party is created.
func StartPartySweeper(s *discordgo.Session) {
	BotSession.Store(s)
	partySweepOnce.Do(func() {
		ids := make([]string, 0, len(s.State.Guilds))
		for _, g := range s.State.Guilds {
			ids = append(ids, g.ID)
		}
		go partyWatch.Seed(ids, Parties.hasParties)
	})
}

var partySweepOnce sync.Once

// PartyManager is the bot side of parties. The database API (sdk.PartyService)
// owns the state and enforces the membership rules atomically: one party per
// player, capacity, leader-only actions, invite state and expiry, leadership
// succession. This manager adds what only the bot can do: the config-driven
// ELO cap (checked before inviting/accepting), resolving the effective party
// config per queue, a short read cache for the queue hot path, translating API
// error codes into the Err* sentinels below, notifying affected players, and
// the sweeper (inactivity, expired invites). Players are identified by Discord
// snowflake everywhere.

// ============================================================================
// Types & errors
// ============================================================================

// Party is a party as the bot uses it. Members is join-ordered and includes the Leader.
type Party struct {
	ID            uint
	Leader        string
	Members       []string
	Invites       map[string]time.Time // pending invitee -> when the invite expires
	MaxSize       int
	GamesPlayed   int
	IsInvitesOpen bool
	LastActive    time.Time
}

// IsEffective reports whether the party counts as a group; a one-person party
// queues as an individual.
func (p *Party) IsEffective() bool { return len(p.Members) >= 2 }

func partyFromView(v *models.PartyView) *Party {
	p := &Party{
		ID: v.ID, Leader: v.LeaderSnowflakeID, MaxSize: v.MaxSize, GamesPlayed: v.GamesPlayed,
		IsInvitesOpen: v.IsInvitesOpen, LastActive: v.LastActiveAt, Invites: map[string]time.Time{},
	}
	for _, m := range v.Members {
		p.Members = append(p.Members, m.SnowflakeID)
	}
	for _, i := range v.Invites {
		p.Invites[i.InviteeSnowflakeID] = i.ExpiresAt
	}
	return p
}

// PartyGroup is defined in package types (parties.go).
type PartyGroup = types.PartyGroup

// EloDenial is returned (as an error) when a party would exceed the ELO cap.
type EloDenial struct {
	Max        int
	Aggregated int
	Method     string
	MemberElos []int
}

func (e *EloDenial) Error() string {
	return fmt.Sprintf("party %s ELO %d exceeds limit %d", e.Method, e.Aggregated, e.Max)
}

var (
	ErrPartiesDisabled    = errors.New("parties are disabled")
	ErrAlreadyInParty     = errors.New("already in a party")
	ErrTargetInParty      = errors.New("target is already in a party")
	ErrNotInParty         = errors.New("not in a party")
	ErrNotLeader          = errors.New("not the party leader")
	ErrNotMember          = errors.New("target is not in your party")
	ErrSelfTarget         = errors.New("cannot target yourself")
	ErrPartyFull          = errors.New("party is full")
	ErrPartyInvitesClosed = errors.New("party is not accepting invites")
	ErrNoInvite           = errors.New("no pending invite")
	ErrInviteExpired      = errors.New("invite expired")
	ErrInvitesDisabled    = errors.New("player has party invites disabled")
)

var partyCodeErrors = map[string]error{
	models.CodeAlreadyInParty:     ErrAlreadyInParty,
	models.CodeTargetInParty:      ErrTargetInParty,
	models.CodeNotInParty:         ErrNotInParty,
	models.CodeNotPartyLeader:     ErrNotLeader,
	models.CodeNotPartyMember:     ErrNotMember,
	models.CodeSelfTarget:         ErrSelfTarget,
	models.CodePartyFull:          ErrPartyFull,
	models.CodePartyInvitesClosed: ErrPartyInvitesClosed,
	models.CodePartyInvitesOff:    ErrInvitesDisabled,
	models.CodeNoPartyInvite:      ErrNoInvite,
	models.CodePartyInviteExpired: ErrInviteExpired,
}

// partyErr turns an API business-rule error into its Err* sentinel; anything
// else (network, 5xx) passes through unchanged.
func partyErr(err error) error {
	if err == nil {
		return nil
	}
	if e, ok := partyCodeErrors[sdk.ErrorCode(err)]; ok {
		return e
	}
	return err
}

// PartyNotice is defined in package types (parties.go).
type PartyNotice = types.PartyNotice

const (
	NoticeDisbandedByLeader PartyNotice = iota
	NoticeDisbandedLastMember
	NoticeDisbandedInactivity
	NoticeDisbandedSystemOff
	NoticeKicked
	NoticeLeaderChanged
)

// PartyAPI is defined in package types (parties.go).
type PartyAPI = types.PartyAPI

var _ PartyAPI = (*sdk.PartyService)(nil)

// PartyDeps is defined in package types (parties.go).
type PartyDeps = types.PartyDeps

const (
	partyCacheTTL    = 10 * time.Second
	partyTouchMinGap = 30 * time.Second
	actorSystem      = sdk.SystemActor
)

type partyCacheEntry struct {
	p   *Party // nil = "not in a party"
	exp time.Time
}

// PartyManager owns the bot's view of every guild's parties.
type PartyManager struct {
	deps PartyDeps

	mu      sync.Mutex
	cache   map[string]partyCacheEntry // guild:user -> party (10s)
	touched map[uint]time.Time         // party id -> last Touch sent
}

// Parties is the process-wide manager.
var Parties = NewPartyManager(PartyDeps{})

// NewPartyManager builds a manager, filling unset deps with the database-backed defaults.
func NewPartyManager(d PartyDeps) *PartyManager {
	cfg := newTTLCache[types.PartiesConfig](30 * time.Second)
	if d.Config == nil {
		d.Config = func(guildID string) types.PartiesConfig {
			c, _ := cfg.get(guildID, func() (types.PartiesConfig, error) { return loadPartiesConfig(guildID) })
			return normalizePartiesConfig(c)
		}
	}
	if d.Modes == nil {
		d.Modes = func(guildID string) []types.Mode { m, _ := GuildMetaModes(guildID); return m }
	}
	if d.Elo == nil {
		d.Elo = dbElo
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &PartyManager{deps: d, cache: map[string]partyCacheEntry{}, touched: map[uint]time.Time{}}
}

func (m *PartyManager) api() PartyAPI {
	if m.deps.API != nil {
		return m.deps.API
	}
	return database.Must().Party
}

// ============================================================================
// Config
// ============================================================================

// DefaultPartiesConfig mirrors the old bot's defaults.
func DefaultPartiesConfig() types.PartiesConfig {
	return types.PartiesConfig{
		MaxSize:                  4,
		InactivityDisbandTime:    15,
		InviteExpireSeconds:      300,
		MinQueueSize:             1,
		RequireAllMembersInQueue: true,
		EloAggregationMethod:     "average",
		EloModifierMultiplier:    1,
	}
}

// loadPartiesConfig reads the party limits the dashboard's Party Limits page
// saves (the guild config's flows.party). A guild that predates that page may
// still have them in the game config's party_settings, which is the fallback.
func loadPartiesConfig(guildID string) (types.PartiesConfig, error) {
	cfg := DefaultPartiesConfig() // Unmarshal only overwrites the keys present

	raw, err := database.Must().GuildConfig.Field(guildID, "flows")
	if err != nil && !sdk.IsNotFound(err) {
		return cfg, err
	}
	if len(raw) > 0 {
		var flows struct {
			Party json.RawMessage `json:"party"`
		}
		if err := json.Unmarshal(raw, &flows); err != nil {
			return DefaultPartiesConfig(), err
		}
		if len(flows.Party) > 0 && string(flows.Party) != "null" {
			if err := json.Unmarshal(flows.Party, &cfg); err != nil {
				return DefaultPartiesConfig(), err
			}
			return cfg, nil
		}
	}

	raw, err = database.Must().GameConfig.Field(guildID, "party_settings")
	if err != nil {
		if sdk.IsNotFound(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return DefaultPartiesConfig(), err
		}
	}
	return cfg, nil
}

func clampInt(v, lo, hi int) int { return max(lo, min(hi, v)) }

func normalizeEloMethod(m string) string {
	switch m = strings.ToLower(strings.TrimSpace(m)); m {
	case "highest", "sum", "leader":
		return m
	}
	return "average"
}

func normalizePartiesConfig(c types.PartiesConfig) types.PartiesConfig {
	c.MaxSize = clampInt(c.MaxSize, 2, 16)
	c.MinQueueSize = clampInt(c.MinQueueSize, 1, 16)
	c.MaxEloDifference = clampInt(c.MaxEloDifference, 0, 5000)
	c.InactivityDisbandTime = max(0, c.InactivityDisbandTime)
	if c.InviteExpireSeconds <= 0 {
		c.InviteExpireSeconds = 300
	}
	c.EloAggregationMethod = normalizeEloMethod(c.EloAggregationMethod)
	return c
}

// ResolveModePartyConfig applies a mode's overrides (nil fields inherit) on top
// of the guild config. Priority is mode > guild.
func ResolveModePartyConfig(base types.PartiesConfig, o *types.ModePartyConfig) types.PartiesConfig {
	base = normalizePartiesConfig(base)
	if o == nil {
		return base
	}
	if o.MaxSize != nil {
		base.MaxSize = *o.MaxSize
	}
	if o.MinQueueSize != nil {
		base.MinQueueSize = *o.MinQueueSize
	}
	if o.RequireAllMembersInQueue != nil {
		base.RequireAllMembersInQueue = *o.RequireAllMembersInQueue
	}
	if o.MaxEloDifference != nil {
		base.MaxEloDifference = *o.MaxEloDifference
	}
	if o.ExemptBoostersFromEloCap != nil {
		base.ExemptBoostersFromEloCap = *o.ExemptBoostersFromEloCap
	}
	if o.EloAggregationMethod != nil {
		base.EloAggregationMethod = *o.EloAggregationMethod
	}
	return normalizePartiesConfig(base)
}

func partiesEnabled(c types.PartiesConfig) bool { return c.IsEnabled == nil || *c.IsEnabled }

// mostPermissiveConfig is what invite/join checks use: they don't know which
// queue the party will end up in, so the loosest party-enabled mode wins and
// the strict per-mode check runs again when the queue pops.
func (m *PartyManager) mostPermissiveConfig(guildID string) types.PartiesConfig {
	cfg := m.deps.Config(guildID)
	for _, mode := range m.deps.Modes(guildID) {
		if !mode.IsPartyQueueEnabled || mode.Party == nil {
			continue
		}
		mc := ResolveModePartyConfig(cfg, mode.Party)
		cfg.MaxSize = max(cfg.MaxSize, mc.MaxSize)
		switch {
		case cfg.MaxEloDifference == 0: // already unlimited
		case mc.MaxEloDifference == 0:
			cfg.MaxEloDifference = 0
		default:
			cfg.MaxEloDifference = max(cfg.MaxEloDifference, mc.MaxEloDifference)
		}
	}
	return cfg
}

// Config returns the guild's normalized party settings.
func (m *PartyManager) Config(guildID string) types.PartiesConfig { return m.deps.Config(guildID) }

// ============================================================================
// ELO limit
// ============================================================================

// AggregateElo combines member ELOs with the configured method.
func AggregateElo(elos []int, method string, leaderElo int) float64 {
	if len(elos) == 0 {
		return 0
	}
	switch method {
	case "highest":
		return float64(slicesMax(elos))
	case "leader":
		return float64(leaderElo)
	}
	sum := 0
	for _, e := range elos {
		sum += e
	}
	if method == "sum" {
		return float64(sum)
	}
	return float64(sum) / float64(len(elos))
}

func slicesMax(v []int) int {
	m := v[0]
	for _, x := range v[1:] {
		m = max(m, x)
	}
	return m
}

// checkElo validates members (+ an optional newcomer) against cfg's ELO cap.
// It returns nil when allowed, and fails open when an ELO can't be read.
// The API doesn't know about this cap, so it is checked here, before the call.
func (m *PartyManager) checkElo(guildID string, members []string, leader, newcomer string, cfg types.PartiesConfig) *EloDenial {
	if cfg.MaxEloDifference <= 0 {
		return nil
	}
	all := append([]string(nil), members...)
	if newcomer != "" {
		all = append(all, newcomer)
	}
	if len(all) <= 1 {
		return nil
	}
	if cfg.ExemptBoostersFromEloCap && m.deps.BoostersExempt != nil && m.deps.BoostersExempt(guildID, all) {
		return nil
	}
	elos := make([]int, len(all))
	leaderElo := 0
	for i, id := range all {
		e, ok := m.deps.Elo(guildID, id)
		if !ok {
			return nil
		}
		elos[i] = e
		if id == leader {
			leaderElo = e
		}
	}
	agg := int(math.Round(AggregateElo(elos, cfg.EloAggregationMethod, leaderElo)))
	if agg <= cfg.MaxEloDifference {
		return nil
	}
	return &EloDenial{Max: cfg.MaxEloDifference, Aggregated: agg, Method: cfg.EloAggregationMethod, MemberElos: elos}
}

// ValidateExistingElo re-checks a formed party against cfg (a queue's per-mode
// config, or nil for the most permissive one). Returns nil when allowed.
func (m *PartyManager) ValidateExistingElo(guildID string, p *Party, cfg *types.PartiesConfig) *EloDenial {
	c := m.mostPermissiveConfig(guildID)
	if cfg != nil {
		c = *cfg
	}
	return m.checkElo(guildID, p.Members, p.Leader, "", c)
}

// ============================================================================
// Lookups
// ============================================================================

// fresh reads userID's party straight from the database (nil = none).
func (m *PartyManager) fresh(guildID, userID string) (*Party, error) {
	v, err := m.api().ByPlayer(guildID, userID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return partyFromView(v), nil
}

// PartyOf returns the party userID is in, or nil. It reads through a 10-second
// cache (the queue asks for every member on every formation attempt); every
// change made through this manager clears it. A database error counts as "not
// in a party" for this call and is logged.
func (m *PartyManager) PartyOf(guildID, userID string) *Party {
	key, now := guildID+":"+userID, m.deps.Now()
	m.mu.Lock()
	if e, ok := m.cache[key]; ok && now.Before(e.exp) {
		m.mu.Unlock()
		return e.p
	}
	m.mu.Unlock()

	p, err := m.fresh(guildID, userID)
	if err != nil {
		appLog.EngineLogger.Error(fmt.Sprintf("[PartyManager] party lookup for %s in %s failed: %v", userID, guildID, err))
		return nil
	}
	m.mu.Lock()
	m.cache[key] = partyCacheEntry{p, now.Add(partyCacheTTL)}
	m.mu.Unlock()
	return p
}

func (m *PartyManager) IsInParty(guildID, userID string) bool {
	return m.PartyOf(guildID, userID) != nil
}

// GuildParties returns every active party in the guild.
func (m *PartyManager) GuildParties(guildID string) ([]*Party, error) {
	vs, err := m.api().ListActive(guildID, time.Time{})
	if err != nil {
		return nil, err
	}
	out := make([]*Party, len(vs))
	for i := range vs {
		out[i] = partyFromView(&vs[i])
	}
	return out, nil
}

// HasPendingInvite reports whether userID holds an unexpired invite from leaderID's party.
func (m *PartyManager) HasPendingInvite(guildID, leaderID, userID string) bool {
	p, err := m.fresh(guildID, leaderID)
	if err != nil || p == nil {
		return false
	}
	exp, ok := p.Invites[userID]
	return ok && m.deps.Now().Before(exp)
}

// BuildMatchParties returns the parties with at least two members on roster,
// each trimmed to the members that are on it.
func (m *PartyManager) BuildMatchParties(guildID string, roster []string) []PartyGroup {
	if !partiesEnabled(m.deps.Config(guildID)) {
		return nil
	}
	on := make(map[string]bool, len(roster))
	for _, id := range roster {
		on[id] = true
	}
	seen := map[uint]bool{}
	var out []PartyGroup
	for _, id := range roster {
		p := m.PartyOf(guildID, id)
		if p == nil || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		var in []string
		for _, mem := range p.Members {
			if on[mem] {
				in = append(in, mem)
			}
		}
		if len(in) >= 2 {
			out = append(out, PartyGroup{Leader: p.Leader, Members: in})
		}
	}
	return out
}

// ============================================================================
// Mutations
// ============================================================================

func (m *PartyManager) invalidate(guildID string) {
	prefix := guildID + ":"
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.cache {
		if strings.HasPrefix(k, prefix) {
			delete(m.cache, k)
		}
	}
}

func (m *PartyManager) notify(guildID string, n PartyNotice, userIDs ...string) {
	m.mu.Lock()
	fn := m.deps.Notify
	m.mu.Unlock()
	if fn == nil {
		return
	}
	for _, id := range userIDs {
		fn(guildID, id, n)
	}
}

var disbandNotices = map[string]PartyNotice{
	models.DisbandLastMember: NoticeDisbandedLastMember,
	models.DisbandInactivity: NoticeDisbandedInactivity,
	models.DisbandSystemOff:  NoticeDisbandedSystemOff,
}

// announce delivers the notices a PartyChange implies. kicked is the player
// removed by a kick ("" otherwise).
func (m *PartyManager) announce(guildID string, ch *models.PartyChange, kicked string) {
	if ch == nil {
		return
	}
	if kicked != "" {
		m.notify(guildID, NoticeKicked, kicked)
	}
	if ch.Disbanded {
		n, ok := disbandNotices[ch.DisbandReason]
		if !ok {
			n = NoticeDisbandedByLeader
		}
		m.notify(guildID, n, ch.Affected...)
		return
	}
	if ch.NewLeader != "" {
		m.notify(guildID, NoticeLeaderChanged, ch.NewLeader)
	}
}

// Create makes a new party led by leaderID.
func (m *PartyManager) Create(guildID, leaderID string) (*Party, error) {
	cfg := m.mostPermissiveConfig(guildID)
	if !partiesEnabled(cfg) {
		return nil, ErrPartiesDisabled
	}
	v, err := m.api().Create(leaderID, guildID, leaderID, cfg.MaxSize)
	m.invalidate(guildID)
	if err != nil {
		return nil, partyErr(err)
	}
	partyWatch.Track(guildID)
	return partyFromView(v), nil
}

// Invite invites targetID to inviterID's party, creating the party first when
// the inviter isn't in one (autoCreated; it is disbanded again if the invite
// then fails). Re-inviting refreshes that invite's expiry.
func (m *PartyManager) Invite(guildID, inviterID, targetID string) (party *Party, autoCreated bool, err error) {
	if inviterID == targetID {
		return nil, false, ErrSelfTarget
	}
	cfg := m.mostPermissiveConfig(guildID)
	if !partiesEnabled(cfg) {
		return nil, false, ErrPartiesDisabled
	}
	cur, err := m.fresh(guildID, inviterID)
	if err != nil {
		return nil, false, err
	}
	members, leader := []string{inviterID}, inviterID
	if cur != nil {
		members, leader = cur.Members, cur.Leader
	}
	// ponytail: the ELO cap is checked here, then the API call runs; a member
	// joining in that gap can slip past it. The queue re-checks at pop time.
	if d := m.checkElo(guildID, members, leader, targetID, cfg); d != nil {
		return nil, false, d
	}
	if cur == nil {
		v, err := m.api().Create(inviterID, guildID, inviterID, cfg.MaxSize)
		if err != nil {
			return nil, false, partyErr(err)
		}
		cur, autoCreated = partyFromView(v), true
	}
	expiry := time.Duration(cfg.InviteExpireSeconds) * time.Second
	if _, err := m.api().Invite(inviterID, guildID, cur.ID, inviterID, targetID, expiry); err != nil {
		if autoCreated { // don't leave an empty party behind
			_, _ = m.api().Disband(inviterID, guildID, cur.ID, inviterID, models.DisbandByLeader)
		}
		m.invalidate(guildID)
		return nil, false, partyErr(err)
	}
	m.invalidate(guildID)
	partyWatch.Track(guildID)
	v, err := m.api().Get(guildID, cur.ID)
	if err != nil {
		return nil, autoCreated, err
	}
	return partyFromView(v), autoCreated, nil
}

// Accept joins userID to leaderID's party using a pending invite.
func (m *PartyManager) Accept(guildID, leaderID, userID string) (*Party, error) {
	cfg := m.mostPermissiveConfig(guildID)
	if !partiesEnabled(cfg) {
		return nil, ErrPartiesDisabled
	}
	p, err := m.fresh(guildID, leaderID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrNoInvite
	}
	if d := m.checkElo(guildID, p.Members, p.Leader, userID, cfg); d != nil {
		return nil, d
	}
	v, err := m.api().Accept(userID, guildID, p.ID, userID)
	m.invalidate(guildID)
	if err != nil {
		return nil, partyErr(err)
	}
	return partyFromView(v), nil
}

// Decline drops userID's invite from leaderID's party.
func (m *PartyManager) Decline(guildID, leaderID, userID string) error {
	p, err := m.fresh(guildID, leaderID)
	if err != nil {
		return err
	}
	if p == nil {
		return ErrNoInvite
	}
	err = m.api().Decline(userID, guildID, p.ID, userID)
	m.invalidate(guildID)
	return partyErr(err)
}

// Leave removes userID from their party (leadership passes on if they led it).
func (m *PartyManager) Leave(guildID, userID string) error {
	ch, err := m.api().Leave(userID, guildID, userID)
	m.invalidate(guildID)
	if err != nil {
		return partyErr(err)
	}
	m.announce(guildID, ch, "")
	return nil
}

// ledParty loads the party leaderID leads, or the error a leader-only action would give.
func (m *PartyManager) ledParty(guildID, leaderID string) (*Party, error) {
	p, err := m.fresh(guildID, leaderID)
	switch {
	case err != nil:
		return nil, err
	case p == nil:
		return nil, ErrNotInParty
	case p.Leader != leaderID:
		return nil, ErrNotLeader
	}
	return p, nil
}

// Kick removes targetID from leaderID's party.
func (m *PartyManager) Kick(guildID, leaderID, targetID string) error {
	p, err := m.ledParty(guildID, leaderID)
	if err != nil {
		return err
	}
	ch, err := m.api().Kick(leaderID, guildID, p.ID, targetID, leaderID)
	m.invalidate(guildID)
	if err != nil {
		return partyErr(err)
	}
	m.announce(guildID, ch, targetID)
	return nil
}

// Promote hands leadership from leaderID to targetID.
func (m *PartyManager) Promote(guildID, leaderID, targetID string) error {
	p, err := m.ledParty(guildID, leaderID)
	if err != nil {
		return err
	}
	_, err = m.api().TransferLeadership(leaderID, guildID, p.ID, leaderID, targetID)
	m.invalidate(guildID)
	return partyErr(err)
}

// Disband ends leaderID's party and tells the other members.
func (m *PartyManager) Disband(guildID, leaderID string) error {
	p, err := m.ledParty(guildID, leaderID)
	if err != nil {
		return err
	}
	ch, err := m.api().Disband(leaderID, guildID, p.ID, leaderID, models.DisbandByLeader)
	m.invalidate(guildID)
	if err != nil {
		return partyErr(err)
	}
	m.announce(guildID, ch, "")
	return nil
}

// SetInvitesOpen lets the leader pause or resume invites without disbanding.
func (m *PartyManager) SetInvitesOpen(guildID, leaderID string, open bool) error {
	p, err := m.ledParty(guildID, leaderID)
	if err != nil {
		return err
	}
	err = m.api().SetInvitesOpen(leaderID, guildID, p.ID, open)
	m.invalidate(guildID)
	return partyErr(err)
}

// SetNotify installs the delivery function for PartyNotices (the /party
// command posts them to its party_alerts channel).
func (m *PartyManager) SetNotify(fn func(guildID, userID string, n PartyNotice)) {
	m.mu.Lock()
	m.deps.Notify = fn
	m.mu.Unlock()
}

// Touch resets the party's inactivity timer (call when it queues or plays). It
// is rate-limited to one API write per party per 30 seconds.
func (m *PartyManager) Touch(guildID, userID string) {
	p := m.PartyOf(guildID, userID)
	if p == nil {
		return
	}
	now := m.deps.Now()
	m.mu.Lock()
	if last, ok := m.touched[p.ID]; ok && now.Sub(last) < partyTouchMinGap {
		m.mu.Unlock()
		return
	}
	m.touched[p.ID] = now
	m.mu.Unlock()
	if err := m.api().Touch(actorSystem, guildID, p.ID); err != nil && !sdk.IsNotFound(err) {
		appLog.EngineLogger.Error(fmt.Sprintf("[PartyManager] touch party %d: %v", p.ID, err))
	}
}

// RecordGame counts a finished game for the party userID is in.
func (m *PartyManager) RecordGame(guildID, userID string) {
	if p := m.PartyOf(guildID, userID); p != nil {
		if err := m.api().RecordGame(actorSystem, guildID, p.ID); err != nil && !sdk.IsNotFound(err) {
			appLog.EngineLogger.Error(fmt.Sprintf("[PartyManager] record game for party %d: %v", p.ID, err))
		}
	}
}

// PurgeGuild forgets the guild's cached parties (bot left the guild). The
// parties themselves stay in the database.
func (m *PartyManager) PurgeGuild(guildID string) { m.invalidate(guildID) }

// ============================================================================
// Sweeper: expired invites + inactivity
// ============================================================================

// Sweep expires stale invites and disbands inactive parties in one guild
// (never while a member is mid-game — that touches the party instead). With
// parties turned off for the guild, all of its parties are disbanded.
func (m *PartyManager) Sweep(guildID string) {
	cfg := m.deps.Config(guildID)
	if _, err := m.api().ExpireInvites(actorSystem, guildID); err != nil {
		appLog.EngineLogger.Error(fmt.Sprintf("[PartyManager] expire invites in %s: %v", guildID, err))
	}

	var before time.Time
	reason := models.DisbandInactivity
	switch {
	case !partiesEnabled(cfg):
		reason = models.DisbandSystemOff
	case cfg.InactivityDisbandTime > 0:
		before = m.deps.Now().Add(-time.Duration(cfg.InactivityDisbandTime) * time.Minute)
	default:
		return
	}
	parties, err := m.api().ListActive(guildID, before)
	if err != nil {
		appLog.EngineLogger.Error(fmt.Sprintf("[PartyManager] list parties in %s: %v", guildID, err))
		return
	}
	for i := range parties {
		p := partyFromView(&parties[i])
		if reason == models.DisbandInactivity && m.anyInGame(guildID, p.Members) {
			if err := m.api().Touch(actorSystem, guildID, p.ID); err != nil {
				appLog.EngineLogger.Error(fmt.Sprintf("[PartyManager] touch party %d: %v", p.ID, err))
			}
			continue
		}
		ch, err := m.api().Disband(actorSystem, guildID, p.ID, "", reason)
		if err != nil {
			appLog.EngineLogger.Error(fmt.Sprintf("[PartyManager] disband party %d: %v", p.ID, err))
			continue
		}
		m.announce(guildID, ch, "")
	}
	m.invalidate(guildID)
}

func (m *PartyManager) anyInGame(guildID string, members []string) bool {
	if m.deps.InGame == nil {
		return false
	}
	for _, id := range members {
		if m.deps.InGame(guildID, id) {
			return true
		}
	}
	return false
}

// hasParties reports whether the guild has an active party. An error counts as yes, so a hiccup never drops a guild.
func (m *PartyManager) hasParties(guildID string) bool {
	parties, err := m.api().ListActive(guildID, time.Time{})
	return err != nil || len(parties) > 0
}

// sweepPending runs Sweep and reports whether the guild still has parties (and so invites) to watch.
func (m *PartyManager) sweepPending(guildID string) bool {
	m.Sweep(guildID)
	return m.hasParties(guildID)
}

// ============================================================================
// Database-backed defaults
// ============================================================================

func dbElo(guildID, userID string) (int, bool) {
	st, err := database.Must().Stats.Get(guildID, userID, 0)
	if err != nil {
		if sdk.IsNotFound(err) {
			return 0, true // no stats yet counts as 0, as before
		}
		return 0, false
	}
	return st.Elo, true
}

// ============================================================================
// Small TTL cache shared by the managers
// ============================================================================

type ttlEntry[T any] struct {
	v   T
	exp time.Time
}

type ttlCache[T any] struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]ttlEntry[T]
}

func newTTLCache[T any](ttl time.Duration) *ttlCache[T] {
	return &ttlCache[T]{ttl: ttl, m: map[string]ttlEntry[T]{}}
}

// get returns the cached value or loads it; failed loads are not cached.
func (c *ttlCache[T]) get(key string, load func() (T, error)) (T, error) {
	c.mu.Lock()
	if e, ok := c.m[key]; ok && time.Now().Before(e.exp) {
		c.mu.Unlock()
		return e.v, nil
	}
	c.mu.Unlock()
	v, err := load()
	if err != nil {
		return v, err
	}
	c.mu.Lock()
	c.m[key] = ttlEntry[T]{v, time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return v, nil
}

func (c *ttlCache[T]) delete(key string) {
	c.mu.Lock()
	delete(c.m, key)
	c.mu.Unlock()
}
