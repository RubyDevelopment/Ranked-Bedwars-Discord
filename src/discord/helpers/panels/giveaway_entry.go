package panels

import (
	"slices"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
)

// gatherEntrant collects what the giveaway's rules need to know about a member (who pressed Enter, or was drawn), reading only what they ask
// for: the stats, the punishment record and the other giveaways' winners are not looked up for a giveaway that does not use them. The second
// result is the server's rank names, lowest first.
func gatherEntrant(guildID, userID string, m *discordgo.Member, g giveawayDoc, giveawayID uint) (entrant, []string, error) {
	e := entrant{JoinedDays: -1, IdleDays: -1}
	if m != nil {
		e.Roles = slices.Clone(m.Roles)
		e.Booster = m.PremiumSince != nil
		if !m.JoinedAt.IsZero() {
			e.JoinedDays = int(time.Since(m.JoinedAt).Hours() / 24)
		}
	}
	var order []string

	if g.ExcludePastWinners {
		if won, err := database.HasWonGiveaway(guildID, userID, giveawayID); err == nil {
			e.PastWinner = won
		}
	}

	wantsPlayer := g.needsRegistration() || g.needsStats() || g.NoActivePunishment
	if !wantsPlayer {
		return e, nil, nil
	}
	view, err := database.Must().Player.Get(guildID, userID)
	if err != nil || view == nil || view.Player == nil {
		return e, nil, nil // not registered: the rules say so
	}
	e.Registered = true
	playerID := sdk.UintToString(view.Player.ID)
	_, seasonID, _ := helpers.ActiveSeasonKey(guildID)

	if g.needsStats() {
		stats, err := database.Must().Stats.GetOrCreate(guildID, playerID, seasonID)
		if err != nil {
			return e, nil, err
		}
		e.Elo, e.Games, e.Wins = stats.Elo, stats.GamesPlayed, stats.Wins
		e.Rank = stats.Rank
		if stats.LastPlayedAt != nil {
			e.IdleDays = int(time.Since(*stats.LastPlayedAt).Hours() / 24)
		}
		if names, err := rankNames(guildID); err == nil {
			order = names
		}
	}
	if g.NoActivePunishment {
		if list, err := database.Must().Punishments.ByPlayer(guildID, playerID, true, "", sdk.Page{Limit: 1}); err == nil && list != nil && len(list.Data) > 0 {
			e.Punished = true
		}
		if strikes, err := database.Must().Strikes.AllByPlayer(guildID, playerID, seasonID, true); err == nil && len(strikes) > 0 {
			e.Punished = true
		}
	}
	return e, order, nil
}
