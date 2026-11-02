package general

import (
	"sync"
	"time"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(GameViewCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameViewCommand Displays deep metadata, rules, and configuration details for a specific match.
var GameViewCommand = &types.Command{
	Name:          "viewgame",
	Description:   "Displays deep metadata, rules, and configuration details for a specific match.",
	PrefixAliases: []string{"gameinfo", "gi", "vg"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:         "game",
				Description:  "The game number to view (defaults to current game if in game channel)",
				Type:         discordgo.ApplicationCommandOptionInteger,
				Required:     false,
				Autocomplete: true,
			},
			{
				Name:        "match_id",
				Description: "Target Match ID identifier",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Autocomplete: helpers.GameNumberAutocomplete(),
	Execute:      executeGameViewCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameViewCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	var game helpers.Game
	var ok bool

	// Resolve the game from the option, falling back to the current game channel
	if gameNumber := helpers.SubOptionInt(i.ApplicationCommandData().Options, "game"); gameNumber > 0 {
		var err error
		game, ok, err = helpers.Games.ByNumberOrStored(i.GuildID, gameNumber)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("look up the game", err))
		}
		if !ok {
			return helpers.EditInteractionEmbed(s, i, helpers.GameViewNotFoundEmbed(i, gameNumber))
		}
	} else {
		game, ok = helpers.Games.ByChannel(i.ChannelID)
		if !ok {
			return helpers.EditInteractionEmbed(s, i, helpers.GameViewNoGameEmbed(i))
		}
	}

	return helpers.EditInteractionEmbed(s, i, helpers.GameViewOverviewEmbed(i, game, gameRatios(i.GuildID, game)))
}

// gameRatios looks up each player's season W/L and K/D (nil when there is no active season).
func gameRatios(guildID string, game helpers.Game) map[string]string {
	_, seasonID, err := helpers.ActiveSeasonKey(guildID)
	if err != nil || seasonID == 0 {
		return nil
	}
	out := make(map[string]string, len(game.Players))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range game.Players {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wlr, kdr := "0.00", "0.00"
			if st, err := database.Must().Stats.Get(guildID, p.UserID, seasonID); err == nil {
				wlr, kdr = helpers.Ratio(st.Wins, st.Losses), helpers.Ratio(st.Kills, st.Deaths)
			}
			mu.Lock()
			out[p.UserID] = "W/L **" + wlr + "** · K/D **" + kdr + "**"
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================
