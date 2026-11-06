package integrity

import (
	"fmt"
	"strconv"
	"strings"

	"ranked-bedwars/src/discord/helpers/pagination"

	"github.com/bwmarrin/discordgo"
)

// PerPage is how many findings a page of the overview shows.
const PerPage = 5

// Custom ids of the overview's components: intg|pg|<page>|<kind> (a page button), intg|off-<slot> (one that cannot be pressed),
// intg|open (the menu that opens a finding's evidence) and intg|kind (the menu that filters by kind).
const (
	PagePrefix = "intg|pg|"
	OffPrefix  = "intg|off-"
	OpenID     = "intg|open"
	KindID     = "intg|kind"
)

var kindOrder = []string{"win_trading", "boosting", "throwing", "void_dodging"}

// matching is the findings of a kind ("" is all of them), each with the number it has in the whole report.
type numbered struct {
	n int
	f Finding
}

func matching(rep *Report, kind string) []numbered {
	var out []numbered
	for i, f := range rep.Findings {
		if kind == "" || f.Kind == kind {
			out = append(out, numbered{i + 1, f})
		}
	}
	return out
}

// gamesRange writes a finding's games as a span and a count: "7 games (#26464–#26966)".
func gamesRange(games []int) string {
	if len(games) == 0 {
		return "no games"
	}
	lo, hi := games[0], games[0]
	for _, g := range games {
		lo, hi = min(lo, g), max(hi, g)
	}
	if lo == hi {
		return fmt.Sprintf("1 game (#%d)", lo)
	}
	return fmt.Sprintf("%d games (#%d–#%d)", len(games), lo, hi)
}

// Pages is how many pages the overview has for a kind.
func Pages(rep *Report, kind string) int {
	return max(1, (len(matching(rep, kind))+PerPage-1)/PerPage)
}

// Overview is one page of the season at a glance: the counts by severity, who is flagged most, and the findings in order of seriousness, with
// the page buttons, a menu to open a finding's evidence and a menu to filter by kind.
func Overview(rep *Report, kind string, page int) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	list := matching(rep, kind)
	pages := Pages(rep, kind)
	page = min(max(1, page), pages)

	counts := map[string]int{}
	worst := ""
	for _, f := range rep.Findings {
		counts[f.Severity]++
		if severityRank[f.Severity] > severityRank[worst] {
			worst = f.Severity
		}
	}
	color := 0x22c55e
	if worst != "" {
		color = severityColor[worst]
	}
	e := &discordgo.MessageEmbed{Title: "🛡️ Ladder integrity", Color: color}
	var d strings.Builder
	fmt.Fprintf(&d, "**%d** scored games · %s – %s · %d players · %d voided\n", rep.GamesAnalysed, day(rep, true), day(rep, false), rep.Players, rep.GamesVoided)
	if len(rep.Findings) == 0 {
		d.WriteString("\n✅ **Nothing stands out.** Nothing here is unlikely enough to be more than luck.")
		e.Description = d.String()
		return e, nil
	}
	d.WriteString(fmt.Sprintf("%s %d critical  ·  %s %d high  ·  %s %d medium  ·  %s %d low\n",
		severityIcon["critical"], counts["critical"], severityIcon["high"], counts["high"], severityIcon["medium"], counts["medium"], severityIcon["low"], counts["low"]))
	d.WriteString("\n*Each line is a probability worked out from the results, not a verdict. Open a finding to see the games it rests on.*")
	e.Description = d.String()

	if page == 1 && kind == "" && len(rep.Risk) > 0 {
		var b strings.Builder
		for i, r := range rep.Risk {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "%s **%s** · %d finding%s%s\n", severityIcon[r.Worst], r.Name, r.Findings, plural(r.Findings), strikes(r.Strikes))
		}
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Most flagged", Value: clip(b.String(), 1000)})
	}
	start := (page - 1) * PerPage
	var onPage []numbered
	if start < len(list) {
		onPage = list[start:min(start+PerPage, len(list))]
	}
	for _, it := range onPage {
		f := it.f
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{
			Name:  clip(fmt.Sprintf("%s #%d · %s · %s", severityIcon[f.Severity], it.n, titleCase(f.Severity), label(f)), 250),
			Value: clip(fmt.Sprintf("%s\n`P %.2g` · `Q %.2g` · %s", f.Summary, f.P, f.Q, gamesRange(f.Games)), 1000),
		})
	}
	if len(onPage) == 0 {
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Nothing of that kind", Value: "No finding of this kind stands out."})
	}
	foot := fmt.Sprintf("Page %d/%d · %d finding%s", page, pages, len(list), plural(len(list)))
	if kind != "" {
		foot += " · " + kindName[kind] + " only"
	}
	e.Footer = &discordgo.MessageEmbedFooter{Text: foot}

	comps := pagination.BuildPageRow(page, pages,
		func(target int) string { return fmt.Sprintf("%s%d|%s", PagePrefix, target, kind) },
		func(slot string) string { return OffPrefix + slot })
	if len(onPage) > 0 {
		opts := make([]discordgo.SelectMenuOption, 0, len(onPage))
		for _, it := range onPage {
			opts = append(opts, discordgo.SelectMenuOption{
				Label:       clip(fmt.Sprintf("#%d · %s · %s", it.n, titleCase(it.f.Severity), label(it.f)), 100),
				Description: clip(it.f.Summary, 100),
				Value:       strconv.Itoa(it.n),
			})
		}
		comps = append(comps, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, CustomID: OpenID, Placeholder: "Open a finding's evidence…", Options: opts},
		}})
	}
	kinds := []discordgo.SelectMenuOption{{Label: "All kinds", Value: "all", Default: kind == ""}}
	for _, k := range kindOrder {
		kinds = append(kinds, discordgo.SelectMenuOption{Label: kindName[k], Value: k, Default: kind == k})
	}
	comps = append(comps, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, CustomID: KindID, Placeholder: "Filter by kind", Options: kinds},
	}})
	return fit(e), comps
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Detail is one finding with its reasoning, the evidence, and what could explain it innocently. hits are what the players wrote.
func Detail(rep *Report, n int, f Finding, hits []Hit) *discordgo.MessageEmbed {
	e := &discordgo.MessageEmbed{
		Title: clip(fmt.Sprintf("%s %s #%d · %s", severityIcon[f.Severity], label(f), n, titleCase(f.Severity)), 250),
		Color: severityColor[f.Severity],
		Description: clip(fmt.Sprintf("> **%s**\n\n**Why it is flagged**\n%s\n\n**How sure**\nIf the games were fair, this would happen about **%.2g** of the time. After allowing for how many pairs and players were checked, roughly **%.1f%%** of findings this strong are expected to be chance.",
			f.Summary, f.Reasoning, f.P, f.Q*100), 3000),
	}
	var names []string
	for _, id := range f.Players {
		name := rep.Names[id]
		if name == "" {
			name = fmt.Sprintf("#%d", id)
		}
		names = append(names, "**"+name+"**")
	}
	e.Fields = append(e.Fields,
		&discordgo.MessageEmbedField{Name: "Players", Value: strings.Join(names, " · "), Inline: true},
		&discordgo.MessageEmbedField{Name: "Games", Value: gamesRange(f.Games), Inline: true},
	)
	for _, ev := range f.Evidence {
		if len(e.Fields) >= 12 {
			break
		}
		v := ev.Value
		if len(ev.Games) > 0 {
			v += "\n`" + gamesText(ev.Games, 12) + "`"
		}
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: clip("📌 "+ev.Label, 250), Value: clip(v, 1000)})
	}
	if len(hits) > 0 {
		var b strings.Builder
		for _, h := range hits {
			fmt.Fprintf(&b, "**#%d** %s · %s: “%s”\n", h.Game, h.Source, h.Who, clip(strings.ReplaceAll(h.Text, "\n", " "), 140))
		}
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "💬 What the players wrote (keyword matches)", Value: clip(b.String(), 1000)})
	} else {
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "💬 What the players wrote", Value: "Nothing in the saved game chats mentions throwing, boosting or trading wins. That proves nothing either way: players rarely say it."})
	}
	e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "⚖️ What could explain this innocently", Value: clip("• "+strings.Join(f.Caveats, "\n• "), 1000)})
	e.Footer = &discordgo.MessageEmbedFooter{Text: "Check the games' own logs on the dashboard before acting."}
	return fit(e)
}
