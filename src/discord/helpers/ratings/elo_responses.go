package ratings

import (
	"errors"
	"fmt"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

// DecayWarningEmbed warns a player that their ELO is about to decay.
func DecayWarningEmbed(n DecayNotice) *discordgo.MessageEmbed {
	return helpers.Preset("caution", types.PresetOptions{
		Title:       "ELO Decay Incoming",
		Description: fmt.Sprintf("You have been inactive for a while. Unless you play a ranked game before <t:%d:R>, you will lose **%d ELO** (%d → %d).", n.At.Unix(), n.Amount, n.Before, n.After),
		Thumbnail:   helpers.ThumbClock,
	})
}

// DecayAppliedEmbed tells a player their ELO decayed.
func DecayAppliedEmbed(n DecayNotice) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "ELO Decayed",
		Description: fmt.Sprintf("%d days without a ranked game cost you **%d ELO** (%d → %d). Play a ranked game to stop the decay.", n.InactivityDays, n.Amount, n.Before, n.After),
		Thumbnail:   helpers.ThumbFlag,
	})
}

// NewPerkResolver builds the lookup the EloManager uses to find a player's
// active perks (see EloManager.SetPerkResolver): the guild's enabled perks from
// the game config, filtered by the member's roles. session returns the live
// Discord session (nil = nobody has perks).
//
// A member boosting the server gets one booster
// stack, and a perk whose allowed roles include one of their managed
// (subscriber) roles counts as a booster perk.
func NewPerkResolver(session func() *discordgo.Session) func(guildID, userID string) PerkContext {
	cache := base.NewTTLCache[[]Perk](base.ConfigCacheTTL)
	return func(guildID, userID string) PerkContext {
		s := session()
		if s == nil {
			return PerkContext{Err: errors.New("no Discord session to read roles from")}
		}
		perks, err := cache.Get(guildID, func() ([]Perk, error) { return GuildPerks(guildID) })
		if err != nil {
			return PerkContext{Err: err}
		}
		if len(perks) == 0 {
			return PerkContext{}
		}
		member, err := s.State.Member(guildID, userID)
		if err != nil {
			if member, err = s.GuildMember(guildID, userID); err != nil {
				if helpers.IsUnknownMember(err) {
					return PerkContext{} // left the server: no roles, so no perks
				}
				return PerkContext{Err: err} // a lookup failure is not "no perks": scoring retries instead of paying nothing
			}
		}

		held := make(map[string]bool, len(member.Roles))
		for _, id := range member.Roles {
			held[id] = true
		}
		var boosterRoles []string
		stacks := 0
		if member.PremiumSince != nil {
			stacks = 1
			if g, err := s.State.Guild(guildID); err == nil {
				for _, r := range g.Roles {
					if r.Managed && held[r.ID] {
						boosterRoles = append(boosterRoles, r.ID)
					}
				}
			}
		}

		ctx := PerkContext{BoosterStacks: stacks}
		for _, p := range MarkBoosters(perks, boosterRoles...) {
			if p.QualifiesFor(held, nil) {
				ctx.Perks = append(ctx.Perks, p)
			}
		}
		return ctx
	}
}
