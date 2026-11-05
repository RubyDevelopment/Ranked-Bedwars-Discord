// Package proofread reads a =submit screenshot: it pulls the game chat out of the image (OCR), runs the lines through the server profile's own
// chat rules (the ones the engine tracks a game with: bed, kill, eliminated, win), and checks what they say against the game's roster.
// The result is advice for staff, never a score: a screenshot can be misread, so nothing here decides a game.
package proofread

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Matcher is a profile text matcher: {"type": "regex" | "includes" | "startsWith" | "endsWith" | "equals", "value": "..."}.
type Matcher struct {
	Type       string `json:"type"`
	Value      string `json:"value"`
	IgnoreCase *bool  `json:"ignoreCase,omitempty"`
}

// Action is one thing a rule does; only "track" matters here.
type Action struct {
	Type   string            `json:"type"`
	Kind   string            `json:"kind"` // bed | kill | eliminated | win
	Fields map[string]string `json:"fields"`
	Final  *Matcher          `json:"final,omitempty"` // a kill that also matches this is a final kill
}

// Rule is a profile rule reading a chat line.
type Rule struct {
	ID      string   `json:"id"`
	On      string   `json:"on"`
	Match   Matcher  `json:"match"`
	Actions []Action `json:"actions"`
}

// Profile is the part of a server profile that tells what the chat means.
type Profile struct {
	Rules     []Rule
	Aliases   map[string][]string // team color synonyms (aqua = cyan)
	FinalKill *Matcher
}

// LoadProfile reads the `server` game config ({"profile": {"rules": [...], "tracking": {...}}}). Missing parts are empty.
func LoadProfile(raw json.RawMessage) Profile {
	var server struct {
		Profile struct {
			Rules    []Rule `json:"rules"`
			Tracking struct {
				TeamAliases map[string][]string `json:"teamAliases"`
				FinalKill   *Matcher            `json:"finalKill"`
			} `json:"tracking"`
		} `json:"profile"`
	}
	if json.Unmarshal(raw, &server) != nil {
		return Profile{}
	}
	return Profile{Rules: server.Profile.Rules, Aliases: server.Profile.Tracking.TeamAliases, FinalKill: server.Profile.Tracking.FinalKill}
}

// compiled is a matcher ready to test lines; groups come only from a regex.
type compiled struct {
	test func(line string) (map[string]string, bool)
}

// compile turns a matcher into a test. A regex Go cannot run (a lookahead, a backreference) gives nil: that rule is skipped.
func compile(m Matcher) *compiled {
	ci := m.Type == "regex"
	if m.IgnoreCase != nil {
		ci = *m.IgnoreCase
	}
	fold := func(s string) string {
		if ci {
			return strings.ToLower(s)
		}
		return s
	}
	want := fold(m.Value)
	switch m.Type {
	case "equals":
		return &compiled{func(l string) (map[string]string, bool) { return nil, fold(l) == want }}
	case "startsWith":
		return &compiled{func(l string) (map[string]string, bool) { return nil, strings.HasPrefix(fold(l), want) }}
	case "endsWith":
		return &compiled{func(l string) (map[string]string, bool) { return nil, strings.HasSuffix(fold(l), want) }}
	case "includes":
		return &compiled{func(l string) (map[string]string, bool) { return nil, strings.Contains(fold(l), want) }}
	case "regex":
		src := m.Value
		if ci {
			src = "(?i)" + src
		}
		re, err := regexp.Compile(src)
		if err != nil {
			return nil
		}
		return &compiled{func(l string) (map[string]string, bool) {
			sub := re.FindStringSubmatch(l)
			if sub == nil {
				return nil, false
			}
			groups := map[string]string{}
			for i, name := range re.SubexpNames() {
				if name != "" {
					groups[name] = sub[i]
				}
			}
			return groups, true
		}}
	}
	return nil
}
