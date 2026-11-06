package proofread

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Member is a player of the game and the team they were put on. A player may be listed several times under other names they go by (their
// Minecraft name, their server nickname, their Discord name), all with the same ID: the first entry is the one a match is reported as.
type Member struct{ Name, Team, ID string }

// Result is what the chat of a screenshot says. Empty fields mean "could not tell", never "no".
type Result struct {
	RulesHit  int      // chat lines a profile rule recognised
	Seen      []Member // game players found in the chat, with the team the game put them on
	Winner    string   // the game's team that won, per the chat
	WinnerBy  string   // how that was worked out
	BedBreaks []Count  // bed breaks by player, most first
	MVP       Member
	MVPBy     string
	Weak      bool   // the winner was inferred from the kills, not stated by the chat: never enough to score a game without staff
	Mismatch  string // set when the chat's winner is not the team that submitted
	Held      string // set by the caller when auto scoring was not trusted, and why
}

// Count is a player's number of something.
type Count struct {
	Member
	N int
}

// HasEvidence reports whether the image showed any game chat at all (a recognised line or a player of the game).
func (r Result) HasEvidence() bool { return r.RulesHit > 0 || len(r.Seen) > 0 }

type event struct {
	kind   string // bed | kill | eliminated | win
	team   string // a team color as the chat writes it
	actor  *Member
	victim *Member
	final  bool
}

// Analyze reads chat lines through the profile's rules and checks them against the roster. `claimed` is the team that submitted.
// `colors` is the in-game color of each team ("Team A" -> "Blue"), when the game knows them: "Blue won the game!" then names a team by itself.
func Analyze(lines []string, profile Profile, roster []Member, claimed string, colors map[string]string) Result {
	type ruleTest struct {
		match   *compiled
		actions []Action
		final   map[int]*compiled
	}
	var rules []ruleTest
	finalDefault := (*compiled)(nil)
	if profile.FinalKill != nil {
		finalDefault = compile(*profile.FinalKill)
	}
	for _, r := range profile.Rules {
		if r.On != "" && r.On != "chat" {
			continue
		}
		m := compile(r.Match)
		if m == nil {
			continue
		}
		rt := ruleTest{match: m, final: map[int]*compiled{}}
		for i, a := range r.Actions {
			if a.Type != "track" {
				continue
			}
			rt.actions = append(rt.actions, a)
			if a.Final != nil {
				rt.final[len(rt.actions)-1] = compile(*a.Final)
			}
			_ = i
		}
		if len(rt.actions) > 0 {
			rules = append(rules, rt)
		}
	}

	teams := map[string]bool{}
	for _, m := range roster {
		teams[m.Team] = true
	}
	teamList := make([]string, 0, len(teams))
	for t := range teams {
		teamList = append(teamList, t)
	}
	sort.Strings(teamList)

	var res Result
	seen := map[string]Member{}
	// A name a rule picked out may be misread by a letter; a word met anywhere else must match a player exactly.
	findAs := func(name string, fuzzy bool) *Member {
		if m := resolve(name, roster, fuzzy); m != nil {
			seen[strings.ToLower(m.Name)] = *m
			return m
		}
		return nil
	}
	find := func(name string) *Member { return findAs(name, true) }

	var events []event
	var topList []*Member // the end-of-game "Top Final Kills and Kills" list, best first
	// apply runs a line through the profile's rules; the first rule that reads it adds its events.
	apply := func(line string) bool {
		for _, rt := range rules {
			groups, ok := rt.match.test(line)
			if !ok {
				continue
			}
			res.RulesHit++
			for n, a := range rt.actions {
				field := func(key string) string { return expand(a.Fields, key, groups) }
				ev := event{kind: a.Kind, team: canonColor(field("team"), profile.Aliases)}
				switch a.Kind {
				case "bed":
					ev.actor = find(field("player"))
				case "kill":
					ev.victim, ev.actor = find(field("victim")), find(field("killer"))
					final := rt.final[n]
					if final == nil {
						final = finalDefault
					}
					if final != nil {
						_, ev.final = final.test(line)
					}
				case "eliminated", "win":
				default:
					continue
				}
				events = append(events, ev)
			}
			return true
		}
		return false
	}
	vocab := ruleWords(profile)

	for _, raw := range lines {
		line := cleanLine(raw)
		if line == "" {
			continue
		}
		for _, name := range wordsOf(line) { // a player named anywhere in the line counts as seen
			findAs(name, false)
		}
		if m := topKillerLine.FindStringSubmatch(line); m != nil {
			if p := find(m[1]); p != nil {
				topList = append(topList, p)
			}
		}
		matched := false
		cands := variants(line, vocab, roster)
		for _, cand := range cands {
			if matched = apply(cand); matched {
				break
			}
		}
		if !matched && profile.InferKills {
			for _, cand := range cands {
				if ev, ok := inferKill(cand, profile, roster, finalDefault); ok {
					res.RulesHit++
					events = append(events, ev)
					seen[strings.ToLower(ev.victim.Name)], seen[strings.ToLower(ev.actor.Name)] = *ev.victim, *ev.actor
					break
				}
			}
		}
	}

	for _, m := range seen {
		res.Seen = append(res.Seen, m)
	}
	sort.Slice(res.Seen, func(i, j int) bool { return res.Seen[i].Name < res.Seen[j].Name })

	// Which game team each team color is, from who was on the receiving end of the chat's events.
	votes := map[string]map[string]int{}
	vote := func(color, team string) {
		if color == "" || team == "" {
			return
		}
		if votes[color] == nil {
			votes[color] = map[string]int{}
		}
		votes[color][team]++
	}
	other := func(team string) string {
		if len(teamList) == 2 {
			for _, t := range teamList {
				if t != team {
					return t
				}
			}
		}
		return ""
	}
	for i, ev := range events {
		switch ev.kind {
		case "bed": // the broken bed is not the breaker's own
			if ev.actor != nil {
				vote(ev.team, other(ev.actor.Team))
			}
		case "eliminated": // the final kills just before it were of that team's players
			voted := false
			for j := max(0, i-6); j < i; j++ {
				if p := events[j]; p.kind == "kill" && p.final && p.victim != nil {
					vote(ev.team, p.victim.Team)
					voted = true
				}
			}
			if !voted { // the reader may have missed the "FINAL KILL" words: the kill just before it is the one that did it
				if p := lastKill(events, i); p != nil && p.victim != nil {
					vote(ev.team, p.victim.Team)
				}
			}
		case "win": // the kill just before the win was made by a player of the team that won
			if p := lastKill(events, i); p != nil && p.actor != nil {
				vote(ev.team, p.actor.Team)
			}
		}
	}
	for team, color := range colors { // what the game says each color is outweighs anything guessed from the chat
		for range 100 {
			vote(canonColor(color, profile.Aliases), team)
		}
	}
	best := func(color string) string {
		top, topN := "", 0
		for t, n := range votes[color] {
			if n > topN || (n == topN && t < top) {
				top, topN = t, n
			}
		}
		return top
	}
	teamOf := func(color string) string {
		if t := best(color); t != "" {
			return t
		}
		if len(teamList) == 2 { // two teams: the color that is not a known one is the other team
			for c := range votes {
				if c != color && best(c) != "" {
					return other(best(c))
				}
			}
		}
		return ""
	}

	for _, ev := range events {
		if ev.kind == "win" {
			if t := teamOf(ev.team); t != "" {
				res.Winner, res.WinnerBy = t, "the win message in chat"
				break
			}
		}
	}
	if res.Winner == "" {
		for _, ev := range events {
			if ev.kind == "eliminated" {
				if t := teamOf(ev.team); t != "" && other(t) != "" {
					res.Winner, res.WinnerBy = other(t), fmt.Sprintf("%s was eliminated", t)
					break
				}
			}
		}
	}
	if res.Winner == "" { // no win or elimination line in view (cut off, or scrolled away): the final kills in view may still say who won
		res.Winner, res.WinnerBy, res.Weak = winnerFromFinalKills(events, roster, teamList)
	}
	if res.Winner != "" && claimed != "" && !strings.EqualFold(res.Winner, claimed) {
		res.Mismatch = fmt.Sprintf("the chat says %s won, but %s submitted", res.Winner, claimed)
	}

	kills, finals, beds := map[string]int{}, map[string]int{}, map[string]int{}
	members := map[string]Member{}
	for _, ev := range events {
		switch {
		case ev.kind == "bed" && ev.actor != nil:
			beds[ev.actor.Name]++
			members[ev.actor.Name] = *ev.actor
		case ev.kind == "kill" && ev.actor != nil:
			kills[ev.actor.Name]++
			members[ev.actor.Name] = *ev.actor
			if ev.final {
				finals[ev.actor.Name]++
			}
		}
	}
	for name, n := range beds {
		res.BedBreaks = append(res.BedBreaks, Count{members[name], n})
	}
	sort.Slice(res.BedBreaks, func(i, j int) bool {
		if a, b := res.BedBreaks[i], res.BedBreaks[j]; a.N != b.N {
			return a.N > b.N
		}
		return res.BedBreaks[i].Name < res.BedBreaks[j].Name
	})
	res.MVP, res.MVPBy = pickMVP(members, kills, finals, beds, res.Winner)
	if res.MVP.Name == "" { // no kills in view: the end banner ranks the players itself, best first
		for _, p := range topList {
			if res.Winner == "" || strings.EqualFold(p.Team, res.Winner) {
				res.MVP, res.MVPBy = *p, "first in the end-of-game ranking"
				break
			}
		}
	}
	return res
}

// inferKill reads a death line no rule knows as a kill, the way the engine's tracker does: a player of the game opens the line, and a player of
// another team is named somewhere in it ("AloneSky was drowned in the sea with the help of zorev"). Chat lines, which hold a skip token such
// as ":", are never read this way.
func inferKill(line string, profile Profile, roster []Member, final *compiled) (event, bool) {
	for _, skip := range profile.InferKillSkip {
		if skip != "" && strings.Contains(line, skip) {
			return event{}, false
		}
	}
	words := strings.Fields(line)
	if len(words) < 3 {
		return event{}, false
	}
	victim := resolve(words[0], roster, true)
	if victim == nil {
		return event{}, false
	}
	var killer *Member
	for _, w := range wordsOf(strings.Join(words[1:], " ")) {
		if m := resolve(w, roster, false); m != nil && m.Team != victim.Team {
			killer = m
			break
		}
	}
	if killer == nil {
		return event{}, false
	}
	ev := event{kind: "kill", victim: victim, actor: killer}
	if final != nil {
		_, ev.final = final.test(line)
	}
	return ev, true
}

// topKillerLine is a line of the end-of-game ranking, "#1 Killer [Diamond] zorev · 2 · 2": the player is the last name before the two counts,
// and the dots between them may be read as any punctuation.
var topKillerLine = regexp.MustCompile(`^#?\d\s.*?([A-Za-z0-9_]{3,16})\s*[^\w\s]+\s*\d+\s*[^\w\s]+\s*\d+\s*$`)

// pickMVP is the winning side's player with the most kills, then final kills, then bed breaks; no one when nothing separates them.
func pickMVP(members map[string]Member, kills, finals, beds map[string]int, winner string) (Member, string) {
	var pool []string
	for name, m := range members {
		if winner == "" || strings.EqualFold(m.Team, winner) {
			pool = append(pool, name)
		}
	}
	slices.Sort(pool)
	tiers := []struct {
		label string
		of    map[string]int
	}{{"most kills", kills}, {"most final kills", finals}, {"most bed breaks", beds}}
	for _, tier := range tiers {
		top, tied := 0, false
		var leader string
		for _, name := range pool {
			switch n := tier.of[name]; {
			case n > top:
				top, leader, tied = n, name, false
			case n == top && n > 0:
				tied = true
			}
		}
		if top == 0 {
			continue
		}
		if !tied {
			return members[leader], fmt.Sprintf("%s (%d)", tier.label, top)
		}
		// a tie in this tier is settled by the next one, among the tied players only
		var still []string
		for _, name := range pool {
			if tier.of[name] == top {
				still = append(still, name)
			}
		}
		pool = still
	}
	return Member{}, ""
}

// expand fills a field template such as "{team}" from the rule's groups; with no template the group of that name is used.
func expand(fields map[string]string, key string, groups map[string]string) string {
	tpl, ok := fields[key]
	if !ok {
		return groups[key]
	}
	out := tpl
	for name, value := range groups {
		out = strings.ReplaceAll(out, "{"+name+"}", value)
	}
	return strings.TrimSpace(out)
}

// canonColor writes a team color the same way however the chat spells it ("Red Team", "red", "Cyan" for "Aqua").
func canonColor(s string, aliases map[string][]string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(s, " team"), "team "))
	for key, list := range aliases {
		key = strings.ToLower(key)
		for _, a := range list {
			if strings.EqualFold(a, s) {
				return key
			}
		}
	}
	return s
}

// cleanLine drops what a screenshot adds around a chat line: the chat mod's [COPY] buttons and stray spacing.
func cleanLine(s string) string {
	s = strings.ReplaceAll(s, "[COPY]", "")
	return strings.Join(strings.Fields(s), " ")
}

// wordsOf lists the name-shaped words of a line.
func wordsOf(line string) []string {
	return strings.FieldsFunc(line, func(r rune) bool {
		return !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	})
}

// resolve finds the roster player a name from the chat is: exactly, or by a small misreading (the OCR may swap or drop a letter).
// A name two players could be taken for is no match.
func resolve(name string, roster []Member, fuzzy bool) *Member {
	name = strings.ToLower(strings.Trim(name, "_ "))
	if len(name) < 3 {
		return nil
	}
	var hit *Member
	bestDist, ambiguous := 99, false
	for i := range roster {
		cand := strings.ToLower(strings.Trim(roster[i].Name, "_ "))
		d := distance(name, cand)
		limit := 0
		switch {
		case !fuzzy:
		case len(cand) >= 10:
			limit = 2
		case len(cand) >= 4:
			limit = 1
		}
		if d > limit {
			continue
		}
		switch {
		case d < bestDist:
			hit, bestDist, ambiguous = &roster[i], d, false
		case d == bestDist:
			ambiguous = true
		}
	}
	if ambiguous {
		return nil
	}
	if hit != nil && hit.ID != "" { // one of the names a player goes by: report them by their first one
		for i := range roster {
			if roster[i].ID == hit.ID {
				return &roster[i]
			}
		}
	}
	return hit
}

// distance is the Levenshtein distance of two strings.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if abs(len(ra)-len(rb)) > 2 {
		return 99
	}
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

var nameTag = regexp.MustCompile(`^\s*\[[^\]]*\]\s*|\s*\[[^\]]*\]\s*$`)

// PlainName is a player's name without the tags a server nickname carries around it ("[155] kingyamx" -> "kingyamx"); chat only shows the name.
func PlainName(s string) string { return strings.TrimSpace(nameTag.ReplaceAllString(s, "")) }

// lastKill is the latest kill among the few events before events[i] that names both players, or nil.
func lastKill(events []event, i int) *event {
	for j := i - 1; j >= max(0, i-6); j-- {
		if p := &events[j]; p.kind == "kill" && p.actor != nil && p.victim != nil {
			return p
		}
	}
	return nil
}

// winnerFromFinalKills tells who won when the chat shows no win or elimination line. A team all of whose players were final-killed in view has
// lost, which is firm. Otherwise the team of whoever made the last final kill is taken to have won (the one that ends a game), which is a
// guess, and is reported as weak.
func winnerFromFinalKills(events []event, roster []Member, teams []string) (winner, by string, weak bool) {
	size := map[string]int{}
	seenPlayer := map[string]bool{}
	for _, m := range roster {
		key := m.Team + "|" + strings.ToLower(m.Name)
		if m.ID != "" {
			key = m.Team + "|" + m.ID
		}
		if !seenPlayer[key] {
			seenPlayer[key] = true
			size[m.Team]++
		}
	}
	finalled := map[string]map[string]bool{}
	var last *event
	for i := range events {
		ev := &events[i]
		if ev.kind != "kill" || !ev.final || ev.victim == nil || ev.actor == nil || ev.victim.Team == ev.actor.Team {
			continue
		}
		if finalled[ev.victim.Team] == nil {
			finalled[ev.victim.Team] = map[string]bool{}
		}
		finalled[ev.victim.Team][ev.victim.Name] = true
		last = ev
	}
	if len(teams) == 2 {
		for _, t := range teams {
			if size[t] > 0 && len(finalled[t]) >= size[t] {
				for _, o := range teams {
					if o != t {
						return o, fmt.Sprintf("every player of %s was final-killed in the chat", t), false
					}
				}
			}
		}
	}
	if last != nil {
		return last.actor.Team, fmt.Sprintf("inferred: the last final kill in the chat was made by %s", last.actor.Name), true
	}
	return "", "", false
}
