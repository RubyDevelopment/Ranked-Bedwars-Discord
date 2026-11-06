package staff

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/panels"
	"ranked-bedwars/src/types"
)

func init() { helpers.Register(GiveawayCommand) }

// GiveawayCommand makes and runs giveaways: for any server, and with the ranked ladder's rules (ELO, rank, games, wins) for a Bedwars one.
// The dashboard (Extra → Giveaways) makes the same giveaways; this is for doing it from Discord.
var GiveawayCommand = &types.Command{
	Name:         "giveaway",
	Requirements: types.StaffRequirements,
	Description:  "Makes giveaways, draws them early, rerolls winners and lists them.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Autocomplete: autocompleteGiveaway,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name: "create", Description: "Post a giveaway", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "prize", Description: "What is given away", Type: discordgo.ApplicationCommandOptionString, Required: true, MaxLength: 200},
					{Name: "duration", Description: "How long it runs: 30m, 2h, 3d, 1w, 1d12h", Type: discordgo.ApplicationCommandOptionString, Required: true},
					{Name: "winners", Description: "How many winners (default 1)", Type: discordgo.ApplicationCommandOptionInteger, MinValue: floatPtr(1), MaxValue: 50},
					{Name: "channel", Description: "Where to post it (default: this channel)", Type: discordgo.ApplicationCommandOptionChannel, ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews}},
					{Name: "description", Description: "Shown on the post, above the rules", Type: discordgo.ApplicationCommandOptionString, MaxLength: 1000},
					{Name: "required_role", Description: "Only members with this role can enter", Type: discordgo.ApplicationCommandOptionRole},
					{Name: "blocked_role", Description: "Members with this role cannot enter", Type: discordgo.ApplicationCommandOptionRole},
					{Name: "prize_role", Description: "A role the winners are given", Type: discordgo.ApplicationCommandOptionRole},
					{Name: "min_join_days", Description: "Days a member must have been in the server", Type: discordgo.ApplicationCommandOptionInteger, MinValue: floatPtr(1), MaxValue: 3650},
					{Name: "boosters_only", Description: "Only server boosters can enter", Type: discordgo.ApplicationCommandOptionBoolean},
					{Name: "registered_only", Description: "Only registered ranked players can enter", Type: discordgo.ApplicationCommandOptionBoolean},
					{Name: "min_elo", Description: "Ranked: the lowest ELO that can enter", Type: discordgo.ApplicationCommandOptionInteger, MinValue: floatPtr(1), MaxValue: 10000},
					{Name: "max_elo", Description: "Ranked: the highest ELO that can enter", Type: discordgo.ApplicationCommandOptionInteger, MinValue: floatPtr(1), MaxValue: 10000},
					{Name: "min_rank", Description: "Ranked: the lowest rank that can enter (a rank's name)", Type: discordgo.ApplicationCommandOptionString},
					{Name: "max_rank", Description: "Ranked: the highest rank that can enter (a rank's name)", Type: discordgo.ApplicationCommandOptionString},
					{Name: "min_games", Description: "Ranked: games needed this season", Type: discordgo.ApplicationCommandOptionInteger, MinValue: floatPtr(1)},
					{Name: "min_wins", Description: "Ranked: wins needed this season", Type: discordgo.ApplicationCommandOptionInteger, MinValue: floatPtr(1)},
					{Name: "active_days", Description: "Ranked: must have played a game in the last this many days", Type: discordgo.ApplicationCommandOptionInteger, MinValue: floatPtr(1), MaxValue: 365},
					{Name: "no_punished", Description: "Ranked: nobody with an active punishment or strike", Type: discordgo.ApplicationCommandOptionBoolean},
					{Name: "new_winners_only", Description: "Nobody who won an earlier giveaway here", Type: discordgo.ApplicationCommandOptionBoolean},
				},
			},
			{
				Name: "end", Description: "Draw a running giveaway now", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{giveawayOption("The giveaway to end")},
			},
			{
				Name: "reroll", Description: "Draw new winners of an ended giveaway", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					giveawayOption("The giveaway to draw again"),
					{Name: "winners", Description: "How many new winners (default 1)", Type: discordgo.ApplicationCommandOptionInteger, MinValue: floatPtr(1), MaxValue: 50},
				},
			},
			{
				Name: "cancel", Description: "Cancel a giveaway that has not ended", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{giveawayOption("The giveaway to cancel")},
			},
			{Name: "list", Description: "The most recent giveaways, with their entries", Type: discordgo.ApplicationCommandOptionSubCommand},
		},
	},
	Execute: executeGiveaway,
}

func floatPtr(v float64) *float64 { return &v }

func giveawayOption(description string) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Name: "giveaway", Description: description, Type: discordgo.ApplicationCommandOptionInteger, Required: true, Autocomplete: true}
}

var durationPart = regexp.MustCompile(`(?i)(\d+)\s*([wdhm])`)

// parseDuration reads 30m, 2h, 3d, 1w or a mix such as 1d12h.
func parseDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	parts := durationPart.FindAllStringSubmatch(s, -1)
	if len(parts) == 0 || strings.TrimSpace(durationPart.ReplaceAllString(s, "")) != "" {
		return 0, false
	}
	var total time.Duration
	unit := map[string]time.Duration{"w": 7 * 24 * time.Hour, "d": 24 * time.Hour, "h": time.Hour, "m": time.Minute}
	for _, p := range parts {
		n, err := strconv.Atoi(p[1])
		if err != nil || n > 100000 {
			return 0, false
		}
		total += time.Duration(n) * unit[strings.ToLower(p[2])]
	}
	return total, true
}

func executeGiveaway(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	sub, opts := types.SelectedSubcommand(i)
	fail := func(err error) error {
		if errors.Is(err, panels.ErrGiveaway) {
			return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed(panels.Message(err)))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed("That did not work just now. Please try again in a moment."))
	}
	ref := panels.CommandRefs(s, i.GuildID)
	id := uint(helpers.SubOptionInt(opts, "giveaway"))

	switch sub {
	case "create":
		d, ok := parseDuration(helpers.SubOptionString(opts, "duration"))
		if !ok {
			return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed("I could not read that duration. Use things like `30m`, `2h`, `3d`, `1w` or `1d12h`."))
		}
		channel := helpers.SubOptionChannelID(opts, "channel")
		if channel == "" {
			channel = i.ChannelID
		}
		rules := panels.GiveawayRules{
			RequiredElo: helpers.SubOptionInt(opts, "min_elo"), MaxElo: helpers.SubOptionInt(opts, "max_elo"),
			MinRank: helpers.SubOptionString(opts, "min_rank"), MaxRank: helpers.SubOptionString(opts, "max_rank"),
			MinGames: helpers.SubOptionInt(opts, "min_games"), MinWins: helpers.SubOptionInt(opts, "min_wins"), ActiveDays: helpers.SubOptionInt(opts, "active_days"),
			RequireRegistered: helpers.SubOptionBool(opts, "registered_only"), NoActivePunishment: helpers.SubOptionBool(opts, "no_punished"),
			ExcludePastWinners: helpers.SubOptionBool(opts, "new_winners_only"), BoostersOnly: helpers.SubOptionBool(opts, "boosters_only"),
			MinJoinDays: helpers.SubOptionInt(opts, "min_join_days"), PrizeRoleID: helpers.SubOptionRoleID(s, opts, "prize_role"),
		}
		if role := helpers.SubOptionRoleID(s, opts, "required_role"); role != "" {
			rules.RequiredRoleIDs = []string{role}
		}
		if role := helpers.SubOptionRoleID(s, opts, "blocked_role"); role != "" {
			rules.BlockedRoleIDs = []string{role}
		}
		winners := max(helpers.SubOptionInt(opts, "winners"), 1)
		row, err := panels.CreateGiveaway(s, i.GuildID, panels.CreateInput{
			Prize: helpers.SubOptionString(opts, "prize"), Description: helpers.SubOptionString(opts, "description"), ChannelID: channel,
			HostID: helpers.InteractionUserID(i), Winners: winners, Duration: d, Rules: rules,
		})
		if err != nil {
			return fail(err)
		}
		return helpers.EditInteractionEmbed(s, i, helpers.Preset("success", types.PresetOptions{
			Title: "Giveaway posted",
			Description: fmt.Sprintf("**#%d · %s** is up in <#%s> and ends <t:%d:R>.\n\nManage it with %s (draw now), %s (new winners), %s and %s. Players can check where they stand with %s.",
				row.ID, row.Prize, channel, row.EndsAt.Unix(), ref("giveaway end"), ref("giveaway reroll"), ref("giveaway cancel"), ref("giveaway list"), ref("stats")),
			Thumbnail: helpers.ThumbTada,
		}))
	case "end":
		winners, err := panels.EndGiveaway(s, i.GuildID, id)
		if err != nil {
			return fail(err)
		}
		return helpers.EditInteractionEmbed(s, i, helpers.Preset("success", types.PresetOptions{
			Title: "Giveaway ended", Description: fmt.Sprintf("Giveaway **#%d** was drawn. %s", id, winnersText(winners)), Thumbnail: helpers.ThumbTada,
		}))
	case "reroll":
		winners, err := panels.RerollGiveaway(s, i.GuildID, id, max(helpers.SubOptionInt(opts, "winners"), 1))
		if err != nil {
			return fail(err)
		}
		return helpers.EditInteractionEmbed(s, i, helpers.Preset("success", types.PresetOptions{
			Title: "New winners drawn", Description: fmt.Sprintf("Giveaway **#%d**: %s They were announced under the giveaway's post.", id, winnersText(winners)), Thumbnail: helpers.ThumbTada,
		}))
	case "cancel":
		if err := panels.CancelGiveaway(s, i.GuildID, id); err != nil {
			return fail(err)
		}
		return helpers.EditInteractionEmbed(s, i, helpers.Preset("success", types.PresetOptions{
			Title: "Giveaway cancelled", Description: fmt.Sprintf("Giveaway **#%d** is cancelled and its post is marked.", id), Thumbnail: helpers.ThumbFlag,
		}))
	case "list":
		list, err := panels.ListGiveaways(i.GuildID)
		if err != nil {
			return fail(err)
		}
		if len(list) == 0 {
			return helpers.EditInteractionEmbed(s, i, helpers.Preset("info", types.PresetOptions{
				Title: "Giveaways", Description: fmt.Sprintf("There are no giveaways yet. Make one with %s or on the dashboard.", ref("giveaway create")), Thumbnail: helpers.ThumbTada,
			}))
		}
		var b strings.Builder
		for _, g := range list {
			when := fmt.Sprintf("ends <t:%d:R>", g.EndsAt.Unix())
			if g.Status == "ended" || g.Status == "cancelled" {
				when = fmt.Sprintf("%s <t:%d:d>", g.Status, g.EndsAt.Unix())
			}
			fmt.Fprintf(&b, "**#%d** · %s · %s · %d entr%s · %d winner%s · %s\n", g.ID, g.Prize, g.Status, g.Entries, map[bool]string{true: "y", false: "ies"}[g.Entries == 1], g.Winners, map[bool]string{true: "", false: "s"}[g.Winners == 1], when)
		}
		return helpers.EditInteractionEmbed(s, i, helpers.Preset("info", types.PresetOptions{
			Title: "Giveaways", Description: b.String() + fmt.Sprintf("\nEnd one early with %s, draw new winners with %s.", ref("giveaway end"), ref("giveaway reroll")), Thumbnail: helpers.ThumbTada,
		}))
	}
	return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed("Pick a subcommand: create, end, reroll, cancel or list."))
}

func winnersText(winners []string) string {
	if len(winners) == 0 {
		return "Nobody qualified."
	}
	parts := make([]string, len(winners))
	for i, w := range winners {
		parts[i] = "<@" + w + ">"
	}
	return "Winner" + map[bool]string{true: "", false: "s"}[len(winners) == 1] + ": " + strings.Join(parts, ", ") + "."
}

// autocompleteGiveaway offers the guild's giveaways that suit the subcommand: running ones to end or cancel, ended ones to reroll.
func autocompleteGiveaway(s *discordgo.Session, i *discordgo.InteractionCreate) {
	sub, opts := types.SelectedSubcommand(i)
	typed := ""
	for _, o := range opts {
		if o.Focused {
			typed = strings.ToLower(fmt.Sprint(o.Value))
		}
	}
	var choices []*discordgo.ApplicationCommandOptionChoice
	if rows, err := database.RecentGiveaways(i.GuildID); err == nil {
		for _, g := range rows {
			switch sub {
			case "end":
				if g.Status != "running" {
					continue
				}
			case "cancel":
				if g.Status != "running" && g.Status != "scheduled" {
					continue
				}
			case "reroll":
				if g.Status != "ended" {
					continue
				}
			}
			name := fmt.Sprintf("#%d · %s · %s", g.ID, g.Prize, g.Status)
			if typed != "" && !strings.Contains(strings.ToLower(name), typed) {
				continue
			}
			if len(name) > 100 {
				name = name[:100]
			}
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: name, Value: g.ID})
			if len(choices) == 25 {
				break
			}
		}
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult, Data: &discordgo.InteractionResponseData{Choices: choices},
	})
}
