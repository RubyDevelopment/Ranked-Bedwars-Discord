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

// A player's own switches (/settings): what others can see of them, who can invite them and when the bot pings them. A guild can
// lock a switch or limit it to roles, perks or boosters (the dashboard's Settings Restrictions). The buttons of the /settings
// answer and the party autowarp live here.

// ============================================================================
// Settings
// ============================================================================

// Setting is one switch.
type Setting struct {
	ID          string
	Emoji       string
	Button      string // the short name on the button
	Label       string
	Column      string // the player config column it is stored in
	Restriction string // its key in the guild's settings restrictions ("" = a guild cannot restrict it)
	On, Off     string // what the state is called
	Detail      string
}

// Settings are the switches in the order the answer lists them.
var Settings = []Setting{
	{ID: "stats", Emoji: "📊", Button: "Stats", Label: "Stats Visibility", Column: "is_stats_visible", Restriction: "stats_visibility", On: "Public", Off: "Hidden",
		Detail: "Whether other players can look at your stats and profile."},
	{ID: "elo", Emoji: "📈", Button: "ELO", Label: "ELO Visibility", Column: "is_elo_visible", Restriction: "elo_visibility", On: "Public", Off: "Hidden",
		Detail: "Whether your ELO shows in your server nickname and on your profile."},
	{ID: "invites", Emoji: "📨", Button: "Invites", Label: "Party Invites", Column: "is_party_invites_enabled", Restriction: "party_invites", On: "Enabled", Off: "Disabled",
		Detail: "Whether other players can invite you to their party."},
	{ID: "autowarp", Emoji: "🚀", Button: "Autowarp", Label: "Party Autowarp", Column: "is_party_autowarp_enabled", Restriction: "party_autowarp", On: "Enabled", Off: "Disabled",
		Detail: "When you lead a party and join a voice channel, your party members who are in voice are moved with you."},
	{ID: "results", Emoji: "🔔", Button: "Result Pings", Label: "Ping On Results", Column: "is_ping_on_results_enabled", Restriction: "result_pings", On: "Yes", Off: "No",
		Detail: "Whether you are pinged when a game you played is scored."},
	{ID: "alerts", Emoji: "📣", Button: "Alert Pings", Label: "Ping On Alerts", Column: "is_ping_on_alerts_enabled", On: "Yes", Off: "No",
		Detail: "Whether party and queue alerts ping you."},
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

// Embed lists every switch with its state and what it does.
func Embed(c *models.PlayerConfigsModel, allowed map[string]bool) *discordgo.MessageEmbed {
	fields := make([]*discordgo.MessageEmbedField, 0, len(Settings))
	locked := false
	for _, st := range Settings {
		on := st.Value(c)
		dot, state := helpers.DotRed, st.Off
		if on {
			dot, state = helpers.DotGreen, st.On
		}
		lock := ""
		if !allowed[st.ID] {
			lock, locked = " 🔒", true
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  st.Emoji + " " + st.Label,
			Value: fmt.Sprintf("%s **%s**%s\n> %s", dot, state, lock, st.Detail),
		})
	}
	note := "Press a button below to switch a setting. Green buttons are on and grey buttons are off."
	if locked {
		note += " A 🔒 means this guild's rules do not let you change that setting."
	}
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Player Settings · " + c.Username,
		Description: note,
		Thumbnail:   helpers.MinecraftBustAvatarURL(c.Username),
		Fields:      fields,
	})
}

// Components is one button per switch, three to a row: green when on, grey when off, disabled when the guild locks it.
func Components(c *models.PlayerConfigsModel, allowed map[string]bool) []discordgo.MessageComponent {
	var rows []discordgo.MessageComponent
	row := discordgo.ActionsRow{}
	for _, st := range Settings {
		style := discordgo.SecondaryButton
		if st.Value(c) {
			style = discordgo.SuccessButton
		}
		row.Components = append(row.Components, discordgo.Button{
			Label: st.Button, Style: style, CustomID: idPrefix + st.ID, Emoji: &discordgo.ComponentEmoji{Name: st.Emoji}, Disabled: !allowed[st.ID],
		})
		if len(row.Components) == 3 {
			rows = append(rows, row)
			row = discordgo.ActionsRow{}
		}
	}
	if len(row.Components) > 0 {
		rows = append(rows, row)
	}
	return rows
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
	embeds, components := []*discordgo.MessageEmbed{Embed(view.Config, allowed)}, Components(view.Config, allowed)
	return helpers.EditInteractionResponse(s, i, &discordgo.WebhookEdit{Embeds: &embeds, Components: &components})
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
		embeds, components := []*discordgo.MessageEmbed{Embed(view.Config, allowed)}, Components(view.Config, allowed)
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &embeds, Components: &components})
		helpers.FollowupEmbed(s, i, restrictedEmbed(st))
		return true
	}
	config, err := database.Must().Player.UpdateConfig(userID, i.GuildID, userID, map[string]any{st.Column: !st.Value(view.Config)})
	if err != nil || config == nil {
		helpers.FollowupEmbed(s, i, errorEmbed())
		return true
	}
	embeds, components := []*discordgo.MessageEmbed{Embed(config, allowed)}, Components(config, allowed)
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &embeds, Components: &components})
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
