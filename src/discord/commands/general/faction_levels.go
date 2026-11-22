package general

import (
	"fmt"
	"log"
	"ranked-bedwars/src/discord/helpers/pagination"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/factionmgr"
	"ranked-bedwars/src/types"
)

func init() { helpers.RegisterComponentHandler(handleFactionLevels) }

const (
	factionLevelsPerPage = 15
	factionLevelsPrefix  = "fclv|" // fclv|<page to go to>; an inert button is fclv|off-<slot>
)

// levelNeeds is the lifetime trophies level n takes.
func levelNeeds(cfg *database.FactionConfig, n int) int {
	if n < 1 || n > len(cfg.Levels) {
		return 0
	}
	return cfg.Levels[n-1]
}

// levelSeats is the seats and officer seats a faction has at level n (the tier it has reached, capped by MaxSlots).
func levelSeats(cfg *database.FactionConfig, n int) (slots, officers int) {
	for _, t := range cfg.Slots {
		if t.Level <= n || slots == 0 {
			slots, officers = t.Slots, t.Officers
		}
	}
	if cfg.MaxSlots > 0 {
		slots = min(slots, cfg.MaxSlots)
	}
	return slots, officers
}

// levelRewards lists what reaching level n opens up, apart from the trophies: cosmetic rewards, a bigger roster and prestige titles.
func levelRewards(cfg *database.FactionConfig, n int) []string {
	var out []string
	for _, r := range []struct {
		at   int
		name string
	}{{cfg.Unlocks.Tag, "Faction Tag"}, {cfg.Unlocks.Color, "Faction Colour"}, {cfg.Unlocks.Role, "Discord Faction Role"}, {cfg.Unlocks.Icon, "Faction Icon"},
		{cfg.Unlocks.Advanced, "Advanced Customization (tag symbols)"}, {cfg.Unlocks.Prestige, "Prestige Badge"}, {cfg.Unlocks.Legendary, "Legendary Status"}} {
		if r.at == n || (n == 1 && r.at < 1) {
			out = append(out, r.name)
		}
	}
	for _, t := range cfg.Slots {
		if t.Level == n && n > 1 {
			out = append(out, fmt.Sprintf("Roster: %d seats, %d officer", min(t.Slots, max(cfg.MaxSlots, 1)), t.Officers))
		}
	}
	for _, b := range cfg.Prestige {
		if b.Level == n {
			out = append(out, "Title: "+b.Name)
		}
	}
	return out
}

// levelsOverview is one page of every level with its trophies, seats and rewards. have > 0 marks the levels a faction has reached.
func levelsOverview(cfg *database.FactionConfig, page int, level int) *discordgo.MessageEmbed {
	pages := max(1, (cfg.MaxLevel+factionLevelsPerPage-1)/factionLevelsPerPage)
	page = min(max(page, 1), pages)
	var b strings.Builder
	for n := (page-1)*factionLevelsPerPage + 1; n <= min(page*factionLevelsPerPage, cfg.MaxLevel); n++ {
		mark := "▫️"
		switch {
		case level == n:
			mark = "📍"
		case level > n:
			mark = "✅"
		}
		slots, officers := levelSeats(cfg, n)
		fmt.Fprintf(&b, "%s **Level %d** · %s · %d seats (%d officer)", mark, n, factionmgr.Trophies(levelNeeds(cfg, n)), slots, officers)
		if rewards := levelRewards(cfg, n); len(rewards) > 0 {
			b.WriteString("\n> " + strings.Join(rewards, " · "))
		}
		b.WriteString("\n")
	}
	return helpers.Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Faction Levels · Page %d/%d", page, pages),
		Description: b.String() + fmt.Sprintf("\nA ranked win earns the faction **%s**, **%s** more for the MVP. Trophies are lifetime trophies. Use `/faction level level:<n>` for one level.", factionmgr.Trophies(cfg.Trophies.PerWin), factionmgr.Trophies(cfg.Trophies.MvpBonus)),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

// levelDetail is one level: what it takes, what it gives and, when the faction is known, how far it is.
func levelDetail(cfg *database.FactionConfig, n int, p *database.FactionProfile) *discordgo.MessageEmbed {
	slots, officers := levelSeats(cfg, n)
	rewards := "Nothing new at this level."
	if r := levelRewards(cfg, n); len(r) > 0 {
		rewards = "• " + strings.Join(r, "\n• ")
	}
	desc := fmt.Sprintf("> **Trophies needed:** %s\n> **Seats:** %d (%d officer)", factionmgr.Trophies(levelNeeds(cfg, n)), slots, officers)
	opts := types.PresetOptions{
		Title:     fmt.Sprintf("Faction Level %d", n),
		Fields:    []*discordgo.MessageEmbedField{{Name: "Unlocks", Value: rewards}},
		Thumbnail: helpers.ThumbPlayers,
	}
	if p != nil {
		opts.Color = factionColor(p)
		switch {
		case p.Level >= n:
			desc += fmt.Sprintf("\n\n✅ **%s** has reached this level.", p.Faction.Name)
		default:
			desc += fmt.Sprintf("\n\n**%s** is level %d and needs **%s** more.", p.Faction.Name, p.Level, factionmgr.Trophies(max(levelNeeds(cfg, n)-p.LifetimeTrophies, 0)))
		}
	}
	opts.Description = desc
	return helpers.Preset("info", opts)
}

// levelStanding is a faction's level: progress, what it has unlocked, and the next few levels with their rewards.
func levelStanding(cfg *database.FactionConfig, p *database.FactionProfile) *discordgo.MessageEmbed {
	desc := fmt.Sprintf("> **Level:** %d / %d\n> **Lifetime trophies:** %s", p.Level, p.MaxLevel, factionmgr.Trophies(p.LifetimeTrophies))
	if p.NextLevelAt > 0 {
		desc += fmt.Sprintf("\n> **Progress:** %s %s / %s (%s to go)", progressBar(p.LifetimeTrophies, p.LevelStartAt, p.NextLevelAt),
			factionmgr.Thousands(p.LifetimeTrophies), factionmgr.Trophies(p.NextLevelAt), factionmgr.Trophies(p.NextLevelAt-p.LifetimeTrophies))
	} else {
		desc += "\n> **Max level reached.**"
	}
	desc += fmt.Sprintf("\n> **Seats:** %d (%d officer)", p.Slots, p.OfficerSlots)
	if p.Prestige != "" {
		desc += "\n> **Title:** " + p.Prestige
	}

	var unlocked []string
	for _, u := range []struct {
		on   bool
		name string
	}{{p.Unlocked.Tag, "Tag"}, {p.Unlocked.Color, "Colour"}, {p.Unlocked.Role, "Role"}, {p.Unlocked.Icon, "Icon"}, {p.Unlocked.Advanced, "Advanced"}, {p.Unlocked.Prestige, "Prestige"}, {p.Unlocked.Legendary, "Legendary"}} {
		if u.on {
			unlocked = append(unlocked, u.name)
		}
	}
	fields := []*discordgo.MessageEmbedField{{Name: "Unlocked", Value: map[bool]string{true: strings.Join(unlocked, " · "), false: "Nothing yet"}[len(unlocked) > 0]}}

	var upcoming strings.Builder
	for n := p.Level + 1; n <= min(p.Level+3, cfg.MaxLevel); n++ {
		slots, officers := levelSeats(cfg, n)
		fmt.Fprintf(&upcoming, "**Level %d** · %s · %d seats (%d officer)", n, factionmgr.Trophies(levelNeeds(cfg, n)), slots, officers)
		if r := levelRewards(cfg, n); len(r) > 0 {
			upcoming.WriteString("\n> " + strings.Join(r, " · "))
		}
		upcoming.WriteString("\n")
	}
	if upcoming.Len() > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Coming Up", Value: upcoming.String()})
	}
	if next := factionNextUnlock(p, cfg); next != "" {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Next Reward", Value: next})
	}
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Faction Level · " + factionHeading(p.Faction.Name, p.DisplayTag),
		Description: desc + "\n\nSee every level with `/faction levels`, or one with `/faction level level:<n>`.",
		Fields:      fields,
		Color:       factionColor(p),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

// factionLevel answers /faction level: one level of the ruleset when `level` is given, otherwise the faction's own standing.
func factionLevel(c *factionCtx) error {
	cfg, err := database.FactionConfigGet(c.guildID)
	if err != nil {
		return err
	}
	var p *database.FactionProfile
	if name := c.str("faction"); strings.TrimSpace(name) != "" {
		if p, err = c.named(name); err != nil {
			return err
		}
	} else if p, err = database.FactionFor(c.guildID, c.userID); err != nil {
		return err
	}
	if n := c.integer("level"); n > 0 {
		if n > cfg.MaxLevel {
			return FactionReply(FactionRefusedEmbed(fmt.Sprintf("The top faction level is **%d**.", cfg.MaxLevel)))
		}
		return c.send(levelDetail(cfg, n, p))
	}
	if p == nil { // not in a faction: show the ladder instead
		embed, components := levelsPage(cfg, 1, 0)
		return c.send(embed, components...)
	}
	return c.send(levelStanding(cfg, p))
}

// factionLevels answers /faction levels: a page of every level, with buttons to turn it.
func factionLevels(c *factionCtx) error {
	cfg, err := database.FactionConfigGet(c.guildID)
	if err != nil {
		return err
	}
	embed, components := levelsPage(cfg, c.integer("page"), factionLevelOf(c.guildID, c.userID))
	return c.send(embed, components...)
}

// factionLevelOf is the level of a player's faction, or 0 when they have none.
func factionLevelOf(guildID, userID string) int {
	if p, err := database.FactionFor(guildID, userID); err == nil && p != nil {
		return p.Level
	}
	return 0
}

// levelsPage is one page of levelsOverview with the buttons that turn pages.
func levelsPage(cfg *database.FactionConfig, page, level int) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	pages := max(1, (cfg.MaxLevel+factionLevelsPerPage-1)/factionLevelsPerPage)
	page = min(max(page, 1), pages)
	return levelsOverview(cfg, page, level), pagination.BuildPageRow(page, pages,
		func(target int) string { return fmt.Sprintf("%s%d", factionLevelsPrefix, target) },
		func(slot string) string { return factionLevelsPrefix + "off-" + slot })
}

// handleFactionLevels turns the page of the levels list. Only the player who ran the command can.
func handleFactionLevels(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	rest, ok := strings.CutPrefix(customID, factionLevelsPrefix)
	if !ok {
		return false
	}
	page, err := strconv.Atoi(rest)
	if err != nil || page < 1 {
		return true // an inert button: nothing to do
	}
	if factionOwnerOnly(s, i, "/faction levels") {
		return true
	}
	embed, components := UnexpectedErrorFactionEmbed(), []discordgo.MessageComponent{}
	if cfg, err := database.FactionConfigGet(i.GuildID); err != nil {
		log.Printf("[Faction] levels page in %s: %v", i.GuildID, err)
	} else {
		embed, components = levelsPage(cfg, page, factionLevelOf(i.GuildID, helpers.InteractionUserID(i)))
		if components == nil {
			components = []discordgo.MessageComponent{}
		}
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Components: components},
	})
	return true
}
