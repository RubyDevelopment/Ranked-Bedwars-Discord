package general

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/factionmgr"
	"ranked-bedwars/src/discord/helpers/pagination"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.RegisterComponentHandler(handleFactionView)
}

// factionViewPrefix starts the custom id of a tab button: fcv|<tab>|<faction name>.
const factionViewPrefix = "fcv|"

var factionTabs = []struct{ key, label string }{
	{"home", "Home"},
	{"members", "Members"},
	{"levels", "Levels"},
	{"achievements", "Achievements"},
}

// ============================================================================
// Command Execution
// ============================================================================

// factionInfo shows a profile: the faction named, the one the picked player is in, or the invoker's own.
func factionInfo(c *factionCtx) error {
	var p *database.FactionProfile
	var err error
	user := helpers.SubOptionUser(c.s, c.options, "user")
	switch name := c.str("faction"); {
	case strings.TrimSpace(name) != "":
		p, err = c.named(name)
	case user != nil && user.Bot:
		return FactionReply(FactionBotTargetEmbed())
	case user != nil && user.ID != c.userID:
		if p, err = database.FactionFor(c.guildID, user.ID); err == nil && p == nil {
			return FactionReply(FactionPlayerHasNoneEmbed(user.ID))
		}
	default:
		p, err = c.mine()
	}
	if err != nil {
		return err
	}
	cfg, _ := database.FactionConfigGet(c.guildID) // only the "next unlock" line needs it
	embed, components := factionView(p, cfg, "home")
	return c.send(embed, components...)
}

// ============================================================================
// Views
// ============================================================================

// factionView is one tab of /faction info with the row of buttons that switches tabs.
func factionView(p *database.FactionProfile, cfg *database.FactionConfig, tab string) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	var embed *discordgo.MessageEmbed
	switch tab {
	case "members":
		embed = factionMembersEmbed(p)
	case "levels":
		if cfg == nil {
			embed = UnexpectedErrorFactionEmbed()
		} else {
			embed = levelStanding(cfg, p)
		}
	case "achievements":
		embed = factionAchievementsEmbed(p, cfg)
	default:
		tab, embed = "home", factionHomeEmbed(p, cfg)
	}
	if icon := p.Faction.Icon; strings.HasPrefix(icon, "http") {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: icon}
	}

	row := &discordgo.ActionsRow{}
	for _, t := range factionTabs {
		b := &discordgo.Button{CustomID: factionViewPrefix + t.key + "|" + p.Faction.Name, Label: t.label, Style: discordgo.SecondaryButton}
		if t.key == tab { // the tab being shown
			b.Style, b.Disabled = discordgo.PrimaryButton, true
		}
		row.Components = append(row.Components, b)
	}
	return embed, []discordgo.MessageComponent{row}
}

// factionTitle is the heading of a faction's embeds: icon, tag and name, flagged when it is Legendary.
func factionTitle(p *database.FactionProfile) string {
	title := factionHeading(p.Faction.Name, p.DisplayTag)
	if p.Faction.Icon != "" && !strings.HasPrefix(p.Faction.Icon, "http") {
		title = p.Faction.Icon + " " + title
	}
	if p.Legendary {
		title = "👑 " + title
	}
	return title
}

// factionHomeEmbed is the overview: who leads it, how far it has come, its trophies, ranks and seats.
func factionHomeEmbed(p *database.FactionProfile, cfg *database.FactionConfig) *discordgo.MessageEmbed {
	level := fmt.Sprintf("`%d`", p.Level)
	if p.Level >= p.MaxLevel {
		level += " (max)"
	}
	if p.Prestige != "" {
		level += " · " + p.Prestige
	}
	lines := []string{
		"> **Founder:** <@" + p.Founder + ">",
		"> **Level:** " + level,
	}
	if p.NextLevelAt > 0 {
		lines = append(lines, fmt.Sprintf("> **Progress:** %s `%s / %s`", progressBar(p.LifetimeTrophies, p.LevelStartAt, p.NextLevelAt), factionmgr.Thousands(p.LifetimeTrophies), factionmgr.Thousands(p.NextLevelAt)))
	}
	if next := factionNextUnlock(p, cfg); next != "" {
		lines = append(lines, "> **Next unlock:** "+next)
	}
	lines = append(lines, fmt.Sprintf("> **Created:** <t:%d:D>", p.Faction.CreatedAt.Unix()))

	title := factionTitle(p)
	if p.Legendary {
		title += " · LEGENDARY"
	}
	return helpers.Preset("info", types.PresetOptions{
		Title:       title,
		Description: strings.Join(lines, "\n"),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Trophies", Value: fmt.Sprintf("Lifetime: %s\nSeasonal: %s", factionmgr.Trophies(p.LifetimeTrophies), factionmgr.Trophies(p.SeasonTrophies)), Inline: true},
			{Name: "Rank", Value: fmt.Sprintf("Lifetime: **#%d**\nSeasonal: **#%d**", p.LifetimeRank, max(p.SeasonRank, 1)), Inline: true},
			{Name: "Roster", Value: fmt.Sprintf("Members: **%d/%d**\nOfficers: **%d/%d**", len(p.Members), p.Slots, countRole(p.Members, "officer"), p.OfficerSlots), Inline: true},
		},
		Color:     factionColor(p),
		Thumbnail: helpers.ThumbPlayers,
	})
}

// factionMembersEmbed lists the Founder, the Officers and the Members, the biggest contributors first.
func factionMembersEmbed(p *database.FactionProfile) *discordgo.MessageEmbed {
	members := append([]database.FactionMember(nil), p.Members...)
	sort.SliceStable(members, func(a, b int) bool { return members[a].Trophies > members[b].Trophies })

	var fields []*discordgo.MessageEmbedField
	for _, g := range []struct{ role, name string }{{"founder", "Founder"}, {"officer", fmt.Sprintf("Officers (%d/%d)", countRole(p.Members, "officer"), p.OfficerSlots)}, {"member", "Members"}} {
		fields = append(fields, &discordgo.MessageEmbedField{Name: g.name, Value: factionRoster(members, g.role)})
	}
	return helpers.Preset("info", types.PresetOptions{
		Title:       factionTitle(p) + " · Members",
		Description: fmt.Sprintf("> **Seats:** %d/%d filled\n> **Lifetime trophies:** %s", len(p.Members), p.Slots, factionmgr.Trophies(p.LifetimeTrophies)),
		Fields:      fields,
		Color:       factionColor(p),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

// factionRoster writes the members of one role, a line each with the trophies they earned, cut to fit a field.
func factionRoster(members []database.FactionMember, role string) string {
	var b strings.Builder
	shown, total := 0, countRole(members, role)
	for _, m := range members {
		if m.Role != role {
			continue
		}
		line := fmt.Sprintf("<@%s> · %s\n", m.SnowflakeID, factionmgr.Trophies(m.Trophies))
		if b.Len()+len(line) > 900 {
			break
		}
		b.WriteString(line)
		shown++
	}
	switch {
	case total == 0:
		return "—"
	case shown < total:
		fmt.Fprintf(&b, "*+%d more*", total-shown)
	}
	return strings.TrimSpace(b.String())
}

// factionAchievementsEmbed shows what the faction has unlocked, what is still locked and when, and its seasonal badges.
func factionAchievementsEmbed(p *database.FactionProfile, cfg *database.FactionConfig) *discordgo.MessageEmbed {
	var perks strings.Builder
	for _, u := range []struct {
		on   bool
		name string
		at   func(database.FactionUnlocks) int
	}{
		{p.Unlocked.Tag, "Faction Tag", func(u database.FactionUnlocks) int { return u.Tag }},
		{p.Unlocked.Color, "Faction Colour", func(u database.FactionUnlocks) int { return u.Color }},
		{p.Unlocked.Role, "Discord Faction Role", func(u database.FactionUnlocks) int { return u.Role }},
		{p.Unlocked.Icon, "Faction Icon", func(u database.FactionUnlocks) int { return u.Icon }},
		{p.Unlocked.Advanced, "Advanced Customization", func(u database.FactionUnlocks) int { return u.Advanced }},
		{p.Unlocked.Prestige, "Prestige Badge", func(u database.FactionUnlocks) int { return u.Prestige }},
		{p.Unlocked.Legendary, "Legendary Status", func(u database.FactionUnlocks) int { return u.Legendary }},
	} {
		switch {
		case u.on:
			perks.WriteString("✅ " + u.name + "\n")
		case cfg != nil:
			fmt.Fprintf(&perks, "🔒 %s · level %d\n", u.name, u.at(cfg.Unlocks))
		default:
			perks.WriteString("🔒 " + u.name + "\n")
		}
	}

	badges := "No seasonal badges yet. Finish a season among the top factions to earn one."
	if len(p.Badges) > 0 {
		var lines []string
		for _, b := range p.Badges {
			lines = append(lines, fmt.Sprintf("🏅 %s · placed **#%d**", b.Title, b.Placement))
		}
		badges = strings.Join(lines, "\n")
	}
	return helpers.Preset("info", types.PresetOptions{
		Title: factionTitle(p) + " · Achievements",
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Unlocks", Value: strings.TrimSpace(perks.String())},
			{Name: "Seasonal Badges", Value: badges},
		},
		Color:     factionColor(p),
		Thumbnail: helpers.ThumbPlayers,
	})
}

func countRole(members []database.FactionMember, role string) int {
	n := 0
	for _, m := range members {
		if m.Role == role {
			n++
		}
	}
	return n
}

// ============================================================================
// Buttons
// ============================================================================

// factionOwnerOnly answers "only the player who ran this command" and returns true when someone else pressed a button.
func factionOwnerOnly(s *discordgo.Session, i *discordgo.InteractionCreate, command string) bool {
	owner := pagination.MessageOwner(i)
	if owner == "" || owner == helpers.InteractionUserID(i) {
		return false
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral, Embeds: []*discordgo.MessageEmbed{
			helpers.Preset("error", types.PresetOptions{Title: "Permission Denied", Description: "Only the player who ran this command can use its buttons. Run `" + command + "` to see your own."}),
		}},
	})
	return true
}

// handleFactionView switches the tab of a faction profile. Only the player who ran the command can.
func handleFactionView(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	rest, ok := strings.CutPrefix(customID, factionViewPrefix)
	if !ok {
		return false
	}
	tab, name, found := strings.Cut(rest, "|")
	if !found {
		return true
	}
	if factionOwnerOnly(s, i, "/faction info") {
		return true
	}
	embed, components := UnexpectedErrorFactionEmbed(), []discordgo.MessageComponent{}
	switch p, err := database.FactionNamed(i.GuildID, name); {
	case err != nil:
		log.Printf("[Faction] profile tab in %s: %v", i.GuildID, err)
	case p == nil:
		embed = FactionNotFoundEmbed(name)
	default:
		cfg, _ := database.FactionConfigGet(i.GuildID)
		embed, components = factionView(p, cfg, tab)
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Components: components},
	})
	return true
}
