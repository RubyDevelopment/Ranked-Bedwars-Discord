package helpers

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// assignPrefixOptions is how "=score B eularie", "=score eularie B" and "=score 27575 team b" all mean the same thing: the words of
// a prefix command are matched to the slash command's options by what each word looks like, not by where it stands.
//
//  1. Words that name themselves: a mention (<@user>, <#channel>, <@&role>), a duration (3d), true/false, a word or phrase that is one
//     of an option's choices ("B" or "Team B", "Links Only"), a game number.
//  2. A bare Discord ID goes to the first user, channel or role option still empty; a ladder level's name goes to `level`.
//  3. Numbers go to the number options, in the order typed.
//  4. A player's Minecraft name goes to the user option: the word that is a registered player's name, else the first plausible one.
//  5. What is left is free text: each text option takes one word, in the order declared, and the last (or a reason) takes the rest.
//
// An option no word fits stays empty, as it would on a slash command.
func assignPrefixOptions(guildID string, leaf []*discordgo.ApplicationCommandOption, args []string) []*discordgo.ApplicationCommandInteractionDataOption {
	out, _ := assignPrefixWords(guildID, leaf, args)
	return out
}

// assignPrefixWords is assignPrefixOptions that also returns the words no option took.
func assignPrefixWords(guildID string, leaf []*discordgo.ApplicationCommandOption, args []string) ([]*discordgo.ApplicationCommandInteractionDataOption, []string) {
	if len(leaf) == 0 || len(args) == 0 {
		return nil, nil
	}
	a := &prefixAssignment{guildID: guildID, words: args, used: make([]bool, len(args)), filled: map[string]*discordgo.ApplicationCommandInteractionDataOption{}}

	// 0. Options named outright: "style=neon", "type:late", or a switch followed by on/off ("stats off").
	a.takeNamed(leaf)

	// 1. Mentions first (they cannot be mistaken), then choices, then the rest that name themselves.
	for _, opt := range leaf {
		switch opt.Type {
		case discordgo.ApplicationCommandOptionUser:
			a.takeWord(opt, func(w string) bool {
				return strings.HasPrefix(w, "<@") && !strings.HasPrefix(w, "<@&") && strings.HasSuffix(w, ">")
			})
		case discordgo.ApplicationCommandOptionChannel:
			a.takeWord(opt, func(w string) bool { return strings.HasPrefix(w, "<#") && strings.HasSuffix(w, ">") })
		case discordgo.ApplicationCommandOptionRole:
			a.takeWord(opt, func(w string) bool { return strings.HasPrefix(w, "<@&") && strings.HasSuffix(w, ">") })
		}
	}
	for _, opt := range leaf {
		if len(opt.Choices) > 0 && (opt.Type == discordgo.ApplicationCommandOptionString || opt.Type == discordgo.ApplicationCommandOptionInteger || opt.Type == discordgo.ApplicationCommandOptionNumber) {
			a.takeChoice(opt)
		}
	}
	for _, opt := range leaf {
		switch {
		case opt.Type == discordgo.ApplicationCommandOptionBoolean:
			a.takeWord(opt, func(w string) bool { return prefixBoolWords[strings.ToLower(w)] != 0 })
		case opt.Type == discordgo.ApplicationCommandOptionString && strings.EqualFold(opt.Name, "duration"):
			a.takeWord(opt, prefixDurationPattern.MatchString)
		case opt.Type == discordgo.ApplicationCommandOptionString && (strings.Contains(strings.ToLower(opt.Name), "url") || strings.Contains(strings.ToLower(opt.Name), "link")):
			a.takeWord(opt, func(w string) bool {
				return strings.HasPrefix(strings.ToLower(w), "http://") || strings.HasPrefix(strings.ToLower(w), "https://")
			})
		case opt.Type == discordgo.ApplicationCommandOptionInteger && len(opt.Choices) == 0 && strings.HasSuffix(strings.ToLower(opt.Name), "game"):
			a.takeWord(opt, prefixGameNumberPattern.MatchString)
		}
	}

	// 2. A bare Discord ID (a user before a channel before a role), and a ladder level written out.
	for _, kind := range []discordgo.ApplicationCommandOptionType{discordgo.ApplicationCommandOptionUser, discordgo.ApplicationCommandOptionChannel, discordgo.ApplicationCommandOptionRole} {
		for _, opt := range leaf {
			if opt.Type == kind {
				a.takeWord(opt, prefixSnowflakePattern.MatchString)
			}
		}
	}
	for _, opt := range leaf {
		if isPrefixLevelOption(opt) && a.filled[opt.Name] == nil {
			if rest, at := a.unused(); len(rest) > 0 {
				if start, end, key := FindPrefixLevel(guildID, rest); end > start {
					for k := start; k < end; k++ {
						a.used[at[k]] = true
					}
					a.filled[opt.Name] = prefixOptionValue(opt, key)
				}
			}
		}
	}

	// 3. Numbers, in the order typed.
	for _, opt := range leaf {
		if (opt.Type == discordgo.ApplicationCommandOptionInteger || opt.Type == discordgo.ApplicationCommandOptionNumber) && len(opt.Choices) == 0 {
			integer := opt.Type == discordgo.ApplicationCommandOptionInteger
			a.takeWord(opt, func(w string) bool {
				if integer {
					_, err := strconv.ParseInt(w, 10, 64)
					return err == nil
				}
				_, err := strconv.ParseFloat(w, 64)
				return err == nil
			})
		}
	}

	// 4. A player named by their Minecraft name.
	for _, opt := range leaf {
		if opt.Type == discordgo.ApplicationCommandOptionUser && a.filled[opt.Name] == nil {
			a.takePlayerName(opt, hasFreeText(leaf))
		}
	}

	// 5. Free text.
	a.fillText(leaf)

	out := make([]*discordgo.ApplicationCommandInteractionDataOption, 0, len(a.filled))
	for _, opt := range leaf {
		if v, ok := a.filled[opt.Name]; ok && prefixValueFits(opt, v.Value) {
			out = append(out, v)
		}
	}
	leftover, _ := a.unused()
	return out, leftover
}

var prefixBoolWords = map[string]int{"true": 1, "yes": 1, "on": 1, "enable": 1, "enabled": 1, "false": -1, "no": -1, "off": -1, "disable": -1, "disabled": -1}

var plainMCName = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)

func isPrefixLevelOption(opt *discordgo.ApplicationCommandOption) bool {
	return opt.Type == discordgo.ApplicationCommandOptionString && opt.Name == "level" && opt.Autocomplete
}

// isPrefixTextOption is an option that takes whatever words are left.
func isPrefixTextOption(opt *discordgo.ApplicationCommandOption) bool {
	return opt.Type == discordgo.ApplicationCommandOptionString && len(opt.Choices) == 0 && !isPrefixLevelOption(opt) && !strings.EqualFold(opt.Name, "duration")
}

func isPrefixGreedy(opt *discordgo.ApplicationCommandOption) bool {
	switch strings.ToLower(opt.Name) {
	case "reason", "content", "message", "description", "note", "nickname", "text":
		return true
	}
	return false
}

func hasFreeText(leaf []*discordgo.ApplicationCommandOption) bool {
	for _, o := range leaf {
		if isPrefixTextOption(o) {
			return true
		}
	}
	return false
}

type prefixAssignment struct {
	guildID string
	words   []string
	used    []bool
	filled  map[string]*discordgo.ApplicationCommandInteractionDataOption
}

// unused lists the words nobody has taken, with where each one stands.
func (a *prefixAssignment) unused() ([]string, []int) {
	var words []string
	var at []int
	for i, w := range a.words {
		if !a.used[i] {
			words, at = append(words, w), append(at, i)
		}
	}
	return words, at
}

// takeWord gives opt the first unused word that fits.
func (a *prefixAssignment) takeWord(opt *discordgo.ApplicationCommandOption, fits func(string) bool) {
	if a.filled[opt.Name] != nil {
		return
	}
	for i, w := range a.words {
		if !a.used[i] && fits(w) {
			a.used[i] = true
			a.filled[opt.Name] = prefixOptionValue(opt, w)
			return
		}
	}
}

// normalizeChoiceWord makes "Best Win Streak", "best_win_streak" and "bestwinstreak" the same.
func normalizeChoiceWord(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// takeChoice finds the words, up to three in a row, that are one of opt's choices: its name or value, the last word of its name
// when that is unique ("B" for "Team B"), or the start of exactly one name.
func (a *prefixAssignment) takeChoice(opt *discordgo.ApplicationCommandOption) {
	if a.filled[opt.Name] != nil {
		return
	}
	match := func(phrase string) (any, bool) {
		want := normalizeChoiceWord(phrase)
		if want == "" {
			return nil, false
		}
		for _, c := range opt.Choices {
			if normalizeChoiceWord(c.Name) == want || normalizeChoiceWord(choiceText(c.Value)) == want {
				return c.Value, true
			}
		}
		lastWord := map[string][]any{}
		for _, c := range opt.Choices {
			if f := strings.Fields(c.Name); len(f) > 1 {
				k := normalizeChoiceWord(f[len(f)-1])
				lastWord[k] = append(lastWord[k], c.Value)
			}
		}
		if v := lastWord[want]; len(v) == 1 {
			return v[0], true
		}
		if len(want) >= 3 {
			var hit []any
			for _, c := range opt.Choices {
				if strings.HasPrefix(normalizeChoiceWord(c.Name), want) {
					hit = append(hit, c.Value)
				}
			}
			if len(hit) == 1 {
				return hit[0], true
			}
		}
		return nil, false
	}
	for size := 3; size >= 1; size-- {
		for i := 0; i+size <= len(a.words); i++ {
			free := true
			for k := i; k < i+size; k++ {
				free = free && !a.used[k]
			}
			if !free {
				continue
			}
			if value, ok := match(strings.Join(a.words[i:i+size], " ")); ok {
				for k := i; k < i+size; k++ {
					a.used[k] = true
				}
				a.filled[opt.Name] = &discordgo.ApplicationCommandInteractionDataOption{Name: opt.Name, Type: opt.Type, Value: choiceOptionValue(opt, value)}
				return
			}
		}
	}
}

func choiceText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

// choiceOptionValue is a choice's value the way Discord sends it: numbers as float64.
func choiceOptionValue(opt *discordgo.ApplicationCommandOption, v any) any {
	if opt.Type == discordgo.ApplicationCommandOptionInteger || opt.Type == discordgo.ApplicationCommandOptionNumber {
		switch x := v.(type) {
		case int:
			return float64(x)
		case int64:
			return float64(x)
		case string:
			if f, err := strconv.ParseFloat(x, 64); err == nil {
				return f
			}
		}
	}
	return v
}

// takePlayerName gives a user option the word that names a registered player, else (when the user is needed, or nothing else
// takes free text) the first word that could be a Minecraft name.
func (a *prefixAssignment) takePlayerName(opt *discordgo.ApplicationCommandOption, textFollows bool) {
	words, at := a.unused()
	for k, w := range words {
		if plainMCName.MatchString(w) && userByPlayerName(a.guildID, w) != nil {
			a.used[at[k]] = true
			a.filled[opt.Name] = prefixOptionValue(opt, w)
			return
		}
	}
	if !opt.Required && textFollows {
		return
	}
	for k, w := range words {
		if _, err := strconv.ParseFloat(w, 64); err == nil || prefixDurationPattern.MatchString(w) || !plainMCName.MatchString(w) {
			continue
		}
		a.used[at[k]] = true
		a.filled[opt.Name] = prefixOptionValue(opt, w)
		return
	}
}

// fillText hands the remaining words to the text options: one word each in the order declared, and the last text option (or one that
// is a reason or message) takes everything that is left, keeping a word back for each later option that is required.
func (a *prefixAssignment) fillText(leaf []*discordgo.ApplicationCommandOption) {
	var text []*discordgo.ApplicationCommandOption
	for _, o := range leaf {
		if isPrefixTextOption(o) && a.filled[o.Name] == nil {
			text = append(text, o)
		}
	}
	words, at := a.unused()
	pos := 0
	for idx, opt := range text {
		if pos >= len(words) {
			return
		}
		later := 0
		for _, o := range text[idx+1:] {
			if o.Required {
				later++
			}
		}
		take := 1
		if isPrefixGreedy(opt) || idx == len(text)-1 {
			take = len(words) - pos - later
		} else if !opt.Required && len(words)-pos <= later {
			continue
		}
		take = max(take, 1)
		take = min(take, len(words)-pos)
		for k := pos; k < pos+take; k++ {
			a.used[at[k]] = true
		}
		a.filled[opt.Name] = prefixOptionValue(opt, strings.Join(words[pos:pos+take], " "))
		pos += take
	}
}

// takeNamed fills the options written with their name: "name=value", "name:value", or a yes/no option's name and then on/off.
func (a *prefixAssignment) takeNamed(leaf []*discordgo.ApplicationCommandOption) {
	for i, w := range a.words {
		if a.used[i] {
			continue
		}
		for _, opt := range leaf {
			if a.filled[opt.Name] != nil {
				continue
			}
			lower := strings.ToLower(w)
			name := strings.ToLower(opt.Name)
			switch {
			case strings.HasPrefix(lower, name+"=") || strings.HasPrefix(lower, name+":"):
				if value := strings.TrimSpace(w[len(name)+1:]); value != "" {
					a.used[i] = true
					a.filled[opt.Name] = a.optionFromText(opt, value)
				}
			case opt.Type == discordgo.ApplicationCommandOptionBoolean && lower == name && i+1 < len(a.words) && !a.used[i+1] && prefixBoolWords[strings.ToLower(a.words[i+1])] != 0:
				a.used[i], a.used[i+1] = true, true
				a.filled[opt.Name] = prefixOptionValue(opt, a.words[i+1])
			}
			if a.used[i] {
				break
			}
		}
	}
}

// optionFromText is the value of an option written as text: a choice by its name, anything else as typed.
func (a *prefixAssignment) optionFromText(opt *discordgo.ApplicationCommandOption, text string) *discordgo.ApplicationCommandInteractionDataOption {
	for _, c := range opt.Choices {
		if normalizeChoiceWord(c.Name) == normalizeChoiceWord(text) || normalizeChoiceWord(choiceText(c.Value)) == normalizeChoiceWord(text) {
			return &discordgo.ApplicationCommandInteractionDataOption{Name: opt.Name, Type: opt.Type, Value: choiceOptionValue(opt, c.Value)}
		}
	}
	return prefixOptionValue(opt, text)
}

// prefixValueFits says whether a typed value is of the kind the option's type needs. The slash helpers (IntValue, BoolValue...)
// panic on anything else, so a word that is not a number never reaches a number option: the option stays empty instead.
func prefixValueFits(opt *discordgo.ApplicationCommandOption, v any) bool {
	switch opt.Type {
	case discordgo.ApplicationCommandOptionInteger, discordgo.ApplicationCommandOptionNumber:
		_, ok := v.(float64)
		return ok
	case discordgo.ApplicationCommandOptionBoolean:
		_, ok := v.(bool)
		return ok
	default:
		_, ok := v.(string)
		return ok
	}
}

// FindPrefixLevel finds the punishment level a prefix command's words name (start and end word, and the level key); the
// punishment code sets it when it loads.
var FindPrefixLevel = func(guildID string, tokens []string) (start, end int, key string) { return 0, 0, "" }
