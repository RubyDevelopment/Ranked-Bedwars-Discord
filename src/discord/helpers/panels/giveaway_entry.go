package panels

import (
	"slices"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
)

// gatherEntrant collects what the giveaway's rules need to know about the member who pressed Enter, reading only what they ask for: the
// stats, the punishment record and the other giveaways' winners are not looked up for a giveaway that does not use them. The second
// result is the server's rank names, lowest first.
func gatherEntrant(guildID, userID string, roles []string, g giveawayDoc, giveawayID uint) (entrant, []string, error) {
	e := entrant{Roles: slices.Clone(roles)}
	var order []string

	if g.ExcludePastWinners {
		if won, err := database.HasWonGiveaway(guildID, userID, giveawayID); err == nil {
			e.PastWinner = won
		}
	}

	wantsPlayer := g.needsStats() || g.NoActivePunishment
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
		if ranks, err := helpers.GuildMetaRanks(guildID); err == nil {
			for _, r := range helpers.SortedRanks(ranks) {
				order = append(order, r.RankName)
			}
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

// memberRoles are the roles of the member who pressed a button.
func memberRoles(i *discordgo.InteractionCreate) []string {
	if i.Member == nil {
		return nil
	}
	return i.Member.Roles
}
