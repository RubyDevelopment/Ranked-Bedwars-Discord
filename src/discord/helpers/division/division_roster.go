package division

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// A roster is one player per line, "<@id> (`username`)", alphabetical by username, 20 to a page with Previous / Next buttons. The
// username is written out because Discord shows a mention as a raw ID for any user the viewer's client has not loaded.

const (
	pageSize   = 20
	pagePrefix = "divpage|" // divpage|<division>|<list|specs|managers>|<page>
)

func init() { helpers.RegisterComponentHandler(handlePage) }

type rosterLine struct{ user, text string }

// rosterLines lists the holders of the role of kind: the division's members ("list"), specs ("specs") or managers ("managers").
// notSet is true when the role is not configured.
func rosterLines(s *discordgo.Session, guildID string, d Div, cfg Config, kind string) (title string, lines []string, notSet bool, err error) {
	members, err := guildMembers(s, guildID)
	if err != nil {
		return "", nil, false, err
	}
	var rows []rosterLine
	switch kind {
	case "managers":
		title = d.Label + " Managers"
		if cfg.Manager == "" {
			return title, nil, true, nil
		}
		for _, m := range holders(members, cfg.Manager) {
			rows = append(rows, rosterLine{m.User.Username, fmt.Sprintf("<@%s> (`%s`)", m.User.ID, m.User.Username)})
		}
	default:
		name, roleID := "Members", cfg.Member
		if kind == "specs" {
			name, roleID = "Specs", cfg.Spec
		}
		title = d.Label + " " + name
		if roleID == "" {
			return title, nil, true, nil
		}
		for _, m := range holders(members, roleID) {
			rows = append(rows, rosterLine{m.User.Username, fmt.Sprintf("<@%s> (`%s`)", m.User.ID, m.User.Username)})
		}
	}
	sort.Slice(rows, func(a, b int) bool { return strings.ToLower(rows[a].user) < strings.ToLower(rows[b].user) })
	for _, r := range rows {
		lines = append(lines, r.text)
	}
	return title, lines, false, nil
}

// Roster builds one page of a roster and its buttons (none when it fits on one page).
func Roster(s *discordgo.Session, guildID string, d Div, cfg Config, kind string, page int) (*discordgo.MessageEmbed, []discordgo.MessageComponent, error) {
	title, lines, notSet, err := rosterLines(s, guildID, d, cfg, kind)
	if err != nil {
		return nil, nil, err
	}
	if notSet {
		return helpers.Preset("default", types.PresetOptions{Title: title, Description: "`Not configured`", Thumbnail: helpers.ThumbPlayers}), nil, nil
	}
	pages := max(1, (len(lines)+pageSize-1)/pageSize)
	page = min(max(page, 0), pages-1)
	body := "*None*"
	if len(lines) > 0 {
		body = strings.Join(lines[page*pageSize:min(len(lines), (page+1)*pageSize)], "\n")
	}
	embed := helpers.Preset("default", types.PresetOptions{
		Title:       fmt.Sprintf("%s · Page %d/%d", title, page+1, pages),
		Description: fmt.Sprintf("**Total:** %d\n\n%s", len(lines), body),
		Thumbnail:   helpers.ThumbPlayers,
	})
	if pages == 1 {
		return embed, nil, nil
	}
	id := func(p int) string { return pagePrefix + d.Key + "|" + kind + "|" + strconv.Itoa(p) }
	return embed, []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		&discordgo.Button{Label: "Previous", Style: discordgo.SecondaryButton, CustomID: id(page - 1), Disabled: page == 0},
		&discordgo.Button{Label: "Next", Style: discordgo.PrimaryButton, CustomID: id(page + 1), Disabled: page == pages-1},
	}}}, nil
}

// handlePage turns the page of a roster.
func handlePage(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, pagePrefix) {
		return false
	}
	parts := strings.Split(customID, "|")
	if len(parts) != 4 {
		return false
	}
	d, ok := Find(parts[1])
	page, perr := strconv.Atoi(parts[3])
	if !ok || perr != nil {
		return false
	}
	cfg, err := Load(i.GuildID, d)
	if err != nil {
		return true
	}
	embed, rows, err := Roster(s, i.GuildID, d, cfg, parts[2], page)
	if err != nil {
		return true
	}
	if rows == nil {
		rows = []discordgo.MessageComponent{}
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Components: rows},
	})
	return true
}
