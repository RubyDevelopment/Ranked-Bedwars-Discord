package match

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	appLog "ranked-bedwars/src/utility"
)

// maxAutocompleteChoices is the most choices Discord accepts in one autocomplete answer.
const maxAutocompleteChoices = 25

// GameNumberAutocomplete answers an autocomplete for a game-number option with the guild's
// games, limited to the given states (every state when none are given).
func GameNumberAutocomplete(states ...GameState) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		typed := focusedText(i)
		choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, maxAutocompleteChoices)
		for _, g := range Games.GuildGames(i.GuildID) {
			if len(states) > 0 && !slices.Contains(states, g.State) {
				continue
			}
			if typed != "" && !strings.Contains(strconv.Itoa(g.Number), typed) {
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

		_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionApplicationCommandAutocompleteResult,
			Data: &discordgo.InteractionResponseData{Choices: choices},
		})
	}
}

// focusedText is what the user has typed so far in the option being completed.
func focusedText(i *discordgo.InteractionCreate) string {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Focused {
			return strings.ToLower(strings.TrimSpace(fmt.Sprint(opt.Value)))
		}
	}
	return ""
}

// ForceVoidGameNumberAutocomplete suggests the guild's games that can be force-voided: the live ones, plus the most recent open
// and completed games from the database. Discord gives an autocomplete three seconds, so it never reads a whole season: a
// number typed in full is looked up on its own, and when the database is slow the live games are offered instead.
func ForceVoidGameNumberAutocomplete() func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		typed := focusedText(i)
		type result struct {
			games []Game
			err   error
		}
		done := make(chan result, 1)
		go func() {
			games, err := Games.ForceVoidSuggestions(i.GuildID, typed)
			done <- result{games, err}
		}()
		var games []Game
		select {
		case r := <-done:
			games = r.games
			if r.err != nil {
				appLog.EngineLogger.Error("[forcevoid] autocomplete: " + r.err.Error())
				games = Games.GuildGames(i.GuildID)
			}
		case <-time.After(2 * time.Second):
			games = Games.GuildGames(i.GuildID)
		}

		sort.Slice(games, func(a, b int) bool {
			if games[a].Number == games[b].Number {
				return games[a].ID > games[b].ID
			}
			return games[a].Number > games[b].Number
		})
		choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, maxAutocompleteChoices)
		for _, game := range games {
			if typed != "" && !strings.Contains(strconv.Itoa(game.Number), typed) {
				continue
			}
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
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
}

// ============================================================================
// Game channels
