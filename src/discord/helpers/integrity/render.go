package integrity

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

var severityIcon = map[string]string{"critical": "🟥", "high": "🟧", "medium": "🟨", "low": "⬜"}
var kindName = map[string]string{"win_trading": "Win trading", "boosting": "Boosting", "throwing": "Throwing", "void_dodging": "Void dodging"}
var severityColor = map[string]int{"critical": 0xdc2626, "high": 0xf97316, "medium": 0xeab308, "low": 0x94a3b8}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func gamesText(games []int, max int) string {
	if len(games) == 0 {
		return ""
	}
	if len(games) > max {
		games = games[:max]
	}
	parts := make([]string, len(games))
	for i, g := range games {
		parts[i] = fmt.Sprintf("#%d", g)
	}
	return strings.Join(parts, ", ")
}

func label(f Finding) string {
	name := kindName[f.Kind]
	if name == "" {
		name = f.Kind
	}
	if f.Subtype != "" {
		name += " · " + strings.ReplaceAll(f.Subtype, "_", " ")
	}
	return name
}

// Overview is the season at a glance: who carries the most suspicion, and the findings in order of seriousness. kind narrows it to one kind.
func Overview(rep *Report, kind string) *discordgo.MessageEmbed {
	e := &discordgo.MessageEmbed{
		Title: "Ladder integrity",
		Color: 0x6366f1,
		Description: fmt.Sprintf("%d scored games read (%s to %s), %d voided, %d players.\nEvery line is a probability worked out from the results, with the games it rests on. **It is a reason to look, not a verdict.**",
			rep.GamesAnalysed, day(rep, true), day(rep, false), rep.GamesVoided, rep.Players),
		Footer: &discordgo.MessageEmbedFooter{Text: "/integrity finding <number> shows the evidence · /integrity player <user> shows one player"},
	}
	if len(rep.Findings) == 0 {
		e.Description += "\n\n✅ Nothing stands out. Nothing here is unlikely enough to be more than luck."
		return e
	}
	if len(rep.Risk) > 0 {
		var b strings.Builder
		for i, r := range rep.Risk {
			if i >= 6 {
				break
			}
			fmt.Fprintf(&b, "%s **%s** · %d finding(s)%s\n", severityIcon[r.Worst], r.Name, r.Findings, strikes(r.Strikes))
		}
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Most flagged players", Value: clip(b.String(), 1000)})
	}
	shown := 0
	for i, f := range rep.Findings {
		if kind != "" && f.Kind != kind {
			continue
		}
		if shown >= 8 {
			break
		}
		shown++
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{
			Name:  clip(fmt.Sprintf("%s #%d · %s · %s", severityIcon[f.Severity], i+1, strings.ToUpper(f.Severity), label(f)), 250),
			Value: clip(fmt.Sprintf("%s\nP=%.2g (Q=%.2g) · games %s", f.Summary, f.P, f.Q, gamesText(f.Games, 6)), 1000),
		})
	}
	if shown == 0 {
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Nothing of that kind", Value: "No finding of this kind stands out."})
	}
	return fit(e)
}

func day(rep *Report, from bool) string {
	t := rep.To
	if from {
		t = rep.From
	}
	if t.IsZero() {
		return "?"
	}
	return t.Format("Jan 2")
}

func strikes(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" · %d strike(s) already", n)
}

// Detail is one finding with its reasoning, every piece of evidence and what could explain it innocently. hits are what the players wrote.
func Detail(rep *Report, n int, f Finding, hits []Hit) *discordgo.MessageEmbed {
	e := &discordgo.MessageEmbed{
		Title:       clip(fmt.Sprintf("%s #%d · %s", label(f), n, strings.ToUpper(f.Severity)), 250),
		Color:       severityColor[f.Severity],
		Description: clip(fmt.Sprintf("**%s**\n\n%s\n\nProbability if the games were fair: **%.2g**. Allowing for how many pairs and players were checked, about **%.1f%%** of findings this strong are expected to be chance.", f.Summary, f.Reasoning, f.P, f.Q*100), 3000),
	}
	var names []string
	for _, id := range f.Players {
		name := rep.Names[id]
		if name == "" {
			name = fmt.Sprintf("#%d", id)
		}
		names = append(names, name)
	}
	e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Players", Value: strings.Join(names, " · ")})
	for _, ev := range f.Evidence {
		if len(e.Fields) >= 12 {
			break
		}
		v := ev.Value
		if len(ev.Games) > 0 {
			v += "\nGames: " + gamesText(ev.Games, 12)
		}
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: clip(ev.Label, 250), Value: clip(v, 1000)})
	}
	if len(hits) > 0 {
		var b strings.Builder
		for _, h := range hits {
			fmt.Fprintf(&b, "**#%d** %s · %s: “%s”\n", h.Game, h.Source, h.Who, clip(strings.ReplaceAll(h.Text, "\n", " "), 140))
		}
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "What the players wrote (keyword matches)", Value: clip(b.String(), 1000)})
	} else {
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "What the players wrote", Value: "Nothing in the saved game chats of these games mentions throwing, boosting or trading wins. That proves nothing either way: players rarely say it."})
	}
	e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "What could explain this innocently", Value: clip("• "+strings.Join(f.Caveats, "\n• "), 1000)})
	e.Footer = &discordgo.MessageEmbedFooter{Text: "Check the games' own logs on the dashboard before acting."}
	return fit(e)
}

// PlayerView is what the report says about one player: their standing and each finding in brief.
func PlayerView(rep *Report, who string, all *Report) *discordgo.MessageEmbed {
	e := &discordgo.MessageEmbed{Title: "Integrity · " + who, Color: 0x6366f1}
	if len(rep.Findings) == 0 {
		e.Description = fmt.Sprintf("✅ Nothing stands out about **%s** in the %d scored games read.", who, rep.GamesAnalysed)
		return e
	}
	e.Description = fmt.Sprintf("%d finding(s) about **%s** in the %d scored games read. Probabilities are what the results would give if the games were fair.", len(rep.Findings), who, rep.GamesAnalysed)
	for i, f := range rep.Findings {
		if i >= 6 {
			break
		}
		n := 0
		if all != nil {
			n = all.Number(f)
		}
		num := ""
		if n > 0 {
			num = fmt.Sprintf(" (finding #%d)", n)
		}
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{
			Name:  clip(fmt.Sprintf("%s %s · %s%s", severityIcon[f.Severity], strings.ToUpper(f.Severity), label(f), num), 250),
			Value: clip(fmt.Sprintf("%s\n%s\nP=%.2g (Q=%.2g) · games %s", f.Summary, firstSentence(f.Reasoning), f.P, f.Q, gamesText(f.Games, 6)), 1000),
		})
	}
	return fit(e)
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}

// Alert is the short form posted to the staff alerts channel when a game just scored adds to a serious finding.
func Alert(rep *Report, n int, f Finding) (string, []*discordgo.MessageEmbedField) {
	desc := fmt.Sprintf("%s **%s** · %s (finding #%d)\n%s\n\nRun `/integrity finding %d` for the evidence.", severityIcon[f.Severity], strings.ToUpper(f.Severity), label(f), n, f.Summary, n)
	var fields []*discordgo.MessageEmbedField
	for _, ev := range f.Evidence {
		if len(fields) >= 3 {
			break
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: clip(ev.Label, 250), Value: clip(ev.Value, 500)})
	}
	return desc, fields
}

// GameView lists the findings that rest on a game, under the report's own numbers so /integrity finding <n> opens the same one.
func GameView(rep *Report, number int) *discordgo.MessageEmbed {
	e := &discordgo.MessageEmbed{Title: fmt.Sprintf("Ladder integrity · game #%d", number), Color: 0x6366f1}
	for i, f := range rep.Findings {
		for _, g := range f.Games {
			if g == number {
				e.Fields = append(e.Fields, &discordgo.MessageEmbedField{
					Name:  clip(fmt.Sprintf("%s #%d · %s · %s", severityIcon[f.Severity], i+1, strings.ToUpper(f.Severity), label(f)), 250),
					Value: clip(fmt.Sprintf("%s\nP=%.2g (Q=%.2g)", f.Summary, f.P, f.Q), 1000),
				})
				break
			}
		}
	}
	if len(e.Fields) == 0 {
		e.Description = fmt.Sprintf("✅ Game #%d is not part of any finding.", number)
	} else {
		e.Description = "Findings that rest on this game. A game can be part of a finding without anything having gone wrong in it."
		e.Footer = &discordgo.MessageEmbedFooter{Text: "/integrity finding <number> shows the evidence"}
	}
	return fit(e)
}

// fit keeps an embed within the 6000 characters Discord allows in all: the longest field is cut back first, and when nothing is left to cut the
// last field goes.
func fit(e *discordgo.MessageEmbed) *discordgo.MessageEmbed {
	size := func() int {
		n := len([]rune(e.Title)) + len([]rune(e.Description))
		if e.Footer != nil {
			n += len([]rune(e.Footer.Text))
		}
		for _, f := range e.Fields {
			n += len([]rune(f.Name)) + len([]rune(f.Value))
		}
		return n
	}
	for size() > 5800 {
		longest, at := 0, -1
		for i, f := range e.Fields {
			if n := len([]rune(f.Value)); n > longest {
				longest, at = n, i
			}
		}
		if at >= 0 && longest > 150 {
			e.Fields[at].Value = clip(e.Fields[at].Value, longest*7/10)
			continue
		}
		if len(e.Fields) == 0 {
			break
		}
		e.Fields = e.Fields[:len(e.Fields)-1]
	}
	return e
}
