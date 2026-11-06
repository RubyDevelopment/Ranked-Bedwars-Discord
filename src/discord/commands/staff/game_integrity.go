package staff

import (
	"context"
	"fmt"
	"log"
	"slices"
	"time"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/integrity"
	"ranked-bedwars/src/discord/helpers/match"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

func init() {
	helpers.Register(IntegrityCommand)
	match.ScoredHooks = append(match.ScoredHooks, alertAfterScoring)
}

// IntegrityCommand shows who looks like they are win trading, boosting, throwing or dodging losses, with the evidence.
var IntegrityCommand = &types.Command{
	Name:         "integrity",
	Requirements: types.GameRequirements(),
	Description:  "Shows suspected win trading, boosting, throwing and void dodging, with the evidence for each.",
	Ephemeral:    true,
	Cooldown:     5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "finding", Description: "Show the evidence for this finding number", Type: discordgo.ApplicationCommandOptionInteger, MinValue: ptrFloat(1)},
			{Name: "player", Description: "Show the findings about one player", Type: discordgo.ApplicationCommandOptionUser},
			{Name: "game", Description: "Show the findings that involve this game number", Type: discordgo.ApplicationCommandOptionInteger, MinValue: ptrFloat(1)},
			{Name: "kind", Description: "Only this kind of finding", Type: discordgo.ApplicationCommandOptionString, Choices: []*discordgo.ApplicationCommandOptionChoice{
				{Name: "Win trading", Value: "win_trading"}, {Name: "Boosting", Value: "boosting"}, {Name: "Throwing", Value: "throwing"}, {Name: "Void dodging", Value: "void_dodging"},
			}},
		},
	},
	Execute: executeIntegrityCommand,
}

func ptrFloat(v float64) *float64 { return &v }

func executeIntegrityCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fail := func(err error) error {
		log.Printf("[Integrity] %v", err)
		return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed("The integrity report could not be loaded. Try again in a moment."))
	}

	if user := helpers.OptionUser(s, i, "player"); user != nil {
		rep, err := integrity.Fetch(ctx, i.GuildID, user.ID, 0)
		if err != nil {
			return fail(err)
		}
		all, _ := integrity.Fetch(ctx, i.GuildID, "", 0)
		return helpers.EditInteractionEmbed(s, i, integrity.PlayerView(rep, user.Username, all))
	}
	rep, err := integrity.Fetch(ctx, i.GuildID, "", 0)
	if err != nil {
		return fail(err)
	}
	if n := helpers.OptionInt(i, "finding"); n > 0 {
		if n > len(rep.Findings) {
			return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed(fmt.Sprintf("There is no finding #%d: the report has %d.", n, len(rep.Findings))))
		}
		f := rep.Findings[n-1]
		hits := integrity.ChatHits(ctx, i.GuildID, f.Games, f.Players, 6)
		return helpers.EditInteractionEmbed(s, i, integrity.Detail(rep, n, f, hits))
	}
	if number := helpers.OptionInt(i, "game"); number > 0 {
		return helpers.EditInteractionEmbed(s, i, integrity.GameView(rep, number))
	}
	return helpers.EditInteractionEmbed(s, i, integrity.Overview(rep, helpers.OptionString(i, "kind")))
}

// alertAfterScoring tells staff when a game that just scored adds to a high or critical finding. It waits for the database's report to move on
// (it is kept a couple of minutes), and never pings the players it is about.
func alertAfterScoring(s *discordgo.Session, g match.Game) {
	if !match.StaffAlertPosts(g.GuildID, "integrity") {
		return
	}
	time.AfterFunc(2*time.Minute+15*time.Second, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		rep, err := integrity.Fetch(ctx, g.GuildID, "", 0)
		if err != nil {
			log.Printf("[Integrity] alert for game %d: %v", g.Number, err)
			return
		}
		for n, f := range rep.Findings {
			if f.AtLeast("high") && slices.Contains(f.Games, g.Number) {
				desc, fields := integrity.Alert(rep, n+1, f)
				match.StaffAlertPinging(s, g.GuildID, "integrity", nil, desc, fields...)
			}
		}
	})
}
