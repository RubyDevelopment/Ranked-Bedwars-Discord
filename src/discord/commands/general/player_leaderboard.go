package general

import (
	"bytes"
	"fmt"
	"log"
	"maps"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/banner"
	"ranked-bedwars/src/discord/helpers/pagination"
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
	short      string // the column heading on a banner (kept short: the column is narrow)
	value      func(*models.PlayerStatsModel) int
}

var leaderboardStats = []leaderboardStat{
	{"elo", "ELO", "ELO", func(m *models.PlayerStatsModel) int { return m.Elo }},
	{"highest_elo", "Peak ELO", "PEAK", func(m *models.PlayerStatsModel) int { return m.HighestElo }},
	{"wins", "Wins", "WINS", func(m *models.PlayerStatsModel) int { return m.Wins }},
	{"losses", "Losses", "LOSS", func(m *models.PlayerStatsModel) int { return m.Losses }},
	{"games_played", "Games Played", "GAMES", func(m *models.PlayerStatsModel) int { return m.GamesPlayed }},
	{"kills", "Kills", "KILLS", func(m *models.PlayerStatsModel) int { return m.Kills }},
	{"final_kills", "Final Kills", "FINAL", func(m *models.PlayerStatsModel) int { return m.FinalKills }},
	{"first_bloods", "First Bloods", "FIRST", func(m *models.PlayerStatsModel) int { return m.FirstBloods }},
	{"assists", "Assists", "ASST", func(m *models.PlayerStatsModel) int { return m.Assists }},
	{"beds_destroyed", "Beds Destroyed", "BEDS", func(m *models.PlayerStatsModel) int { return m.BedsDestroyed }},
	{"mvp_count", "MVPs", "MVPS", func(m *models.PlayerStatsModel) int { return m.MvpCount }},
	{"highest_win_streak", "Best Win Streak", "STRK", func(m *models.PlayerStatsModel) int { return m.HighestWinStreak }},
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

	stat := leaderboardStats[0]
	for _, st := range leaderboardStats {
		if st.key == helpers.OptionString(i, "stat") {
			stat = st
		}
	}
	view := buildLeaderboard(s, leaderboardAsk{
		guildID: i.GuildID, invoker: invokingUser(i), member: i.Member,
		page: max(1, helpers.OptionInt(i, "page")), stat: stat, style: helpers.OptionString(i, "style"),
	})
	if view.png != nil {
		return helpers.EditInteractionFile(s, i, view.text, "leaderboard.png", view.png, view.components...)
	}
	if len(view.components) > 0 {
		return helpers.EditInteractionComponents(s, i, view.embed, view.components)
	}
	return helpers.EditInteractionEmbed(s, i, view.embed)
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

// leaderboardAsk is everything one page of a leaderboard depends on, so the command and its page buttons build it the same way.
type leaderboardAsk struct {
	guildID string
	invoker *discordgo.User
	member  *discordgo.Member
	page    int
	stat    leaderboardStat
	style   string // a banner style named in the command ("" = the one the player chose before)
}

// leaderboardView is one page, ready to send: the banner picture, or the embed that stands in for it (or answers an error), and its buttons.
type leaderboardView struct {
	png        []byte
	embed      *discordgo.MessageEmbed
	text       string
	components []discordgo.MessageComponent
}

// leaderboardPagePrefix starts the custom id of the page buttons: lbpg|<page to go to>|<stat key>. An inert button is lbpg|off-<slot>.
const leaderboardPagePrefix = "lbpg|"

// buildLeaderboard loads one page of the leaderboard and draws it.
func buildLeaderboard(s *discordgo.Session, ask leaderboardAsk) *leaderboardView {
	failed := func(e *discordgo.MessageEmbed) *leaderboardView { return &leaderboardView{embed: e} }

	seasonKey, seasonID, err := helpers.ActiveSeasonKey(ask.guildID)
	if err != nil {
		return failed(UnexpectedErrorLeaderboardEmbed(nil))
	}
	if seasonKey == "" {
		return failed(LeaderboardNoActiveSeasonEmbed(nil))
	}
	rowData, total, err := database.Leaderboard(ask.guildID, seasonID, leaderboardPerPage, (ask.page-1)*leaderboardPerPage, ask.stat.key)
	if err != nil {
		log.Printf("leaderboard for guild %s: %v", ask.guildID, err)
		return failed(UnexpectedErrorLeaderboardEmbed(nil))
	}
	if len(rowData) == 0 {
		if ask.page > 1 {
			return failed(LeaderboardEmptyPageEmbed(nil, ask.page))
		}
		return failed(LeaderboardEmptyEmbed(nil, seasonKey))
	}
	totalPages := max(1, (total+leaderboardPerPage-1)/leaderboardPerPage)

	view := &leaderboardView{components: pagination.BuildPageRow(ask.page, totalPages,
		func(target int) string { return fmt.Sprintf("%s%d|%s", leaderboardPagePrefix, target, ask.stat.key) },
		func(slot string) string { return leaderboardPagePrefix + "off-" + slot })}

	img, err := renderLeaderboardBanner(s, ask, rowData, seasonKey, seasonID, totalPages)
	if img != nil {
		view.png = img
		return view
	}
	if err != nil {
		log.Printf("leaderboard banner for guild %s: %v", ask.guildID, err)
	}
	title := seasonKey
	if ask.stat.key != "elo" {
		title += " · " + ask.stat.label
	}
	view.embed = LeaderboardOverviewEmbed(nil, rowData, title, ask.page, totalPages)
	return view
}

// renderLeaderboardBanner draws the banner of a page. It returns nil (and no error) when the guild has no style the viewer may use.
func renderLeaderboardBanner(s *discordgo.Session, ask leaderboardAsk, rows []sdk.LeaderboardEntry, season string, seasonID uint, totalPages int) ([]byte, error) {
	invoker := ask.invoker
	if invoker == nil {
		return nil, nil
	}
	// The ten rows' players are looked up while the rest (the viewer's own profile, their style, their position) is loaded.
	rowVars := banner.Vars{}
	rowsDone := make(chan struct{})
	go func() {
		defer close(rowsDone)
		banner.LeaderboardRowVars(s, ask.guildID, rows, rowVars)
	}()
	doc := banner.LoadBanners(ask.guildID)
	viewer := banner.BannerViewer(invoker.ID, ask.member)

	view, verr := database.Must().Player.Get(ask.guildID, invoker.ID)
	preferred := ""
	if verr == nil {
		preferred = banner.ChosenBanner(view.Config, "leaderboard")
	}
	if ask.style != "" {
		for _, b := range doc.Eligible("leaderboard", viewer) {
			if b.ID == ask.style {
				preferred = ask.style
				if verr == nil {
					if err := banner.RememberBanner(ask.guildID, invoker.ID, view.Config, "leaderboard", ask.style); err != nil {
						log.Printf("remember leaderboard banner: %v", err)
					}
				}
			}
		}
	}
	chosen := doc.Choose("leaderboard", viewer, preferred)
	if chosen == nil {
		return nil, nil
	}

	// The viewer's own row is looked up while the page's players are (it does not depend on them).
	youVars := banner.Vars{}
	youDone := make(chan struct{})
	go func() {
		defer close(youDone)
		if verr == nil {
			you(s, ask.guildID, invoker, view, seasonID, ask.stat, youVars)
		}
	}()

	vars := banner.GuildVars(s, ask.guildID, invoker)
	vars["lb.title"] = fmt.Sprintf("%s Leaderboard", season)
	vars["lb.stat"], vars["lb.stat_short"] = ask.stat.label, ask.stat.short
	vars["season.name"] = season
	<-rowsDone
	maps.Copy(vars, rowVars)
	for n := range rows {
		vars[fmt.Sprintf("lb.%d.value", n+1)] = fmt.Sprint(ask.stat.value(&rows[n].PlayerStatsModel))
	}
	vars["page.number"], vars["page.total"] = fmt.Sprint(ask.page), fmt.Sprint(totalPages)
	<-youDone
	maps.Copy(vars, youVars)
	return banner.RenderBanner(chosen, vars)
}

// ============================================================================
// Page buttons
// ============================================================================

func init() { helpers.RegisterComponentHandler(handleLeaderboardPage) }

// handleLeaderboardPage turns the page of a leaderboard when one of its buttons is pressed: the banner is drawn again for the new page and
// replaces the old one. Only the player who ran the command turns its pages (the banner shows their own row).
func handleLeaderboardPage(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	rest, ok := strings.CutPrefix(customID, leaderboardPagePrefix)
	if !ok {
		return false
	}
	target, statKey, found := strings.Cut(rest, "|")
	page, err := strconv.Atoi(target)
	if !found || err != nil || page < 1 {
		return true // an inert button (the current page, or a button at the end): nothing to do
	}
	stat := leaderboardStats[0]
	for _, st := range leaderboardStats {
		if st.key == statKey {
			stat = st
		}
	}

	pressed := helpers.InteractionUserID(i)
	if owner := pagination.MessageOwner(i); owner != "" && owner != pressed {
		_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Flags:  discordgo.MessageFlagsEphemeral,
				Embeds: []*discordgo.MessageEmbed{helpers.Preset("error", types.PresetOptions{Title: "Permission Denied", Description: "Only the player who ran this command can change pages. Run `/leaderboard` to see your own."})},
			},
		})
		return true
	}

	// Drawing a page takes a moment: acknowledge the press now, then replace the message.
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})
	user := i.User
	if i.Member != nil && i.Member.User != nil {
		user = i.Member.User
	}
	view := buildLeaderboard(s, leaderboardAsk{guildID: i.GuildID, invoker: user, member: i.Member, page: page, stat: stat})

	edit := leaderboardPageEdit(view)
	if err := helpers.EditInteractionResponse(s, i, edit); err != nil {
		log.Printf("leaderboard page for guild %s: %v", i.GuildID, err)
	}
	return true
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

// you adds the viewer's own row: their stats, their value of the chosen stat (me.value) and where they stand on that stat.
func you(s *discordgo.Session, guildID string, invoker *discordgo.User, view *sdk.PlayerView, seasonID uint, stat leaderboardStat, vars banner.Vars) {
	// Turning pages asks for the same viewer row again and again (and for a stat the database does not rank, finding the position reads
	// thousands of players), so it is kept for a short while.
	key := fmt.Sprintf("%s|%d|%d|%s", guildID, seasonID, view.Player.ID, stat.key)
	youCache.Lock()
	hit, ok := youCache.m[key]
	youCache.Unlock()
	if ok && time.Now().Before(hit.until) {
		maps.Copy(vars, hit.vars)
		return
	}
	mine := banner.Vars{}
	youFresh(guildID, invoker, view, seasonID, stat, mine)
	if len(mine) > 0 {
		youCache.Lock()
		if len(youCache.m) > 500 {
			clear(youCache.m)
		}
		youCache.m[key] = youEntry{vars: mine, until: time.Now().Add(youCacheFor)}
		youCache.Unlock()
	}
	maps.Copy(vars, mine)
}

const youCacheFor = 45 * time.Second

type youEntry struct {
	vars  banner.Vars
	until time.Time
}

var youCache = struct {
	sync.Mutex
	m map[string]youEntry
}{m: map[string]youEntry{}}

func youFresh(guildID string, invoker *discordgo.User, view *sdk.PlayerView, seasonID uint, stat leaderboardStat, vars banner.Vars) {
	playerID := sdk.UintToString(view.Player.ID)
	position := make(chan int, 1) // the position is asked for while the stats load
	go func() { position <- positionOn(guildID, seasonID, view.Player.ID, stat) }()
	st, err := database.Must().Stats.Get(guildID, playerID, seasonID)
	pos := <-position
	if err != nil {
		return
	}
	banner.EntryVars(vars, "me.", pos, view.Config.Username, banner.AvatarOf(invoker), st)
	vars["me.value"] = fmt.Sprint(stat.value(st))
}

// positionOn is a player's place on the leaderboard ordered by stat, or 0 when it cannot be found. The database ranks by ELO only, so
// for any other stat the leaderboard is read in pages of 200 (five at a time) until the player appears.
func positionOn(guildID string, seasonID, playerID uint, stat leaderboardStat) int {
	if stat.key == "elo" {
		if rank, err := database.Must().Stats.Rank(guildID, sdk.UintToString(playerID), seasonID); err == nil {
			return rank.Position
		}
		return 0
	}
	const pageSize, batch, maxPages = 200, 5, 25 // reads at most the top 5000 players
	for first := 0; first < maxPages; first += batch {
		found := make(chan int, batch)
		var wg sync.WaitGroup
		more := false
		var mu sync.Mutex
		for p := first; p < first+batch && p < maxPages; p++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rows, err := database.Must().Stats.Leaderboard(guildID, seasonID, sdk.Page{Limit: pageSize, Offset: p * pageSize, Sort: stat.key})
				if err != nil {
					return
				}
				for _, row := range rows.Data {
					if row.PlayerID == playerID {
						found <- row.Position
						return
					}
				}
				if len(rows.Data) == pageSize {
					mu.Lock()
					more = true
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		select {
		case pos := <-found:
			return pos
		default:
		}
		if !more {
			return 0
		}
	}
	return 0
}

// leaderboardPageEdit is the edit that replaces a leaderboard message with another page: the old banner is dropped (attachments: [], or it
// would stay beside the new one) and the new one attached, or the embed that stands in for it.
func leaderboardPageEdit(view *leaderboardView) *discordgo.WebhookEdit {
	components := view.components
	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	edit := &discordgo.WebhookEdit{Components: &components, Attachments: &[]*discordgo.MessageAttachment{}}
	if view.png != nil {
		edit.Content = &view.text
		edit.Embeds = &[]*discordgo.MessageEmbed{}
		edit.Files = []*discordgo.File{{Name: "leaderboard.png", ContentType: "image/png", Reader: bytes.NewReader(view.png)}}
		return edit
	}
	empty := ""
	edit.Content = &empty
	edit.Embeds = &[]*discordgo.MessageEmbed{view.embed}
	return edit
}
