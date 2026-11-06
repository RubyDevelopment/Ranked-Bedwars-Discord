package staff

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"time"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/integrity"
	"ranked-bedwars/src/discord/helpers/match"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

func init() {
	helpers.Register(IntegrityCommand)
	helpers.RegisterComponentHandler(handleIntegrityComponent)
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
	e, components := integrity.Overview(rep, helpers.OptionString(i, "kind"), 1)
	return helpers.EditInteractionComponents(s, i, e, components)
}

// isStaffMember says whether whoever pressed a component may use the integrity report: an administrator or the guild's staff role (the same
// people who can run the command).
func isStaffMember(i *discordgo.InteractionCreate) bool {
	if i.Member == nil {
		return false
	}
	if i.Member.Permissions&discordgo.PermissionAdministrator != 0 {
		return true
	}
	role, err := helpers.RequirementSlotID(i.GuildID, "score", types.ReqRole, types.StaffRoleKey)
	return err == nil && role != "" && slices.Contains(i.Member.Roles, role)
}

// handleIntegrityComponent turns the overview's pages, filters it by kind, and opens a finding's evidence when one is picked.
func handleIntegrityComponent(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, "intg|") {
		return false
	}
	if strings.HasPrefix(customID, integrity.OffPrefix) {
		return true // a button that cannot be pressed
	}
	if !isStaffMember(i) {
		_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral, Content: "Only staff can use the integrity report."},
		})
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if customID == integrity.OpenID { // a finding picked from the menu: its evidence, for the person who picked it only
		values := i.MessageComponentData().Values
		_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
		})
		n, _ := strconv.Atoi(strings.Join(values, ""))
		rep, err := integrity.Fetch(ctx, i.GuildID, "", 0)
		if err != nil || n < 1 || n > len(rep.Findings) {
			content := "That finding is no longer in the report: it has changed since this page was drawn. Run /integrity again."
			_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content})
			return true
		}
		f := rep.Findings[n-1]
		hits := integrity.ChatHits(ctx, i.GuildID, f.Games, f.Players, 6)
		embeds := []*discordgo.MessageEmbed{integrity.Detail(rep, n, f, hits)}
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &embeds})
		return true
	}

	page, kind := 1, ""
	switch {
	case customID == integrity.KindID:
		if v := strings.Join(i.MessageComponentData().Values, ""); v != "all" {
			kind = v
		}
	case strings.HasPrefix(customID, integrity.PagePrefix):
		rest := strings.TrimPrefix(customID, integrity.PagePrefix)
		target, k, _ := strings.Cut(rest, "|")
		page, _ = strconv.Atoi(target)
		kind = k
	default:
		return true
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})
	rep, err := integrity.Fetch(ctx, i.GuildID, "", 0)
	if err != nil {
		log.Printf("[Integrity] page: %v", err)
		return true
	}
	e, components := integrity.Overview(rep, kind, page)
	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	embeds := []*discordgo.MessageEmbed{e}
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &embeds, Components: &components}); err != nil {
		log.Printf("[Integrity] page: %v", err)
	}
	return true
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
