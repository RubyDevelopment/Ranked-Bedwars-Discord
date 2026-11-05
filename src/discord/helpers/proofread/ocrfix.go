package proofread

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	groupName  = regexp.MustCompile(`\(\?P?<\w+>`)
	escapeSeq  = regexp.MustCompile(`\\.`)
	wordTokens = regexp.MustCompile(`[A-Za-z]{3,}`)
)

// ruleWords are the words the profile's chat rules are written with ("has been killed by", "eliminated", "FINAL KILL"): what the text reader
// is expected to have read, so a word it misread by a letter can be put right.
func ruleWords(p Profile) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		v = escapeSeq.ReplaceAllString(groupName.ReplaceAllString(v, " "), " ")
		for _, w := range wordTokens.FindAllString(v, -1) {
			if k := strings.ToLower(w); !seen[k] {
				seen[k] = true
				out = append(out, w)
			}
		}
	}
	for _, r := range p.Rules {
		if r.On == "" || r.On == "chat" {
			add(r.Match.Value)
			for _, a := range r.Actions {
				if a.Final != nil {
					add(a.Final.Value)
				}
			}
		}
	}
	if p.FinalKill != nil {
		add(p.FinalKill.Value)
	}
	return out
}

// variants are the ways a line may be read: as it came, with the profile's words put right where the reader missed a letter, and without the
// scraps of letters it often adds at the front. A rule is tried against each, so one misread word does not hide a whole line.
func variants(line string, vocab []string, roster []Member) []string {
	out := []string{line}
	add := func(s string) {
		for _, o := range out {
			if o == s {
				return
			}
		}
		out = append(out, s)
	}
	fixed := snapWords(line, vocab, roster)
	add(fixed)
	for _, s := range []string{line, fixed} {
		words := strings.Fields(s)
		for drop := 0; drop < 2 && len(words) > 3 && len([]rune(strings.Trim(words[0], ".,:;!?"))) < 3; drop++ {
			words = words[1:]
			add(strings.Join(words, " "))
		}
	}
	return out
}

// snapWords replaces each word that is one letter away from a word of the profile's rules (two for long words) by that word, in the same case
// style. Names of the game's players are left alone.
func snapWords(line string, vocab []string, roster []Member) string {
	words := strings.Fields(line)
	for i, w := range words {
		core := strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if len(core) < 3 {
			continue
		}
		if resolve(core, roster, true) != nil {
			continue // a player's name, maybe misread: not for this to change
		}
		lc := strings.ToLower(core)
		best, bestD, tie := "", 99, false
		for _, v := range vocab {
			d := distance(lc, strings.ToLower(v))
			limit := 1
			if len(v) >= 7 {
				limit = 2
			}
			if d > limit || d == 0 {
				if d == 0 {
					best, bestD, tie = "", 0, false
					break
				}
				continue
			}
			switch {
			case d < bestD:
				best, bestD, tie = v, d, false
			case d == bestD && !strings.EqualFold(v, best):
				tie = true
			}
		}
		if best == "" || tie {
			continue
		}
		words[i] = strings.Replace(w, core, matchCase(best, core), 1)
	}
	return strings.Join(words, " ")
}

// matchCase writes `word` in the case style of `like`: all capitals, a capital first, or as it is.
func matchCase(word, like string) string {
	switch {
	case like == strings.ToUpper(like) && strings.ToLower(like) != like:
		return strings.ToUpper(word)
	case unicode.IsUpper([]rune(like)[0]):
		return strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
	}
	return strings.ToLower(word)
}
