package helpers

import (
	"fmt"
)

// The banners every server gets before it designs its own in the panel's Banner Builder: the old bot's purple
// stats card, leaderboard and match-results cards, drawn with the same layers the builder uses. A server that
// saves a banner of its own for a command replaces the default of that command.

const (
	bnBackdrop = "#0d0b1a"
	bnDeep     = "#1e0440"
	bnCard     = "#151030"
	bnBorder   = "#2d1f55"
	bnFrame    = "#8b5cf6"
	bnAccent   = "#a855f7"
	bnMain     = "#e2d9f3"
	bnMuted    = "#8b7fb0"
	bnWin      = "#4ade80"
	bnLoss     = "#f87171"
)

type bnOpt func(*Layer)

func bnBold(l *Layer)          { l.Bold = true }
func bnRight(l *Layer)         { l.Align = "right" }
func bnCenter(l *Layer)        { l.Align = "center" }
func bnRadius(r float64) bnOpt { return func(l *Layer) { l.Radius = r } }
func bnStroke(c string) bnOpt  { return func(l *Layer) { l.Stroke, l.StrokeWidth = c, 2 } }
func bnRule(v, op, val string, set RuleSet) bnOpt {
	return func(l *Layer) { l.Rules = append(l.Rules, Rule{Var: v, Op: op, Value: val, Set: set}) }
}
func colorPtr(c string) *string { return &c }
func truePtr() *bool            { t := true; return &t }

func bnTxt(id string, x, y, w, h float64, text string, size float64, col string, opts ...bnOpt) *Layer {
	l := &Layer{ID: id, Kind: "text", X: x, Y: y, W: w, H: h, Text: text, Size: size, Color: col, Font: "sans", VAlign: "middle"}
	for _, o := range opts {
		o(l)
	}
	return l
}

func bnBox(id string, x, y, w, h float64, fill string, opts ...bnOpt) *Layer {
	l := &Layer{ID: id, Kind: "shape", X: x, Y: y, W: w, H: h, Color: fill, Shape: "rect"}
	for _, o := range opts {
		o(l)
	}
	return l
}

func bnPic(id string, x, y, w, h float64, src, fit string, opts ...bnOpt) *Layer {
	l := &Layer{ID: id, Kind: "image", X: x, Y: y, W: w, H: h, Src: src, Fit: fit}
	for _, o := range opts {
		o(l)
	}
	return l
}

// base is the purple canvas and frame every default card sits on.
func bnBase(id, command string, w, h int, layers ...*Layer) *Banner {
	all := []*Layer{bnBox("frame", 12, 12, float64(w-24), float64(h-24), "#00000000", bnRadius(14), bnStroke(bnFrame))}
	return &Banner{
		ID: id, Name: "Default", Command: command, Enabled: true, Width: w, Height: h,
		Background: Background{Color: bnBackdrop, Color2: bnDeep, Angle: 135},
		Layers:     append(all, layers...),
	}
}

// DefaultBanner is the built-in banner of a command ("stats", "leaderboard"), or nil for a command without one.
func DefaultBanner(command string) *Banner {
	switch command {
	case "stats":
		return defaultStatsBanner()
	case "leaderboard":
		return defaultLeaderboardBanner()
	}
	return nil
}

func defaultStatsBanner() *Banner {
	layers := []*Layer{
		bnPic("body", 40, 36, 190, 428, "{player.body}", "contain"),
		bnTxt("name", 262, 36, 640, 70, "{player.name}", 54, bnMain, bnBold),
		bnTxt("rank", 262, 104, 640, 40, "{stats.rank}  ·  #{stats.position}", 28, bnAccent, bnBold),
		bnTxt("elo", 880, 36, 280, 70, "{stats.elo}", 58, bnMain, bnBold, bnRight),
		bnTxt("elo-label", 880, 100, 280, 34, "ELO  (peak {stats.highest_elo})", 22, bnMuted, bnRight),
	}
	tiles := [][2]string{
		{"WINS", "{stats.wins}"}, {"LOSSES", "{stats.losses}"}, {"W/L", "{stats.wlr}"}, {"WIN STREAK", "{stats.win_streak}"},
		{"KILLS", "{stats.kills}"}, {"FINAL KILLS", "{stats.final_kills}"}, {"BEDS BROKEN", "{stats.beds_destroyed}"}, {"MVPS", "{stats.mvp_count}"},
	}
	for i, t := range tiles {
		x, y := 262+float64(i%4)*226, 170+float64(i/4)*130
		layers = append(layers,
			bnBox(fmt.Sprintf("tile%d", i), x, y, 210, 112, bnCard, bnRadius(10), bnStroke(bnBorder)),
			bnTxt(fmt.Sprintf("tile%d-label", i), x+16, y+12, 178, 30, t[0], 20, bnMuted, bnBold),
			bnTxt(fmt.Sprintf("tile%d-value", i), x+16, y+44, 178, 56, t[1], 40, bnMain, bnBold),
		)
	}
	layers = append(layers,
		bnTxt("footer-left", 262, 438, 440, 34, "{season.name}  ·  {stats.games_played} games  ·  K/D {stats.kdr}", 22, bnMuted),
		bnTxt("footer-bnRight", 712, 438, 448, 34, "Last played {stats.last_played}", 22, bnMuted, bnRight),
	)
	return bnBase("default-stats", "stats", 1200, 500, layers...)
}

func defaultLeaderboardBanner() *Banner {
	const rows = 10
	layers := []*Layer{
		bnTxt("title", 40, 28, 920, 64, "{lb.title}", 44, bnMain, bnBold),
		bnTxt("page", 640, 40, 320, 40, "Page {page.number}/{page.total}", 24, bnMuted, bnRight),
		bnTxt("h-name", 150, 96, 360, 28, "PLAYER", 18, bnMuted, bnBold),
		bnTxt("h-rank", 520, 96, 160, 28, "RANK", 18, bnMuted, bnBold, bnCenter),
		bnTxt("h-elo", 690, 96, 120, 28, "ELO", 18, bnMuted, bnBold, bnRight),
		bnTxt("h-wl", 830, 96, 130, 28, "W / L", 18, bnMuted, bnBold, bnRight),
	}
	for n := 1; n <= rows; n++ {
		y := 130 + float64(n-1)*62
		hide := bnRule(fmt.Sprintf("lb.%d.name", n), "eq", "", RuleSet{Hidden: truePtr()})
		p := func(f string) string { return fmt.Sprintf("{lb.%d.%s}", n, f) }
		layers = append(layers,
			bnBox(fmt.Sprintf("row%d", n), 40, y, 920, 54, bnCard, bnRadius(8), bnStroke(bnBorder), hide),
			bnTxt(fmt.Sprintf("pos%d", n), 52, y, 70, 54, "#"+p("position"), 26, bnAccent, bnBold, hide),
			bnPic(fmt.Sprintf("head%d", n), 118, y+7, 40, 40, p("head"), "cover", hide),
			bnTxt(fmt.Sprintf("name%d", n), 168, y, 340, 54, p("name"), 26, bnMain, bnBold, hide),
			bnTxt(fmt.Sprintf("rank%d", n), 520, y, 160, 54, p("rank"), 22, bnAccent, bnCenter, hide),
			bnTxt(fmt.Sprintf("elo%d", n), 690, y, 120, 54, p("elo"), 26, bnMain, bnBold, bnRight, hide),
			bnTxt(fmt.Sprintf("wl%d", n), 830, y, 118, 54, p("wins")+" / "+p("losses"), 22, bnMuted, bnRight, hide),
		)
	}
	layers = append(layers, bnTxt("me", 40, 764, 920, 36, "You: #{me.position}  {me.name}  ·  {me.elo} ELO  ·  {me.rank}", 22, bnMuted,
		bnRule("me.name", "eq", "", RuleSet{Hidden: truePtr()})))
	return bnBase("default-leaderboard", "leaderboard", 1000, 820, layers...)
}

// ResultsTeam is one team of a finished match, as the results card shows it.
type ResultsTeam struct {
	Name    string
	Win     bool
	Players []ResultsPlayer
}

// ResultsPlayer is one player's line on the results card.
type ResultsPlayer struct {
	Name, Head, RankBefore, RankAfter string
	Before, After, Change             int
	MVP                               bool
}

// ResultsVars are the {match.*} and {tN.pM.*} values of the results card.
func ResultsVars(number int, season, mode, mapName string, teams []ResultsTeam) Vars {
	v := Vars{"match.number": fmt.Sprint(number), "match.season": season, "match.mode": mode, "match.map": mapName}
	for t, team := range teams {
		pre := fmt.Sprintf("t%d.", t+1)
		v[pre+"name"] = team.Name
		v[pre+"result"] = map[bool]string{true: "VICTORY", false: "DEFEAT"}[team.Win]
		for p, pl := range team.Players {
			q := fmt.Sprintf("%sp%d.", pre, p+1)
			v[q+"name"], v[q+"head"] = pl.Name, pl.Head
			v[q+"rank"], v[q+"rank_before"], v[q+"rank_after"] = pl.RankAfter, pl.RankBefore, pl.RankAfter
			v[q+"before"], v[q+"after"], v[q+"change"] = fmt.Sprint(pl.Before), fmt.Sprint(pl.After), fmt.Sprintf("%+d", pl.Change)
			v[q+"mvp"] = "" // set for every player, so an unset one draws nothing instead of its placeholder
			if pl.MVP {
				v[q+"mvp"] = "MVP"
			}
		}
	}
	return v
}

// DefaultResultsBanner is the match results card for a game with these teams: a header, then one panel per team
// (two across) listing its players with their rank and ELO change. It is built to fit the game, so a 2v2 and an
// 8v8 both read well. A server's own "results" banner from the Banner Builder takes its place.
func DefaultResultsBanner(teams []ResultsTeam) *Banner {
	perRow := 2
	if len(teams) == 1 {
		perRow = 1
	}
	maxPlayers := 1
	for _, t := range teams {
		maxPlayers = max(maxPlayers, len(t.Players))
	}
	const (
		pad    = 40.0
		rowH   = 56.0
		headH  = 96.0
		panelW = 780.0
	)
	panelH := 70 + float64(maxPlayers)*rowH + 16
	panelRows := (len(teams) + perRow - 1) / perRow
	w := int(pad*2 + float64(perRow)*panelW + float64(perRow-1)*24)
	h := int(160 + float64(panelRows)*(panelH+24))

	layers := []*Layer{
		bnTxt("match", pad, 28, 700, 70, "MATCH #{match.number}", 52, bnMain, bnBold),
		bnTxt("season", float64(w)-pad-600, 28, 600, 40, "{match.season}", 28, bnAccent, bnBold, bnRight),
		bnTxt("detail", float64(w)-pad-600, 70, 600, 36, "{match.mode}  ·  {match.map}", 22, bnMuted, bnRight),
		bnBox("rule", pad, headH+20, float64(w)-pad*2, 2, bnFrame),
	}
	for t := range teams {
		pre := fmt.Sprintf("t%d.", t+1)
		x := pad + float64(t%perRow)*(panelW+24)
		y := 160 + float64(t/perRow)*(panelH+24)
		won := bnRule(pre+"result", "eq", "VICTORY", RuleSet{Color: colorPtr(bnWin)})
		lost := bnRule(pre+"result", "eq", "DEFEAT", RuleSet{Color: colorPtr(bnLoss)})
		layers = append(layers,
			bnBox(fmt.Sprintf("panel%d", t), x, y, panelW, panelH, bnCard, bnRadius(12), bnStroke(bnBorder)),
			bnTxt(fmt.Sprintf("team%d", t), x+24, y+10, 440, 50, "{"+pre+"name}", 32, bnMain, bnBold),
			bnTxt(fmt.Sprintf("result%d", t), x+panelW-264, y+10, 240, 50, "{"+pre+"result}", 30, bnMuted, bnBold, bnRight, won, lost),
		)
		for p := range teams[t].Players {
			q := fmt.Sprintf("%sp%d.", pre, p+1)
			ry := y + 70 + float64(p)*rowH
			up := bnRule(q+"change", "gte", "0", RuleSet{Color: colorPtr(bnWin)})
			down := bnRule(q+"change", "lt", "0", RuleSet{Color: colorPtr(bnLoss)})
			layers = append(layers,
				bnPic(fmt.Sprintf("head%d_%d", t, p), x+24, ry+6, 44, 44, "{"+q+"head}", "cover"),
				bnTxt(fmt.Sprintf("name%d_%d", t, p), x+80, ry, 250, rowH, "{"+q+"name}", 26, bnMain, bnBold),
				bnTxt(fmt.Sprintf("mvp%d_%d", t, p), x+300, ry, 100, rowH, "{"+q+"mvp}", 18, "#facc15", bnBold),
				bnTxt(fmt.Sprintf("rank%d_%d", t, p), x+400, ry, 150, rowH, "{"+q+"rank}", 22, bnAccent, bnCenter),
				bnTxt(fmt.Sprintf("elo%d_%d", t, p), x+556, ry, 120, rowH, "{"+q+"after}", 24, bnMain, bnBold, bnRight),
				bnTxt(fmt.Sprintf("chg%d_%d", t, p), x+680, ry, 80, rowH, "{"+q+"change}", 24, bnMuted, bnBold, bnRight, up, down),
			)
		}
	}
	b := bnBase("default-results", "results", w, h, layers...)
	return b
}
