package staff

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

var teamChoices = []*discordgo.ApplicationCommandOptionChoice{
	{Name: "Team A", Value: 0}, {Name: "Team B", Value: 1}, {Name: "Team C", Value: 2}, {Name: "Team D", Value: 3},
}

func init() {
	helpers.Register(GameHostCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameHostCommand builds a custom game with a setup panel and manages the ones running.
var GameHostCommand = &types.Command{
	Name:         "host",
	Description:  "Creates a custom game with a setup panel, or manages the hosted games that are running.",
	Requirements: []types.Requirement{{Type: types.ReqRole, Key: "staff_role"}},
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "create", Description: "Set up a custom game step by step: mode, options, teams, voice channels, then start.", Type: discordgo.ApplicationCommandOptionSubCommand},
			{Name: "list", Description: "List the hosted games that are running.", Type: discordgo.ApplicationCommandOptionSubCommand},
			{Name: "view", Description: "Show a hosted game's settings, teams and voice channels.", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{gameOption()}},
			{
				Name: "cancel", Description: "Cancel a hosted game: no ELO or stats, and its channels are removed.", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{gameOption(), {Name: "reason", Description: "Why it is being cancelled.", Type: discordgo.ApplicationCommandOptionString, Required: true, MaxLength: 200}},
			},
			{
				Name: "finish", Description: "End a running hosted game and record the winning team.", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{gameOption(), teamOption("winner", "The winning team.")},
			},
			{
				Name: "settings", Description: "Change what a running hosted game counts toward.", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					gameOption(),
					{Name: "ranks", Description: "Count toward ELO and ranks.", Type: discordgo.ApplicationCommandOptionBoolean},
					{Name: "stats", Description: "Count toward season stats.", Type: discordgo.ApplicationCommandOptionBoolean},
				},
			},
			{
				Name: "replace", Description: "Swap a player for another on the same team (player-hosted games).", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					gameOption(),
					{Name: "out", Description: "Player leaving the game.", Type: discordgo.ApplicationCommandOptionString, Required: true, Autocomplete: true},
					{Name: "in", Description: "Player taking their place.", Type: discordgo.ApplicationCommandOptionUser, Required: true},
				},
			},
			{
				Name: "move", Description: "Move a player to another team (player-hosted games).", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					gameOption(),
					{Name: "player", Description: "Player to move.", Type: discordgo.ApplicationCommandOptionString, Required: true, Autocomplete: true},
					teamOption("team", "The team to move them to."),
				},
			},
			{
				Name: "voice", Description: "Change the voice channel a team plays in.", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					gameOption(), teamOption("team", "Which team."),
					{Name: "channel", Description: "New voice channel.", Type: discordgo.ApplicationCommandOptionChannel, Required: true, ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildVoice}},
				},
			},
		},
	},
	Autocomplete: autocompleteGameHost,
	Execute:      executeGameHostCommand,
}

func gameOption() *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Name: "game", Description: "Hosted game.", Type: discordgo.ApplicationCommandOptionInteger, Required: true, Autocomplete: true}
}

func teamOption(name, description string) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Name: name, Description: description, Type: discordgo.ApplicationCommandOptionInteger, Required: true, Choices: teamChoices}
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameHostCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	sub, opts := types.SelectedSubcommand(i)
	guildID, actor := i.GuildID, helpers.InteractionUserID(i)

	if sub == "create" {
		session, errEmbed := helpers.NewHostSession(s, guildID, actor)
		if errEmbed != nil {
			return helpers.EditInteractionEmbed(s, i, errEmbed)
		}
		embed, components := session.Render(s)
		return helpers.EditInteractionComponents(s, i, embed, components)
	}
	if sub == "list" {
		return helpers.EditInteractionEmbed(s, i, gameHostListEmbed(helpers.Games.CustomGames(guildID)))
	}

	number := helpers.SubOptionInt(opts, "game")
	g, ok := helpers.Games.ByNumber(guildID, number)
	if !ok || g.Custom == nil {
		return helpers.EditInteractionEmbed(s, i, gameHostNotFoundEmbed(number))
	}

	switch sub {
	case "view":
		return helpers.EditInteractionEmbed(s, i, gameHostViewEmbed(g))

	case "cancel":
		if err := helpers.Games.Void(actor, guildID, g.ID, helpers.SubOptionString(opts, "reason"), helpers.VoidOptions{}); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.HostGameErrorEmbed(err))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.HostGameDoneEmbed(fmt.Sprintf("Game **#%d** was cancelled. No ELO or stats were recorded.", g.Number)))

	case "finish":
		team := helpers.SubOptionInt(opts, "winner")
		if team < 0 || team >= len(g.Custom.Teams) {
			return helpers.EditInteractionEmbed(s, i, helpers.HostGameErrorEmbed(helpers.ErrUnknownTeam))
		}
		if _, err := helpers.Games.Score(actor, guildID, g.ID, helpers.TeamName(team), helpers.ScoreOptions{}); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.HostGameErrorEmbed(err))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.HostGameDoneEmbed(fmt.Sprintf("Game **#%d** finished: **%s** won.", g.Number, helpers.TeamName(team))))

	case "settings":
		ranked, stats := optionalBool(opts, "ranks"), optionalBool(opts, "stats")
		if ranked == nil && stats == nil {
			return helpers.EditInteractionEmbed(s, i, helpers.HostGameErrorEmbed(errors.New("choose at least one of ranks or stats to change")))
		}
		if err := helpers.Games.SetCustomOptions(guildID, g.ID, ranked, stats); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.HostGameErrorEmbed(err))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.HostGameDoneEmbed(fmt.Sprintf("Updated game **#%d**.", g.Number)))

	case "replace":
		incoming := helpers.SubOptionUser(s, opts, "in")
		if incoming == nil || incoming.Bot {
			return helpers.EditInteractionEmbed(s, i, helpers.HostGameErrorEmbed(errors.New("choose a real player to take their place")))
		}
		if err := helpers.Games.ReplacePlayer(guildID, g.ID, helpers.SubOptionString(opts, "out"), incoming.ID, incoming.Username); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.HostGameErrorEmbed(err))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.HostGameDoneEmbed(fmt.Sprintf("<@%s> took <@%s>'s place in game **#%d**.", incoming.ID, helpers.SubOptionString(opts, "out"), g.Number)))

	case "move":
		team := helpers.SubOptionInt(opts, "team")
		player := helpers.SubOptionString(opts, "player")
		if err := helpers.Games.MovePlayer(guildID, g.ID, player, team); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.HostGameErrorEmbed(err))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.HostGameDoneEmbed(fmt.Sprintf("<@%s> is now on **%s** in game **#%d**.", player, helpers.TeamName(team), g.Number)))

	case "voice":
		team, channel := helpers.SubOptionInt(opts, "team"), helpers.SubOptionChannelID(opts, "channel")
		if err := helpers.Games.SetTeamVoice(guildID, g.ID, team, channel); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.HostGameErrorEmbed(err))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.HostGameDoneEmbed(fmt.Sprintf("**%s** of game **#%d** now plays in <#%s>.", helpers.TeamName(team), g.Number, channel)))
	}
	return helpers.EditInteractionEmbed(s, i, helpers.HostGameErrorEmbed(errors.New("unknown subcommand")))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

func optionalBool(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) *bool {
	for _, o := range opts {
		if o.Name == name && o.Type == discordgo.ApplicationCommandOptionBoolean {
			v := o.BoolValue()
			return &v
		}
	}
	return nil
}

// autocompleteGameHost suggests the running games, and a game's players for `out` and `player`.
func autocompleteGameHost(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_, opts := types.SelectedSubcommand(i)
	var focused *discordgo.ApplicationCommandInteractionDataOption
	for _, o := range opts {
		if o.Focused {
			focused = o
		}
	}
	var choices []*discordgo.ApplicationCommandOptionChoice
	if focused != nil {
		typed := strings.ToLower(fmt.Sprint(focused.Value))
		switch focused.Name {
		case "game":
			for _, g := range helpers.Games.CustomGames(i.GuildID) {
				name := fmt.Sprintf("#%d | %s | %s", g.Number, g.Mode.Name, g.State)
				if strings.Contains(strings.ToLower(name), typed) {
					choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: name, Value: g.Number})
				}
			}
		case "out", "player":
			if g, ok := helpers.Games.ByNumber(i.GuildID, helpers.SubOptionInt(opts, "game")); ok && g.Custom != nil {
				for _, p := range g.Players {
					if strings.Contains(strings.ToLower(p.Username), typed) {
						choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: p.Username, Value: p.UserID})
					}
				}
			}
		}
	}
	if len(choices) > 25 {
		choices = choices[:25]
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
}

// ============================================================================
// Embed Builders
// ============================================================================

func gameHostNotFoundEmbed(number int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Hosted Game Not Found",
		Description: fmt.Sprintf("No live hosted game **#%d** was found. Use `/game_host list` to see the running ones.", number),
	})
}

func gameHostListEmbed(games []helpers.Game) *discordgo.MessageEmbed {
	if len(games) == 0 {
		return helpers.Preset("info", types.PresetOptions{
			Title:       "Hosted Games",
			Description: "No hosted games are running. Start one with `/game_host create`.",
		})
	}
	lines := make([]string, len(games))
	for i, g := range games {
		sizes := make([]string, len(g.Custom.Teams))
		for t, team := range g.Custom.Teams {
			sizes[t] = strconv.Itoa(len(team))
		}
		lines[i] = fmt.Sprintf("> `#%d` · `%s` · `%s` · `%s` · ranks %s · stats %s · <@%s>", g.Number, g.Mode.Name, strings.Join(sizes, "v"), g.State, helpers.HostOnOff(g.Custom.Ranked), helpers.HostOnOff(g.Custom.Stats), g.Custom.HostedBy)
	}
	return helpers.Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Hosted Games (%d)", len(games)),
		Description: strings.Join(lines, "\n"),
	})
}

func gameHostViewEmbed(g helpers.Game) *discordgo.MessageEmbed {
	var fields []*discordgo.MessageEmbedField
	for t, team := range g.Custom.Teams {
		value := "<@" + strings.Join(team, "> <@") + ">"
		if name := helpers.TeamName(t); g.Channels.Voice[name] != "" {
			value += "\n<#" + g.Channels.Voice[name] + ">"
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: helpers.TeamName(t), Value: value, Inline: true})
	}
	hosting := "by hand"
	if g.Bot != "" {
		hosting = "`" + g.Bot + "`"
	}
	return helpers.Preset("info", types.PresetOptions{
		Title: fmt.Sprintf("Hosted Game #%d", g.Number),
		Description: fmt.Sprintf("> **Mode:** `%s`\n> **State:** `%s`\n> **Map:** `%s`\n> **Ranks:** %s\n> **Stats:** %s\n> **Hosted:** %s\n> **By:** <@%s>",
			g.Mode.Name, g.State, g.Map, helpers.HostOnOff(g.Custom.Ranked), helpers.HostOnOff(g.Custom.Stats), hosting, g.Custom.HostedBy),
		Fields: fields,
	})
}
