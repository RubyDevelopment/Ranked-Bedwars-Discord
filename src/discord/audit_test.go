package discord

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

var nameRe = regexp.MustCompile(`^[\p{Ll}\p{N}_-]{1,32}$`)


// TestAuditCommands checks every registered command against Discord's rules and the bot's own conventions.
func TestAuditCommands(t *testing.T) {
	var hard, soft []string
	bad := func(format string, a ...any) { hard = append(hard, fmt.Sprintf(format, a...)) }
	note := func(format string, a ...any) { soft = append(soft, fmt.Sprintf(format, a...)) }

	names := map[string]string{}
	for _, c := range helpers.CommandRegistry {
		if c.Execute == nil {
			bad("%s: no Execute", c.Name)
		}
		if !nameRe.MatchString(c.Name) {
			bad("%s: name is not a valid Discord command name", c.Name)
		}
		if c.Description == "" || len(c.Description) > 100 {
			bad("%s: description length %d (1-100 required)", c.Name, len(c.Description))
		}
		for _, n := range append([]string{c.Name}, append(c.SlashAliases, c.PrefixAliases...)...) {
			if other, ok := names[strings.ToLower(n)]; ok && other != c.Name {
				bad("%s: name or alias %q is also used by %s", c.Name, n, other)
			}
			names[strings.ToLower(n)] = c.Name
		}
		if c.Data == nil {
			bad("%s: no Data", c.Name)
			continue
		}
		if len(c.Data.Options) > 25 {
			bad("%s: %d options (25 max)", c.Name, len(c.Data.Options))
		}
		hasAuto := false
		var check func(opts []*discordgo.ApplicationCommandOption, path string)
		check = func(opts []*discordgo.ApplicationCommandOption, path string) {
			sawOptional := false
			if len(opts) > 25 {
				bad("%s: %d options at %s (25 max)", c.Name, len(opts), path)
			}
			for _, o := range opts {
				p := path + "/" + o.Name
				if !nameRe.MatchString(o.Name) {
					bad("%s: option name %q is invalid", c.Name, p)
				}
				if o.Description == "" || len(o.Description) > 100 {
					bad("%s: option %s description length %d", c.Name, p, len(o.Description))
				}
				isSub := o.Type == discordgo.ApplicationCommandOptionSubCommand || o.Type == discordgo.ApplicationCommandOptionSubCommandGroup
				if !isSub {
					if o.Required && sawOptional {
						bad("%s: required option %s comes after an optional one", c.Name, p)
					}
					if !o.Required {
						sawOptional = true
					}
				}
				if o.Autocomplete {
					hasAuto = true
					if len(o.Choices) > 0 {
						bad("%s: option %s has both choices and autocomplete", c.Name, p)
					}
				}
				if len(o.Choices) > 25 {
					bad("%s: option %s has %d choices (25 max)", c.Name, p, len(o.Choices))
				}
				check(o.Options, p)
			}
		}
		check(c.Data.Options, "")
		if hasAuto && c.Autocomplete == nil {
			bad("%s: has an autocomplete option but no Autocomplete handler", c.Name)
		}
		if c.Autocomplete != nil && !hasAuto {
			note("%s: has an Autocomplete handler but no autocomplete option", c.Name)
		}

		// requirements and role gates
		hasStaff := types.HasRequirementKey(c.Requirements, types.ReqRole, types.StaffRoleKey)
		needsReg := types.RequiresRole(c.Requirements, types.RegisteredRoleKey)
		grantsReg := types.HasRequirementKey(c.Requirements, types.ReqRole, types.RegisteredRoleKey) && !needsReg
		switch c.Category {
		case "staff":
			if !hasStaff {
				bad("%s: staff command without the staff role requirement", c.Name)
			}
			if needsReg {
				note("%s: staff command also requires the registered role", c.Name)
			}
		case "general":
			if hasStaff {
				bad("%s: general command requires the staff role", c.Name)
			}
			if !needsReg && !grantsReg {
				note("%s: general command with no registered-role rule (open to anyone)", c.Name)
			}
		case "admin":
			if hasStaff {
				note("%s: admin command requires the staff role", c.Name)
			}
		}
		if c.Cooldown == 0 {
			note("%s: no cooldown", c.Name)
		}
	}

	// one key is one setting: it must keep one type, and be optional or required the same way everywhere it is declared
	type use struct{ types map[types.RequirementType][]string; optional map[bool][]string }
	keys := map[string]*use{}
	addReq := func(owner string, reqs []types.Requirement) {
		for _, r := range reqs {
			u := keys[r.Key]
			if u == nil {
				u = &use{types: map[types.RequirementType][]string{}, optional: map[bool][]string{}}
				keys[r.Key] = u
			}
			u.types[r.Type] = append(u.types[r.Type], owner)
			if !r.Grants {
				u.optional[r.Optional] = append(u.optional[r.Optional], owner)
			}
		}
	}
	for _, c := range helpers.CommandRegistry {
		addReq("/"+c.Name, c.Requirements)
	}
	for _, e := range helpers.EventRegistry {
		addReq("event "+e.Name, e.Requirements)
	}
	for k, u := range keys {
		if len(u.types) > 1 {
			bad("requirement key %q is declared as more than one type: %v", k, u.types)
		}
		if len(u.optional) > 1 {
			note("requirement key %q is optional in %v but required in %v", k, u.optional[true], u.optional[false])
		}
	}

	sort.Strings(hard)
	sort.Strings(soft)
	t.Logf("commands: %d", len(helpers.CommandRegistry))
	for _, h := range hard {
		t.Errorf("HARD: %s", h)
	}
	for _, s := range soft {
		t.Logf("NOTE: %s", s)
	}
}
