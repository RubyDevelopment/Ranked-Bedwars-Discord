package general

import (
	"fmt"
	"log"
	"maps"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/banner"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

const leaderboardPerPage = 10

func ptrToFloat64(v float64) *float64 { return &v }

// leaderboardStats are the stats a leaderboard can be ordered by; key is the database's sort value.
type leaderboardStat struct {
	key, label string
	value      func(*models.PlayerStatsModel) int
}

var leaderboardStats = []leaderboardStat{
	{"elo", "ELO", func(m *models.PlayerStatsModel) int { return m.Elo }},
	{"highest_elo", "Peak ELO", func(m *models.PlayerStatsModel) int { return m.HighestElo }},
	{"wins", "Wins", func(m *models.PlayerStatsModel) int { return m.Wins }},
	{"losses", "Losses", func(m *models.PlayerStatsModel) int { return m.Losses }},
	{"games_played", "Games Played", func(m *models.PlayerStatsModel) int { return m.GamesPlayed }},
	{"kills", "Kills", func(m *models.PlayerStatsModel) int { return m.Kills }},
	{"final_kills", "Final Kills", func(m *models.PlayerStatsModel) int { return m.FinalKills }},
	{"first_bloods", "First Bloods", func(m *models.PlayerStatsModel) int { return m.FirstBloods }},
	{"assists", "Assists", func(m *models.PlayerStatsModel) int { return m.Assists }},
	{"beds_destroyed", "Beds Destroyed", func(m *models.PlayerStatsModel) int { return m.BedsDestroyed }},
	{"mvp_count", "MVPs", func(m *models.PlayerStatsModel) int { return m.MvpCount }},
	{"highest_win_streak", "Best Win Streak", func(m *models.PlayerStatsModel) int { return m.HighestWinStreak }},
}

func leaderboardStatChoices() []*discordgo.ApplicationCommandOptionChoice {
	out := make([]*discordgo.ApplicationCommandOptionChoice, len(leaderboardStats))
	for n, st := range leaderboardStats {
		out[n] = &discordgo.ApplicationCommandOptionChoice{Name: st.label, Value: st.key}
	}
	return out
}

func init() {
	helpers.Register(PlayerLeaderboardCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerLeaderboardCommand Displays the top-ranked players and seasonal ELO leaderboards.
var PlayerLeaderboardCommand = &types.Command{
	Name:          "leaderboard",
	Description:   "Displays the top-ranked players and seasonal ELO leaderboards.",
	PrefixAliases: []string{"lb"},
	Cooldown:      5 * time.Second,
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
			{
				Name:        "stat",
				Description: "The stat to rank by (default: ELO)",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
				Choices:     leaderboardStatChoices(),
			},
			helpers.StyleOption(),
		},
	},
	Execute:      executePlayerLeaderboardCommand,
	Autocomplete: helpers.StyleAutocomplete("leaderboard"),
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerLeaderboardCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
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
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorLeaderboardEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, LeaderboardNoActiveSeasonEmbed(i))
	}

	stat := leaderboardStats[0]
	for _, st := range leaderboardStats {
		if st.key == helpers.OptionString(i, "stat") {
			stat = st
		}
	}

	offset := (page - 1) * leaderboardPerPage
	rows, err := database.Must().Stats.Leaderboard(i.GuildID, seasonID, sdk.Page{Limit: leaderboardPerPage, Offset: offset, Sort: stat.key})
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorLeaderboardEmbed(i))
	}
	if len(rows.Data) == 0 {
		if page > 1 {
			return helpers.EditInteractionEmbed(s, i, LeaderboardEmptyPageEmbed(i, page))
		}
		return helpers.EditInteractionEmbed(s, i, LeaderboardEmptyEmbed(i, seasonKey))
	}

	totalPages := 1
	if rows.Total != nil && *rows.Total > 0 {
		totalPages = int((*rows.Total + leaderboardPerPage - 1) / leaderboardPerPage)
	}

	sent, err := sendLeaderboardBanner(s, i, rows.Data, seasonKey, seasonID, stat, page, totalPages)
	if sent {
		return nil
	}
	if err != nil {
		log.Printf("leaderboard banner for guild %s: %v", i.GuildID, err)
	}

	title := seasonKey
	if stat.key != "elo" {
		title += " · " + stat.label
	}
	return helpers.EditInteractionEmbed(s, i, LeaderboardOverviewEmbed(i, rows.Data, title, page, totalPages))
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
func sendLeaderboardBanner(s *discordgo.Session, i *discordgo.InteractionCreate, rows []sdk.LeaderboardEntry, season string, seasonID uint, stat leaderboardStat, page, totalPages int) (sent bool, err error) {
	invoker := invokingUser(i)
	if invoker == nil {
		return false, nil
	}
	// The ten rows' players are looked up while the rest (the viewer's own profile, their style, their position) is loaded.
	rowVars := banner.Vars{}
	rowsDone := make(chan struct{})
	go func() {
		defer close(rowsDone)
		banner.LeaderboardRowVars(s, i.GuildID, rows, rowVars)
	}()
	doc := banner.LoadBanners(i.GuildID)
	viewer := banner.BannerViewer(invoker.ID, i.Member)

	view, verr := database.Must().Player.Get(i.GuildID, invoker.ID)
	preferred := ""
	if verr == nil {
		preferred = banner.ChosenBanner(view.Config, "leaderboard")
	}
	if style := helpers.OptionString(i, "style"); style != "" {
		for _, b := range doc.Eligible("leaderboard", viewer) {
			if b.ID == style {
				preferred = style
				if verr == nil {
					if err := banner.RememberBanner(i.GuildID, invoker.ID, view.Config, "leaderboard", style); err != nil {
						log.Printf("remember leaderboard banner: %v", err)
					}
				}
			}
		}
	}
	chosen := doc.Choose("leaderboard", viewer, preferred)
	if chosen == nil {
		return false, nil
	}

	vars := banner.GuildVars(s, i.GuildID, invoker)
	vars["lb.title"] = fmt.Sprintf("%s Leaderboard", season)
	vars["lb.stat"] = stat.label
	vars["season.name"] = season
	<-rowsDone
	maps.Copy(vars, rowVars)
	for n := range rows {
		vars[fmt.Sprintf("lb.%d.value", n+1)] = fmt.Sprint(stat.value(&rows[n].PlayerStatsModel))
	}
	vars["page.number"], vars["page.total"] = fmt.Sprint(page), fmt.Sprint(totalPages)
	if verr == nil {
		you(s, i.GuildID, invoker, view, seasonID, vars)
	}

	img, err := banner.RenderBanner(chosen, vars)
	if err != nil {
		return false, err
	}
	text := ""
	if totalPages > 1 {
		text = fmt.Sprintf("Page %d/%d", page, totalPages)
	}
	return true, helpers.EditInteractionFile(s, i, text, "leaderboard.png", img)
}

func you(s *discordgo.Session, guildID string, invoker *discordgo.User, view *sdk.PlayerView, seasonID uint, vars banner.Vars) {
	playerID := sdk.UintToString(view.Player.ID)
	position := make(chan int, 1) // the position is asked for while the stats load
	go func() {
		pos := 0
		if rank, err := database.Must().Stats.Rank(guildID, playerID, seasonID); err == nil {
			pos = rank.Position
		}
		position <- pos
	}()
	st, err := database.Must().Stats.Get(guildID, playerID, seasonID)
	pos := <-position
	if err != nil {
		return
	}
	banner.EntryVars(vars, "me.", pos, view.Config.Username, banner.AvatarOf(invoker), st)
}

// ============================================================================
// Helper functions
// ============================================================================

func UnexpectedErrorLeaderboardEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the leaderboard for this guild. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func LeaderboardNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func LeaderboardEmptyEmbed(i *discordgo.InteractionCreate, season string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("Leaderboard · %s", season),
		Description: "No players have stats for the active season yet.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func LeaderboardEmptyPageEmbed(i *discordgo.InteractionCreate, page int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Page Empty",
		Description: fmt.Sprintf("There are no leaderboard entries on page `%d`. Try a lower page number.", page),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func LeaderboardOverviewEmbed(i *discordgo.InteractionCreate, rows []sdk.LeaderboardEntry, season string, page, totalPages int) *discordgo.MessageEmbed {
	var b strings.Builder
	for _, row := range rows {
		total := row.Wins + row.Losses
		winRate := 0.0
		if total > 0 {
			winRate = float64(row.Wins) / float64(total) * 100
		}
		b.WriteString(helpers.FillLayout(helpers.GuildOf(i), helpers.LayoutLeaderboardRow, map[string]string{
			"position": fmt.Sprint(row.Position), "elo": fmt.Sprint(row.Elo), "wins": fmt.Sprint(row.Wins), "losses": fmt.Sprint(row.Losses),
			"games": fmt.Sprint(total), "win_rate": fmt.Sprintf("%.0f%%", winRate), "rank": row.Rank, "peak": fmt.Sprint(row.HighestElo),
			"kills": fmt.Sprint(row.Kills), "deaths": fmt.Sprint(row.Deaths), "final_kills": fmt.Sprint(row.FinalKills), "beds": fmt.Sprint(row.BedsDestroyed), "mvps": fmt.Sprint(row.MvpCount),
		}) + "\n")
	}

	return helpers.Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("Leaderboard · %s", season),
		Description: b.String(),
		FooterText:  fmt.Sprintf("Page %d/%d", page, totalPages),
		Thumbnail:   helpers.ThumbPlayers,
	})
}
