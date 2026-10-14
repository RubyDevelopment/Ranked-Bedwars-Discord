package general

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

const leaderboardPerPage = 10

func ptrToFloat64(v float64) *float64 { return &v }

func init() {
	helpers.Register(GameLeaderboardCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameLeaderboardCommand Displays the top-ranked players and seasonal ELO leaderboards.
var GameLeaderboardCommand = &types.Command{
	Name:        "leaderboard",
	Description: "Displays the top-ranked players and seasonal ELO leaderboards.",
	Ephemeral:   true,
	Cooldown:    5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "page",
				Description: "The page of the leaderboard to display",
				Type:        discordgo.ApplicationCommandOptionInteger,
				Required:    false,
				MinValue:    ptrToFloat64(1),
			},
			{
				Name:        "season",
				Description: "Filter by season number",
				Type:        discordgo.ApplicationCommandOptionInteger,
				Required:    false,
			},
			{
				Name:        "mode",
				Description: "Filter by game mode",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
			helpers.StyleOption(),
		},
	},
	Execute:      executeGameLeaderboardCommand,
	Autocomplete: helpers.StyleAutocomplete("leaderboard"),
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameLeaderboardCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// Looking up ten players and their avatars can take longer than the three seconds Discord allows for a first answer.
	if err := helpers.DeferSlashInteraction(s, i, false); err != nil {
		return err
	}

	page := helpers.OptionInt(i, "page")
	if page < 1 {
		page = 1
	}

	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorLeaderboardEmbed())
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, leaderboardNoActiveSeasonEmbed())
	}

	offset := (page - 1) * leaderboardPerPage
	rows, err := database.Must().Stats.Leaderboard(i.GuildID, seasonID, sdk.Page{Limit: leaderboardPerPage, Offset: offset})
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorLeaderboardEmbed())
	}
	if len(rows.Data) == 0 {
		if page > 1 {
			return helpers.EditInteractionEmbed(s, i, leaderboardEmptyPageEmbed(page))
		}
		return helpers.EditInteractionEmbed(s, i, leaderboardEmptyEmbed(seasonKey))
	}

	totalPages := 1
	if rows.Total != nil && *rows.Total > 0 {
		totalPages = int((*rows.Total + leaderboardPerPage - 1) / leaderboardPerPage)
	}

	sent, err := sendLeaderboardBanner(s, i, rows.Data, seasonKey, seasonID, page, totalPages)
	if sent {
		return nil
	}
	if err != nil {
		log.Printf("leaderboard banner for guild %s: %v", i.GuildID, err)
	}

	return helpers.EditInteractionEmbed(s, i, leaderboardOverviewEmbed(rows.Data, seasonKey, page, totalPages))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

func invokingUser(i *discordgo.InteractionCreate) *discordgo.User {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User
	}
	return i.User
}

// sendLeaderboardBanner answers with the banner. sent is false when the guild has
// no style the invoker may use or drawing failed: the caller then sends the embed.
func sendLeaderboardBanner(s *discordgo.Session, i *discordgo.InteractionCreate, rows []sdk.LeaderboardEntry, season string, seasonID uint, page, totalPages int) (sent bool, err error) {
	invoker := invokingUser(i)
	if invoker == nil {
		return false, nil
	}
	doc := helpers.LoadBanners(i.GuildID)
	viewer := helpers.BannerViewer(invoker.ID, i.Member)

	view, verr := database.Must().Player.Get(i.GuildID, invoker.ID)
	preferred := ""
	if verr == nil {
		preferred = helpers.ChosenBanner(view.Config, "leaderboard")
	}
	if style := helpers.OptionString(i, "style"); style != "" {
		for _, b := range doc.Eligible("leaderboard", viewer) {
			if b.ID == style {
				preferred = style
				if verr == nil {
					if err := helpers.RememberBanner(i.GuildID, invoker.ID, view.Config, "leaderboard", style); err != nil {
						log.Printf("remember leaderboard banner: %v", err)
					}
				}
			}
		}
	}
	banner := doc.Choose("leaderboard", viewer, preferred)
	if banner == nil {
		return false, nil
	}

	vars := helpers.GuildVars(s, i.GuildID, invoker)
	vars["lb.title"] = fmt.Sprintf("%s Leaderboard", season)
	vars["season.name"] = season
	helpers.LeaderboardRowVars(s, i.GuildID, rows, vars)
	vars["page.number"], vars["page.total"] = fmt.Sprint(page), fmt.Sprint(totalPages)
	if view, err := database.Must().Player.Get(i.GuildID, invoker.ID); err == nil {
		you(s, i.GuildID, invoker, view, seasonID, vars)
	}

	img, err := helpers.RenderBanner(banner, vars)
	if err != nil {
		return false, err
	}
	text := ""
	if totalPages > 1 {
		text = fmt.Sprintf("Page %d/%d", page, totalPages)
	}
	return true, helpers.EditInteractionFile(s, i, text, "leaderboard.png", img)
}

func you(s *discordgo.Session, guildID string, invoker *discordgo.User, view *sdk.PlayerView, seasonID uint, vars helpers.Vars) {
	playerID := sdk.UintToString(view.Player.ID)
	st, err := database.Must().Stats.Get(guildID, playerID, seasonID)
	if err != nil {
		return
	}
	position := 0
	if rank, err := database.Must().Stats.Rank(guildID, playerID, seasonID); err == nil {
		position = rank.Position
	}
	helpers.EntryVars(vars, "me.", position, view.Config.Username, helpers.AvatarOf(invoker), st)
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorLeaderboardEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the leaderboard for this guild. Please contact an administrator for assistance or try again later.",
	})
}

func leaderboardNoActiveSeasonEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured.",
	})
}

func leaderboardEmptyEmbed(season string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("Leaderboard — %s", season),
		Description: "No players have stats for the active season yet.",
	})
}

func leaderboardEmptyPageEmbed(page int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Page Empty",
		Description: fmt.Sprintf("There are no leaderboard entries on page `%d`. Try a lower page number.", page),
	})
}

func leaderboardOverviewEmbed(rows []sdk.LeaderboardEntry, season string, page, totalPages int) *discordgo.MessageEmbed {
	var b strings.Builder
	for _, row := range rows {
		total := row.Wins + row.Losses
		winRate := 0.0
		if total > 0 {
			winRate = float64(row.Wins) / float64(total) * 100
		}
		fmt.Fprintf(&b, "> **#%d** — `%d` ELO — %dW/%dL (`%.0f%%`)\n", row.Position, row.Elo, row.Wins, row.Losses, winRate)
	}

	return helpers.Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("Leaderboard — %s", season),
		Description: b.String(),
		FooterText:  fmt.Sprintf("Page %d/%d", page, totalPages),
	})
}
