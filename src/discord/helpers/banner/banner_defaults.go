package banner

import (
	"fmt"
)

// The banners every server gets before it designs its own in the panel's Banner Builder: the stats card, the leaderboard and the
// match-results card, drawn with the same layers the builder uses. A server that saves a banner of its own for a command replaces
// the default of that command. All three are 1400 wide (1400 x 800 for stats and the leaderboard, and for results unless a game has
// so many players that the rows need more room).

const (
	bnWidth  = 1400
	bnHeight = 800

	bnBackdrop = "#171232"
	bnDeep     = "#33166e"
	bnCard     = "#2a2058d9" // translucent, so the glows of the background show through
	bnCardEnd  = "#1c1642d9"
	bnRow      = "#ffffff0d"
	bnBorder   = "#4c3a8c"
	bnFrame    = "#8b5cf6"
	bnAccent   = "#b072fb"
	bnMain     = "#f1ebff"
	bnMuted    = "#9a8dc4"
	bnWin      = "#4ade80"
	bnLoss     = "#f87171"
	bnGold     = "#fbbf24"
	bnSilver   = "#d1d5db"
	bnBronze   = "#cd7f32"
)

type bnOpt func(*Layer)

func bnBold(l *Layer)          { l.Bold = true }
func bnRight(l *Layer)         { l.Align = "right" }
func bnCenter(l *Layer)        { l.Align = "center" }
func bnRadius(r float64) bnOpt { return func(l *Layer) { l.Radius = r } }
func bnStroke(c string) bnOpt  { return func(l *Layer) { l.Stroke, l.StrokeWidth = c, 2 } }

// bnFont picks one of the preset fonts (Rajdhani gives the cards their sharp, game-like numbers).
func bnFont(name string) bnOpt { return func(l *Layer) { l.Font = name } }

// bnSpace spreads the letters of a label.
func bnSpace(px float64) bnOpt { return func(l *Layer) { l.LetterSpacing = px } }

// bnGrad fills a block with a gradient from its colour to c2.
func bnGrad(c2 string, angle float64) bnOpt { return func(l *Layer) { l.Color2, l.Angle = c2, angle } }

func bnRule(v, op, val string, set RuleSet) bnOpt {
	return func(l *Layer) { l.Rules = append(l.Rules, Rule{Var: v, Op: op, Value: val, Set: set}) }
}
func colorPtr(c string) *string { return &c }
func truePtr() *bool            { t := true; return &t }

func bnTxt(id string, x, y, w, h float64, text string, size float64, col string, opts ...bnOpt) *Layer {
	l := &Layer{ID: id, Kind: "text", X: x, Y: y, W: w, H: h, Text: text, Size: size, Color: col, Font: "rajdhani", VAlign: "middle"}
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

// bnBase is the purple canvas and frame every default card sits on, with the glows that give it depth.
func bnBase(id, command string, w, h int, glows []Glow, layers ...*Layer) *Banner {
	all := []*Layer{bnBox("frame", 14, 14, float64(w-28), float64(h-28), "#00000000", bnRadius(24), bnStroke(bnFrame))}
	return &Banner{
		ID: id, Name: "Default", Command: command, Enabled: true, Width: w, Height: h,
		Background: Background{Color: bnBackdrop, Color2: bnDeep, Angle: 135, Glows: glows},
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
	const (
		left   = 468.0 // where the right-hand side begins
		tileW  = 203.0
		tileH  = 136.0
		gap    = 24.0
		rowGap = 16.0
	)
	layers := []*Layer{
		// The player, on the left.
		bnBox("side", 48, 48, 380, 704, bnCard, bnGrad(bnCardEnd, 160), bnRadius(26), bnStroke(bnBorder)),
		bnPic("body", 78, 74, 320, 520, "{player.body}", "contain"),
		bnBox("rank-pill", 88, 612, 300, 60, "#8b5cf62e", bnRadius(30), bnStroke(bnFrame)),
		bnTxt("rank", 88, 612, 300, 60, "{stats.rank}", 36, bnMain, bnBold, bnCenter, bnSpace(1)),
		bnTxt("position", 78, 686, 320, 44, "#{stats.position} on the leaderboard", 24, bnMuted, bnCenter),

		// The header.
		bnTxt("name", left, 50, 540, 100, "{player.name}", 76, bnMain, bnBold),
		bnTxt("season", left, 146, 540, 40, "{season.name}  ·  {stats.games_played} games played", 28, bnMuted),
		bnTxt("elo-label", 1040, 52, 312, 32, "ELO", 26, bnMuted, bnBold, bnRight, bnSpace(5)),
		bnTxt("elo", 960, 78, 392, 104, "{stats.elo}", 96, bnMain, bnBold, bnRight),
		bnTxt("peak", 960, 178, 392, 34, "PEAK {stats.highest_elo}", 26, bnAccent, bnBold, bnRight, bnSpace(2)),
		bnBox("rule", left, 226, 884, 2, bnBorder),
	}
	type tile struct{ label, value, color string }
	tiles := []tile{
		{"WINS", "{stats.wins}", bnWin}, {"LOSSES", "{stats.losses}", bnLoss}, {"W/L", "{stats.wlr}", bnMain}, {"WIN RATE", "{stats.win_rate}", bnMain},
		{"KILLS", "{stats.kills}", bnMain}, {"FINAL KILLS", "{stats.final_kills}", bnMain}, {"K/D", "{stats.kdr}", bnMain}, {"FKDR", "{stats.fkdr}", bnMain},
		{"BEDS BROKEN", "{stats.beds_destroyed}", bnMain}, {"BEDS LOST", "{stats.beds_lost}", bnMain}, {"MVPS", "{stats.mvp_count}", bnMain}, {"WIN STREAK", "{stats.win_streak}", bnMain},
	}
	for i, t := range tiles {
		x, y := left+float64(i%4)*(tileW+gap), 250+float64(i/4)*(tileH+rowGap)
		layers = append(layers,
			bnBox(fmt.Sprintf("tile%d", i), x, y, tileW, tileH, bnCard, bnGrad(bnCardEnd, 160), bnRadius(18), bnStroke(bnBorder)),
			bnBox(fmt.Sprintf("tile%d-bar", i), x+18, y+22, 4, 24, bnFrame, bnRadius(2)),
			bnTxt(fmt.Sprintf("tile%d-label", i), x+32, y+16, tileW-44, 36, t.label, 22, bnMuted, bnBold, bnSpace(2)),
			bnTxt(fmt.Sprintf("tile%d-value", i), x+20, y+56, tileW-36, 66, t.value, 56, t.color, bnBold),
		)
	}
	layers = append(layers,
		bnTxt("footer-left", left, 716, 440, 36, "Best win streak {stats.highest_win_streak}", 24, bnMuted),
		bnTxt("footer-right", left+444, 716, 440, 36, "Last played {stats.last_played}", 24, bnMuted, bnRight),
	)
	glows := []Glow{
		{X: 238, Y: 330, Radius: 300, Color: "#a855f7", Opacity: 0.5},
		{X: 1240, Y: 90, Radius: 460, Color: "#7c3aed", Opacity: 0.28},
		{X: 700, Y: 800, Radius: 380, Color: "#6d28d9", Opacity: 0.18},
	}
	return bnBase("default-stats", "stats", bnWidth, bnHeight, glows, layers...)
}

func defaultLeaderboardBanner() *Banner {
	const (
		rows   = 10
		rowY   = 142.0
		rowH   = 50.0
		pitch  = 55.0
		left   = 56.0
		inner  = 1288.0 // the width of the table: 1400 less the margins
		tableR = left + inner
	)
	layers := []*Layer{
		bnTxt("title", left, 36, 900, 72, "{lb.title}", 54, bnMain, bnBold),
		bnBox("page-pill", tableR-210, 48, 210, 48, "#8b5cf62e", bnRadius(24), bnStroke(bnFrame)),
		bnTxt("page", tableR-210, 48, 210, 48, "PAGE {page.number} / {page.total}", 22, bnMain, bnBold, bnCenter, bnSpace(2)),
		bnTxt("h-pos", left+8, 108, 70, 28, "#", 20, bnMuted, bnBold, bnCenter, bnSpace(2)),
		bnTxt("h-name", left+118, 108, 340, 28, "PLAYER", 20, bnMuted, bnBold, bnSpace(2)),
		bnTxt("h-rank", 640, 108, 170, 28, "RANK", 20, bnMuted, bnBold, bnCenter, bnSpace(2)),
		bnTxt("h-elo", 820, 108, 110, 28, "{lb.stat_short}", 20, bnMuted, bnBold, bnRight, bnSpace(2)),
		bnTxt("h-peak", 944, 108, 110, 28, "PEAK", 20, bnMuted, bnBold, bnRight, bnSpace(2)),
		bnTxt("h-wl", 1070, 108, 110, 28, "W / L", 20, bnMuted, bnBold, bnRight, bnSpace(2)),
		bnTxt("h-wlr", 1196, 108, 124, 28, "W/L RATIO", 20, bnMuted, bnBold, bnRight, bnSpace(2)),
	}
	for n := 1; n <= rows; n++ {
		y := rowY + float64(n-1)*pitch
		p := func(f string) string { return fmt.Sprintf("{lb.%d.%s}", n, f) }
		pos := fmt.Sprintf("lb.%d.position", n)
		hide := bnRule(fmt.Sprintf("lb.%d.name", n), "eq", "", RuleSet{Hidden: truePtr()})
		// The top three positions (on any page) get a medal colour.
		gold := bnRule(pos, "eq", "1", RuleSet{Color: colorPtr(bnGold)})
		silver := bnRule(pos, "eq", "2", RuleSet{Color: colorPtr(bnSilver)})
		bronze := bnRule(pos, "eq", "3", RuleSet{Color: colorPtr(bnBronze)})
		goldEdge := bnRule(pos, "eq", "1", RuleSet{Stroke: colorPtr(bnGold)})
		silverEdge := bnRule(pos, "eq", "2", RuleSet{Stroke: colorPtr(bnSilver)})
		bronzeEdge := bnRule(pos, "eq", "3", RuleSet{Stroke: colorPtr(bnBronze)})
		layers = append(layers,
			bnBox(fmt.Sprintf("row%d", n), left, y, inner, rowH, bnCard, bnGrad(bnCardEnd, 90), bnRadius(14), bnStroke(bnBorder), hide, goldEdge, silverEdge, bronzeEdge),
			bnTxt(fmt.Sprintf("pos%d", n), left+8, y, 70, rowH, "#"+p("position"), 28, bnAccent, bnBold, bnCenter, hide, gold, silver, bronze),
			bnPic(fmt.Sprintf("head%d", n), left+88, y+5, 40, 40, p("head"), "cover", bnRadius(10), hide),
			bnTxt(fmt.Sprintf("name%d", n), left+146, y, 400, rowH, p("name"), 30, bnMain, bnBold, hide),
			bnTxt(fmt.Sprintf("rank%d", n), 640, y, 170, rowH, p("rank"), 24, bnAccent, bnBold, bnCenter, hide),
			bnTxt(fmt.Sprintf("elo%d", n), 820, y, 110, rowH, p("value"), 30, bnMain, bnBold, bnRight, hide),
			bnTxt(fmt.Sprintf("peak%d", n), 944, y, 110, rowH, p("peak"), 24, bnMuted, bnBold, bnRight, hide),
			bnTxt(fmt.Sprintf("wl%d", n), 1070, y, 110, rowH, p("wins")+" / "+p("losses"), 24, bnMuted, bnBold, bnRight, hide),
			bnTxt(fmt.Sprintf("wlr%d", n), 1196, y, 124, rowH, p("wlr"), 26, bnMain, bnBold, bnRight, hide),
		)
	}
	hideMe := bnRule("me.name", "eq", "", RuleSet{Hidden: truePtr()})
	layers = append(layers,
		bnBox("me-card", left, 712, inner, 56, "#8b5cf633", bnGrad("#8b5cf60d", 90), bnRadius(16), bnStroke(bnFrame), hideMe),
		bnTxt("me-label", left+20, 712, 110, 56, "YOU", 22, bnAccent, bnBold, bnSpace(3), hideMe),
		bnTxt("me-pos", left+120, 712, 90, 56, "#{me.position}", 30, bnAccent, bnBold, hideMe),
		bnTxt("me-name", left+222, 712, 420, 56, "{me.name}", 30, bnMain, bnBold, hideMe),
		bnTxt("me-rank", 640, 712, 170, 56, "{me.rank}", 24, bnAccent, bnBold, bnCenter, hideMe),
		bnTxt("me-elo", 820, 712, 110, 56, "{me.value}", 30, bnMain, bnBold, bnRight, hideMe),
		bnTxt("me-wl", 1070, 712, 110, 56, "{me.wins} / {me.losses}", 24, bnMuted, bnBold, bnRight, hideMe),
		bnTxt("me-wlr", 1196, 712, 124, 56, "{me.wlr}", 26, bnMain, bnBold, bnRight, hideMe),
	)
	glows := []Glow{
		{X: 1250, Y: 60, Radius: 520, Color: "#7c3aed", Opacity: 0.3},
		{X: 120, Y: 800, Radius: 420, Color: "#a855f7", Opacity: 0.2},
	}
	return bnBase("default-leaderboard", "leaderboard", bnWidth, bnHeight, glows, layers...)
}

// ResultsTeam is one team of a finished match, as the results card shows it.
type ResultsTeam struct {
	Name    string
	Win     bool
	Label   string // replaces VICTORY/DEFEAT when the game has no winner (a casual game)
	Players []ResultsPlayer
}

// ResultsPlayer is one player's line on the results card.
type ResultsPlayer struct {
	Name, Head, RankBefore, RankAfter string
	Before, After, Change             int
	MVP                               bool
	Unrated                           bool // a void or casual game: Before and After are the current ELO and there is no change
}

// ResultsVars are the {match.*} and {tN.pM.*} values of the results card.
func ResultsVars(number int, season, mode, mapName string, teams []ResultsTeam) Vars {
	v := Vars{"match.number": fmt.Sprint(number), "match.season": season, "match.mode": mode, "match.map": mapName}
	for t, team := range teams {
		pre := fmt.Sprintf("t%d.", t+1)
		v[pre+"name"] = team.Name
		v[pre+"result"] = map[bool]string{true: "VICTORY", false: "DEFEAT"}[team.Win]
		if team.Label != "" {
			v[pre+"result"] = team.Label
		}
		for p, pl := range team.Players {
			q := fmt.Sprintf("%sp%d.", pre, p+1)
			v[q+"name"], v[q+"head"] = pl.Name, pl.Head
			v[q+"rank"], v[q+"rank_before"], v[q+"rank_after"] = pl.RankAfter, pl.RankBefore, pl.RankAfter
			v[q+"before"], v[q+"after"], v[q+"change"] = fmt.Sprint(pl.Before), fmt.Sprint(pl.After), fmt.Sprintf("%+d", pl.Change)
			if pl.Unrated {
				v[q+"change"] = "" // nothing was scored: no change to draw
			}
			v[q+"mvp"] = "" // set for every player, so an unset one draws nothing instead of its placeholder
			if pl.MVP {
				v[q+"mvp"] = "MVP"
			}
		}
	}
	return v
}

// DefaultResultsBannerFor is DefaultResultsBanner, with the rank and ELO columns left out when the game has no rating (showElo false).
// The card is 1400 x 800: the team panels share that height, and a game with so many players that its rows would get too small
// makes the card taller instead.
func DefaultResultsBannerFor(teams []ResultsTeam, showElo bool) *Banner {
	perRow := 2
	if len(teams) == 1 {
		perRow = 1
	}
	maxPlayers := 1
	for _, t := range teams {
		maxPlayers = max(maxPlayers, len(t.Players))
	}
	const (
		pad = 48.0
		gap = 24.0

		headH   = 76.0 // a panel's title strip
		foot    = 14.0
		maxRowH = 84.0
		minRowH = 38.0
	)
	top := 164.0 // where the panels may begin
	panelW := (bnWidth - pad*2 - gap*float64(perRow-1)) / float64(perRow)
	panelRows := (len(teams) + perRow - 1) / perRow

	// Fit the panels into the card's 800: rows shrink (down to minRowH) before the card grows, and panels with few players
	// hug their rows and sit in the middle of the space instead of stretching.
	h := float64(bnHeight)
	avail := h - top - pad
	panelH := (avail - gap*float64(panelRows-1)) / float64(panelRows)
	rowH := min(maxRowH, (panelH-headH-foot)/float64(maxPlayers))
	if rowH < minRowH {
		rowH = minRowH
		h = top + float64(panelRows)*(headH+foot+float64(maxPlayers)*rowH) + gap*float64(panelRows-1) + pad
		avail = h - top - pad
	}
	panelH = headH + foot + float64(maxPlayers)*rowH
	top += max(0, (avail-(float64(panelRows)*panelH+gap*float64(panelRows-1)))/2)

	layers := []*Layer{
		bnTxt("match", pad, 34, 760, 84, "MATCH #{match.number}", 66, bnMain, bnBold),
		bnTxt("season", bnWidth-pad-560, 38, 560, 44, "{match.season}", 32, bnAccent, bnBold, bnRight, bnSpace(2)),
		bnTxt("detail", bnWidth-pad-560, 80, 560, 40, "{match.mode}  ·  {match.map}", 26, bnMuted, bnRight),
		bnBox("rule", pad, 130, bnWidth-pad*2, 3, bnFrame, bnGrad("#8b5cf600", 90)),
	}
	for t := range teams {
		pre := fmt.Sprintf("t%d.", t+1)
		x := pad + float64(t%perRow)*(panelW+gap)
		y := top + float64(t/perRow)*(panelH+gap)
		// A winning team's panel has a green edge and a losing team's a red one; its result text takes the same colour.
		wonEdge := bnRule(pre+"result", "eq", "VICTORY", RuleSet{Stroke: colorPtr(bnWin)})
		lostEdge := bnRule(pre+"result", "eq", "DEFEAT", RuleSet{Stroke: colorPtr(bnLoss)})
		won := bnRule(pre+"result", "eq", "VICTORY", RuleSet{Color: colorPtr(bnWin)})
		lost := bnRule(pre+"result", "eq", "DEFEAT", RuleSet{Color: colorPtr(bnLoss)})
		layers = append(layers,
			bnBox(fmt.Sprintf("panel%d", t), x, y, panelW, panelH, bnCard, bnGrad(bnCardEnd, 160), bnRadius(22), bnStroke(bnBorder), wonEdge, lostEdge),
			bnTxt(fmt.Sprintf("team%d", t), x+28, y+10, panelW-300, headH-14, "{"+pre+"name}", 42, bnMain, bnBold),
			bnTxt(fmt.Sprintf("result%d", t), x+panelW-300, y+10, 272, headH-14, "{"+pre+"result}", 38, bnMuted, bnBold, bnRight, bnSpace(3), won, lost),
		)
		for p := range teams[t].Players {
			q := fmt.Sprintf("%sp%d.", pre, p+1)
			ry := y + headH + float64(p)*rowH
			inner := rowH - 8
			head := min(inner-10, 60)
			up := bnRule(q+"change", "gte", "0", RuleSet{Color: colorPtr(bnWin)})
			down := bnRule(q+"change", "lt", "0", RuleSet{Color: colorPtr(bnLoss)})
			mvpEdge := bnRule(q+"mvp", "eq", "MVP", RuleSet{Stroke: colorPtr(bnGold)})
			layers = append(layers,
				bnBox(fmt.Sprintf("row%d_%d", t, p), x+14, ry, panelW-28, inner, bnRow, bnRadius(14), bnStroke("#ffffff00"), mvpEdge),
				bnPic(fmt.Sprintf("head%d_%d", t, p), x+26, ry+(inner-head)/2, head, head, "{"+q+"head}", "cover", bnRadius(10)),
				bnTxt(fmt.Sprintf("name%d_%d", t, p), x+26+head+14, ry, 232-(head-48), inner, "{"+q+"name}", 30, bnMain, bnBold),
			)
			if showElo {
				layers = append(layers,
					bnTxt(fmt.Sprintf("mvp%d_%d", t, p), x+panelW-330, ry, 56, inner, "{"+q+"mvp}", 22, bnGold, bnBold, bnCenter, bnSpace(1)),
					bnTxt(fmt.Sprintf("rank%d_%d", t, p), x+panelW-270, ry, 100, inner, "{"+q+"rank}", 24, bnAccent, bnBold, bnCenter),
					bnTxt(fmt.Sprintf("elo%d_%d", t, p), x+panelW-166, ry, 76, inner, "{"+q+"after}", 30, bnMain, bnBold, bnRight),
					bnTxt(fmt.Sprintf("chg%d_%d", t, p), x+panelW-86, ry, 62, inner, "{"+q+"change}", 26, bnMuted, bnBold, bnRight, up, down),
				)
			} else {
				layers = append(layers, bnTxt(fmt.Sprintf("mvp%d_%d", t, p), x+panelW-110, ry, 84, inner, "{"+q+"mvp}", 24, bnGold, bnBold, bnRight, bnSpace(1)))
			}
		}
	}
	glows := []Glow{
		{X: 260, Y: 40, Radius: 520, Color: "#7c3aed", Opacity: 0.3},
		{X: float64(bnWidth) - 200, Y: h, Radius: 520, Color: "#a855f7", Opacity: 0.22},
	}
	return bnBase("default-results", "results", bnWidth, int(h), glows, layers...)
}
