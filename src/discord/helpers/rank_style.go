package helpers

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"ranked-bedwars/src/discord/helpers/banner"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/types"
)

// Rank colours for banners. Every banner variable that holds a rank name (stats.rank, lb.3.rank, me.rank, t1.p2.rank, rank_before...)
// also gets <name>_color, <name>_bg and <name>_border: the rank's colour from Rank thresholds, toned the way the PRBW cards tone it
// (readable text, a soft fill, a soft outline), ready for a badge: style="color: {rank_color}; background: {rank_bg}; border: 1px solid {rank_border}".

var rankCache = base.NewTTLCache[[]types.Rank](base.ConfigCacheTTL)

const unrankedColor = "#9ca3af"

func hexRGB(hex string) (r, g, b float64, ok bool) {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return 0, 0, 0, false
	}
	n, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return float64(n >> 16 & 255), float64(n >> 8 & 255), float64(n & 255), true
}

// tonedRank turns a rank's colour into badge colours: saturation capped at 70% and lightness kept between 55% and 75%.
func tonedRank(hex string) (text, bg, border string) {
	r, g, b, ok := hexRGB(hex)
	if !ok {
		r, g, b, _ = hexRGB(unrankedColor)
	}
	r, g, b = r/255, g/255, b/255
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l := (mx + mn) / 2
	var h, s float64
	if mx != mn {
		d := mx - mn
		if l > 0.5 {
			s = d / (2 - mx - mn)
		} else {
			s = d / (mx + mn)
		}
		switch mx {
		case r:
			h = (g-b)/d + map[bool]float64{true: 6, false: 0}[g < b]
		case g:
			h = (b-r)/d + 2
		default:
			h = (r-g)/d + 4
		}
		h /= 6
	}
	s = math.Min(s, 0.7)
	l = math.Max(0.55, math.Min(l, 0.75))
	// back to RGB
	q := l * (1 + s)
	if l >= 0.5 {
		q = l + s - l*s
	}
	p := 2*l - q
	ch := func(t float64) float64 {
		if t < 0 {
			t++
		}
		if t > 1 {
			t--
		}
		switch {
		case t < 1.0/6:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return p + (q-p)*(2.0/3-t)*6
		}
		return p
	}
	R, G, B := math.Round(ch(h+1.0/3)*255), math.Round(ch(h)*255), math.Round(ch(h-1.0/3)*255)
	return fmt.Sprintf("#%02x%02x%02x", int(R), int(G), int(B)),
		fmt.Sprintf("rgba(%d, %d, %d, 0.12)", int(R), int(G), int(B)),
		fmt.Sprintf("rgba(%d, %d, %d, 0.35)", int(R), int(G), int(B))
}

// withRankStyles returns vars plus the colour variables of every rank variable in it. A rank the server does not list gets a neutral grey.
func withRankStyles(guildID string, vars banner.Vars) banner.Vars {
	if guildID == "" {
		return vars // a banner drawn outside a server (a preview) has no ranks to look up
	}
	var colors map[string]string
	extra := banner.Vars{}
	for key := range vars {
		base := ""
		for _, suffix := range []string{"rank_before", "rank_after", "rank"} {
			if key == suffix || strings.HasSuffix(key, "."+suffix) {
				base = key
				break
			}
		}
		if base == "" {
			continue
		}
		if colors == nil {
			colors = map[string]string{}
			if ranks, err := rankCache.Get(guildID, func() ([]types.Rank, error) { return GuildMetaRanks(guildID) }); err == nil {
				for _, r := range ranks {
					colors[strings.ToLower(r.RankName)] = r.Color
				}
			}
		}
		text, bg, border := tonedRank(colors[strings.ToLower(vars[base])])
		if vars[base+"_color"] == "" {
			extra[base+"_color"], extra[base+"_bg"], extra[base+"_border"] = text, bg, border
		}
	}
	for k, v := range extra {
		vars[k] = v
	}
	return vars
}

func init() { banner.ExtraVars = withRankStyles }
