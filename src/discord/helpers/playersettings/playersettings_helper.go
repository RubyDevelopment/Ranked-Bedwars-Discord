package playersettings

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/partymgr"
	"ranked-bedwars/src/discord/helpers/ratings"
	"ranked-bedwars/src/types"
)

// A player's own switches (/settings, one V2 container with a button beside each switch): what others can see of them, who can invite them and when the bot pings them. A guild can
// lock a switch or limit it to roles, perks or boosters (the dashboard's Settings Restrictions). The buttons of the /settings
// answer and the party autowarp live here.

// ============================================================================
// Settings
// ============================================================================

// Setting is one switch.
type Setting struct {
	ID          string
	Emoji       string
	Label       string
	Column      string // the player config column it is stored in
	Restriction string // its key in the guild's settings restrictions ("" = a guild cannot restrict it)
	On, Off     string // what the state is called
	Detail      string
}

// Settings are the switches in the order the answer lists them.
var Settings = []Setting{
	{ID: "stats", Emoji: "📊", Label: "Stats Visibility", Column: "is_stats_visible", Restriction: "stats_visibility", On: "Public", Off: "Hidden",
		Detail: "Whether other players can look at your stats and profile."},
	{ID: "elo", Emoji: "📈", Label: "ELO Visibility", Column: "is_elo_visible", Restriction: "elo_visibility", On: "Public", Off: "Hidden",
		Detail: "Whether your ELO shows in your server nickname and on your profile."},
	{ID: "invites", Emoji: "📨", Label: "Party Invites", Column: "is_party_invites_enabled", Restriction: "party_invites", On: "Enabled", Off: "Disabled",
		Detail: "Whether other players can invite you to their party."},
	{ID: "autowarp", Emoji: "🚀", Label: "Party Autowarp", Column: "is_party_autowarp_enabled", Restriction: "party_autowarp", On: "Enabled", Off: "Disabled",
		Detail: "When you lead a party and join a voice channel, your party members who are in voice are moved with you."},
	{ID: "results", Emoji: "🔔", Label: "Ping On Results", Column: "is_ping_on_results_enabled", Restriction: "result_pings", On: "Yes", Off: "No",
		Detail: "Whether you are pinged when a game you played is scored."},
	{ID: "alerts", Emoji: "📣", Label: "Ping On Alerts", Column: "is_ping_on_alerts_enabled", On: "Yes", Off: "No",
		Detail: "Whether party, queue and game alerts ping you."},
}

func find(id string) *Setting {
	for n := range Settings {
		if Settings[n].ID == id {
			return &Settings[n]
		}
	}
	return nil
}

// Value is the state of one switch in the player's config.
func (st Setting) Value(c *models.PlayerConfigsModel) bool {
	switch st.ID {
	case "stats":
		return c.IsStatsVisible
	case "elo":
		return c.IsEloVisible
	case "invites":
		return c.IsPartyInvitesEnabled
	case "autowarp":
		return c.IsPartyAutowarpEnabled
	case "results":
		return c.IsPingOnResultsEnabled
	case "alerts":
		return c.IsPingOnAlertsEnabled
	}
	return false
}

// ============================================================================
// Restrictions
// ============================================================================

// restriction is how a guild limits one switch: roles, perks or boosters may use it, or nobody can.
type restriction struct {
	AllowedRoles   []string `json:"allowed_roles"`
	AllowedPerks   []string `json:"allowed_perks"`
	RequireBooster bool     `json:"require_booster"`
	IsLocked       bool     `json:"is_locked"`
}

var perkResolver = ratings.NewPerkResolver(partymgr.BotSession.Load)

// Allowed reports which switches the member may change. Administrators are not exempt: only the configured roles, perks,
// boosters and locks count, as in the old bot.
func Allowed(guildID, userID string, roles []string) map[string]bool {
	allowed := make(map[string]bool, len(Settings))
	for _, st := range Settings {
		allowed[st.ID] = true
	}
	raw, err := database.Must().GuildConfig.Field(guildID, "settings_restrictions")
	if err != nil || len(raw) == 0 {
		return allowed
	}
	var rules map[string]restriction
	if json.Unmarshal(raw, &rules) != nil {
		return allowed
	}
	var perks *ratings.PerkContext // looked up once, and only when a rule needs it
	for _, st := range Settings {
		rule, ok := rules[st.Restriction]
		if st.Restriction == "" || !ok {
			continue
		}
		if rule.IsLocked {
			allowed[st.ID] = false
			continue
		}
		if len(rule.AllowedRoles) == 0 && len(rule.AllowedPerks) == 0 && !rule.RequireBooster {
			continue // no gate on this switch
		}
		ok = slices.Contains(rule.AllowedRoles, guildID) // @everyone
		for _, role := range rule.AllowedRoles {
			ok = ok || slices.Contains(roles, role)
		}
		if !ok && (len(rule.AllowedPerks) > 0 || rule.RequireBooster) {
			if perks == nil {
				ctx := perkResolver(guildID, userID)
				perks = &ctx
			}
			for _, perk := range perks.Perks {
				ok = ok || slices.Contains(rule.AllowedPerks, perk.Name)
			}
			ok = ok || (rule.RequireBooster && perks.BoosterStacks > 0)
		}
		allowed[st.ID] = ok
	}
	return allowed
}

// ============================================================================
// The answer
// ============================================================================

const idPrefix = "pset:"

// maxComponents is how many components Discord allows in one message.
const maxComponents = 40

// Panel is the answer: one container laid out like the embed it replaced. At the top the title and the note, with the player's head on
// the right; then one section per switch, its name, state and what it does on the left and its button on the far right. The button is
// an icon (the same one as in the text beside it): green when on, grey when off, disabled when the guild locks it.
func Panel(c *models.PlayerConfigsModel, allowed map[string]bool) []discordgo.MessageComponent {
	locked := false
	for _, st := range Settings {
		if !allowed[st.ID] {
			locked = true
		}
	}
	note := "Press the button beside a setting to switch it. Green is on and grey is off."
	if locked {
		note += " A 🔒 means this guild's rules do not let you change that setting."
	}
	children := []discordgo.MessageComponent{
		discordgo.Section{
			Components: []discordgo.MessageComponent{discordgo.TextDisplay{Content: "## Player Settings · " + c.Username + "\n" + note}},
			Accessory:  discordgo.Thumbnail{Media: discordgo.UnfurledMediaItem{URL: helpers.MinecraftBustAvatarURL(c.Username)}},
		},
	}
	// A section is three components (itself, its text, its button). The divider under the title only fits while the message stays within the limit.
	if 1+3+1+3*len(Settings) <= maxComponents {
		divider, spacing := true, discordgo.SeparatorSpacingSizeSmall
		children = append(children, discordgo.Separator{Divider: &divider, Spacing: &spacing})
	}
	for _, st := range Settings {
		on := st.Value(c)
		dot, state, style := helpers.DotRed, st.Off, discordgo.SecondaryButton
		if on {
			dot, state, style = helpers.DotGreen, st.On, discordgo.SuccessButton
		}
		lock := ""
		if !allowed[st.ID] {
			lock = " 🔒"
		}
		children = append(children, discordgo.Section{
			Components: []discordgo.MessageComponent{discordgo.TextDisplay{Content: fmt.Sprintf("**%s %s**\n%s **%s**%s\n> %s", st.Emoji, st.Label, dot, state, lock, st.Detail)}},
			Accessory: discordgo.Button{
				Style: style, CustomID: idPrefix + st.ID, Emoji: &discordgo.ComponentEmoji{Name: st.Emoji}, Disabled: !allowed[st.ID],
			},
		})
	}
	accent := helpers.ColorInfo
	return []discordgo.MessageComponent{discordgo.Container{AccentColor: &accent, Components: children}}
}

// editV2 fills the reply the command (or a button press) is answering with a V2 message. A prefix command has no interaction to edit, so
// it gets a message of its own, in reply to what was typed.
func editV2(s *discordgo.Session, i *discordgo.InteractionCreate, components []discordgo.MessageComponent) error {
	if helpers.IsPrefixInteraction(i) {
		_, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
			Flags:      discordgo.MessageFlagsIsComponentsV2,
			Components: components,
			Reference:  &discordgo.MessageReference{MessageID: helpers.PrefixSourceMessageID(i), ChannelID: i.ChannelID, GuildID: i.GuildID},
		})
		return err
	}
	_, err := s.RequestWithBucketID(
		"PATCH",
		discordgo.EndpointInteractionResponseActions(s.State.User.ID, i.Interaction.Token),
		map[string]any{"flags": discordgo.MessageFlagsIsComponentsV2, "components": components},
		discordgo.EndpointInteractionResponseActions(s.State.User.ID, ""),
	)
	return err
}

// Show is what /settings answers with: the player's settings and the buttons.
func Show(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	userID := helpers.InteractionUserID(i)
	view, err := database.Must().Player.Get(i.GuildID, userID)
	if err != nil || view.Config == nil {
		if err == nil || sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, notRegisteredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, errorEmbed())
	}
	allowed := Allowed(i.GuildID, userID, memberRoles(i))
	return editV2(s, i, Panel(view.Config, allowed))
}

func memberRoles(i *discordgo.InteractionCreate) []string {
	if i.Member == nil {
		return nil
	}
	return i.Member.Roles
}

func notRegisteredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register before you can manage your settings. Run `/register` to link your Minecraft account first.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func errorEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while updating your settings. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func restrictedEmbed(st *Setting) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Setting Restricted",
		Description: fmt.Sprintf("You do not meet the role, perk or booster requirements to change **%s**. Contact staff if you think this is a mistake.", st.Label),
		Thumbnail:   helpers.ThumbFlag,
	})
}

// ============================================================================
// Buttons
// ============================================================================

func init() { helpers.RegisterComponentHandler(handleButton) }

// handleButton flips the switch of the button that was pressed and redraws the answer.
func handleButton(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, idPrefix) {
		return false
	}
	st := find(strings.TrimPrefix(customID, idPrefix))
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})
	if st == nil {
		return true
	}
	userID := helpers.InteractionUserID(i)
	view, err := database.Must().Player.Get(i.GuildID, userID)
	if err != nil || view.Config == nil {
		helpers.FollowupEmbed(s, i, notRegisteredEmbed())
		return true
	}
	allowed := Allowed(i.GuildID, userID, memberRoles(i))
	if !allowed[st.ID] {
		_ = editV2(s, i, Panel(view.Config, allowed))
		helpers.FollowupEmbed(s, i, restrictedEmbed(st))
		return true
	}
	config, err := database.Must().Player.UpdateConfig(userID, i.GuildID, userID, map[string]any{st.Column: !st.Value(view.Config)})
	if err != nil || config == nil {
		helpers.FollowupEmbed(s, i, errorEmbed())
		return true
	}
	_ = editV2(s, i, Panel(config, allowed))
	if st.ID == "elo" {
		// The ELO in the server nickname follows the switch right away.
		go func() { _, _ = helpers.SyncRegisteredPlayerProfile(s, i.GuildID, view.Player.ID, userID) }()
	}
	return true
}

// ============================================================================
// Party autowarp
// ============================================================================

var autowarpAt = struct {
	sync.Mutex
	by map[string]time.Time
}{by: map[string]time.Time{}}

// HandleAutowarp moves a party along with its leader: when the leader joins a voice channel and has Party Autowarp on, every party
// member who is in voice is moved into it.
func HandleAutowarp(s *discordgo.Session, v *discordgo.VoiceStateUpdate) {
	if v.GuildID == "" || v.ChannelID == "" || (v.BeforeUpdate != nil && v.BeforeUpdate.ChannelID == v.ChannelID) {
		return
	}
	party := partymgr.Parties.PartyOf(v.GuildID, v.UserID)
	if party == nil || party.Leader != v.UserID || len(party.Members) < 2 {
		return
	}
	view, err := database.Must().Player.Get(v.GuildID, v.UserID)
	if err != nil || view.Config == nil || !view.Config.IsPartyAutowarpEnabled {
		return
	}
	key := v.GuildID + ":" + v.UserID
	autowarpAt.Lock()
	recent := time.Since(autowarpAt.by[key]) < 2*time.Second
	if !recent {
		autowarpAt.by[key] = time.Now()
	}
	autowarpAt.Unlock()
	if recent {
		return
	}
	for _, member := range party.Members {
		if member == v.UserID {
			continue
		}
		if state, err := s.State.VoiceState(v.GuildID, member); err == nil && state.ChannelID != "" && state.ChannelID != v.ChannelID {
			channel := v.ChannelID
			_ = s.GuildMemberMove(v.GuildID, member, &channel)
		}
	}
}
