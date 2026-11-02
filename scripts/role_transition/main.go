// role_transition moves a member between roles in every server the bot is in: it takes one role away and gives others.
// It is the old bot's test-remove-role script, run with the new bot's token.
//
// Nothing is changed unless --yes is given, so a first run only shows what would happen.
//
//	go run ./scripts/role_transition --user 123456789012345678                        (preview, with the default roles)
//	go run ./scripts/role_transition --user 123456789012345678 --yes
//	go run ./scripts/role_transition --user 1234… --user 5678… --remove-role 1241… --add-role 1241… --add-role 1228… --yes
//	go run ./scripts/role_transition --user 1234… --guild 1228653980780072981 --yes   (one server only)
//
// The token is read from DISCORD_BOT_TOKEN (the bot's .env), or --token. A server where the member is missing, or where a
// role does not exist, is skipped; a role above the bot's own is reported, not forced.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

// The old script's roles, for the main server: the role taken away and the two given.
const defaultRemoveRole = "1241707942420549714"

var defaultAddRoles = []string{"1241748756257570836", "1228653981086257184"}

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, strings.TrimSpace(v)); return nil }

func main() {
	var users, addRoles list
	token := flag.String("token", "", "bot token (default: DISCORD_BOT_TOKEN)")
	removeRole := flag.String("remove-role", defaultRemoveRole, "role to take away")
	guildOnly := flag.String("guild", "", "only this server")
	confirmed := flag.Bool("yes", false, "make the changes (without it nothing is changed)")
	flag.Var(&users, "user", "Discord user ID (repeat for several)")
	flag.Var(&addRoles, "add-role", "role to give (repeat for several; default: the old script's two roles)")
	flag.Parse()

	_ = godotenv.Load()
	if len(users) == 0 {
		fmt.Fprintln(os.Stderr, "Missing --user <discord id>.")
		os.Exit(2)
	}
	if len(addRoles) == 0 {
		addRoles = defaultAddRoles
	}
	tok := strings.TrimSpace(*token)
	if tok == "" {
		tok = strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN"))
	}
	if tok == "" {
		fmt.Fprintln(os.Stderr, "Missing the bot token: set DISCORD_BOT_TOKEN or pass --token.")
		os.Exit(2)
	}

	s, err := discordgo.New("Bot " + strings.TrimPrefix(tok, "Bot "))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	me, err := s.User("@me")
	if err != nil {
		fmt.Fprintln(os.Stderr, "The token was refused:", err)
		os.Exit(1)
	}
	fmt.Printf("Logged in as %s (%s). Taking away %s, giving %s.\n", me.Username, me.ID, *removeRole, strings.Join(addRoles, ", "))

	guilds, err := allGuilds(s)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not list the servers:", err)
		os.Exit(1)
	}
	failed := 0
	for _, g := range guilds {
		if *guildOnly != "" && g.ID != *guildOnly {
			continue
		}
		roles, err := s.GuildRoles(g.ID)
		if err != nil {
			fmt.Printf("%s (%s): could not read roles: %v\n", g.Name, g.ID, err)
			failed++
			continue
		}
		exists := map[string]bool{}
		for _, r := range roles {
			exists[r.ID] = true
		}
		give := make([]string, 0, len(addRoles))
		for _, id := range addRoles {
			if exists[id] {
				give = append(give, id)
			}
		}
		for _, user := range users {
			member, err := s.GuildMember(g.ID, user)
			if err != nil {
				fmt.Printf("%s: %s is not a member\n", g.Name, user)
				continue
			}
			has := map[string]bool{}
			for _, id := range member.Roles {
				has[id] = true
			}
			if exists[*removeRole] && has[*removeRole] {
				failed += apply(s, g, user, "remove "+*removeRole, *confirmed, func() error { return s.GuildMemberRoleRemove(g.ID, user, *removeRole) })
			}
			for _, id := range give {
				if !has[id] {
					failed += apply(s, g, user, "add "+id, *confirmed, func() error { return s.GuildMemberRoleAdd(g.ID, user, id) })
				}
			}
		}
	}
	if !*confirmed {
		fmt.Println("Preview only. Run again with --yes to make these changes.")
	}
	if failed > 0 {
		os.Exit(1)
	}
}

// apply runs one role change (or only reports it in a preview) and says how it went. It returns 1 on a failure.
func apply(_ *discordgo.Session, g *discordgo.UserGuild, user, what string, confirmed bool, do func() error) int {
	if !confirmed {
		fmt.Printf("  would %s for %s in %s\n", what, user, g.Name)
		return 0
	}
	if err := do(); err != nil {
		fmt.Printf("  FAILED %s for %s in %s: %v (is the role above the bot's?)\n", what, user, g.Name, err)
		return 1
	}
	fmt.Printf("  did %s for %s in %s\n", what, user, g.Name)
	return 0
}

// allGuilds pages through every server the bot is in.
func allGuilds(s *discordgo.Session) ([]*discordgo.UserGuild, error) {
	var out []*discordgo.UserGuild
	after := ""
	for {
		page, err := s.UserGuilds(200, "", after, false)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < 200 {
			return out, nil
		}
		after = page[len(page)-1].ID
	}
}
