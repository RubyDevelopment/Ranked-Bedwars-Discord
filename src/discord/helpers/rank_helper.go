package helpers

import (
	"sync"
	"time"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
	"sort"
	"strings"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"
)

// ============================================================================

// SortedRanks returns rank tiers ordered by minimum ELO ascending.
func SortedRanks(ranks []types.Rank) []types.Rank {
	out := append([]types.Rank(nil), ranks...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].MinElo == out[j].MinElo {
			return out[i].RankName < out[j].RankName
		}
		return out[i].MinElo < out[j].MinElo
	})
	return out
}

// RankForElo returns the rank tier matching the given ELO.
func RankForElo(ranks []types.Rank, elo int) *types.Rank {
	sorted := SortedRanks(ranks)
	var current *types.Rank
	for i := range sorted {
		if elo >= sorted[i].MinElo {
			current = &sorted[i]
		}
	}
	return current
}

// ActiveSeasonKey retrieves the active season key for a guild. An unavailable
// database or a guild with no active season both return ("", 0, nil).
//
// Every stats card, leaderboard and game result asks for it, and it changes once a season, so the answer is kept for a few seconds.
func ActiveSeasonKey(guildID string) (string, uint, error) {
	svc := database.Get()
	if svc == nil {
		return "", 0, nil
	}

	seasons.mu.Lock()
	if e, ok := seasons.by[guildID]; ok && time.Now().Before(e.expires) {
		seasons.mu.Unlock()
		return e.name, e.id, nil
	}
	seasons.mu.Unlock()

	name, id := "", uint(0)
	season, err := svc.Season.Active(guildID)
	switch {
	case err == nil:
		name, id = season.Name, season.ID
	case !sdk.IsNotFound(err):
		return "", 0, err
	}
	seasons.mu.Lock()
	seasons.by[guildID] = activeSeason{name: name, id: id, expires: time.Now().Add(seasonTTL)}
	seasons.mu.Unlock()
	return name, id, nil
}

type activeSeason struct {
	name    string
	id      uint
	expires time.Time
}

const seasonTTL = 10 * time.Second

var seasons = struct {
	mu sync.Mutex
	by map[string]activeSeason
}{by: map[string]activeSeason{}}

// AdministratorPermission returns the permission bit for administrators.
func AdministratorPermission() *int64 {
	v := int64(discordgo.PermissionAdministrator)
	return &v
}

// ManageMessagesPermission returns the permission bit for managing messages.
func ManageMessagesPermission() *int64 {
	v := int64(discordgo.PermissionManageMessages)
	return &v
}

// SubOptionString reads a subcommand string option.
func SubOptionString(options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionString {
			return strings.TrimSpace(opt.StringValue())
		}
	}
	return ""
}

// SubOptionChannelID reads a subcommand channel option ID.
func SubOptionChannelID(options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionChannel {
			return opt.ChannelValue(nil).ID
		}
	}
	return ""
}

// SubOptionInt reads a subcommand integer option.
func SubOptionInt(options []*discordgo.ApplicationCommandInteractionDataOption, name string) int {
	for _, opt := range options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionInteger {
			return int(opt.IntValue())
		}
	}
	return 0
}

// SubOptionBool reads a subcommand boolean option, defaulting to false when absent.
func SubOptionBool(options []*discordgo.ApplicationCommandInteractionDataOption, name string) bool {
	for _, opt := range options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionBoolean {
			return opt.BoolValue()
		}
	}
	return false
}

// SubOptionUser reads a subcommand user option.
func SubOptionUser(s *discordgo.Session, options []*discordgo.ApplicationCommandInteractionDataOption, name string) *discordgo.User {
	for _, opt := range options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionUser {
			if raw, ok := opt.Value.(string); ok && !prefixSnowflakePattern.MatchString(raw) {
				return nil // a name no registered player has: not an ID to hand to the database
			}
			return opt.UserValue(s)
		}
	}
	return nil
}

// SubOptionRoleID reads a subcommand role option ID.
func SubOptionRoleID(s *discordgo.Session, options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionRole {
			role := opt.RoleValue(s, "")
			if role != nil {
				return role.ID
			}
		}
	}
	return ""
}

// FetchGuildMember loads a guild member, preferring cache when available.
func FetchGuildMember(s *discordgo.Session, guildID, userID string) (*discordgo.Member, error) {
	if s.State != nil {
		if member, err := s.State.Member(guildID, userID); err == nil && member != nil {
			return member, nil
		}
	}
	return s.GuildMember(guildID, userID)
}

// AddMemberRole assigns a role to a member.
func AddMemberRole(s *discordgo.Session, guildID, userID, roleID string) error {
	if strings.TrimSpace(roleID) == "" {
		return nil
	}
	return s.GuildMemberRoleAdd(guildID, userID, roleID)
}

// RemoveMemberRole removes a role from a member.
func RemoveMemberRole(s *discordgo.Session, guildID, userID, roleID string) error {
	if strings.TrimSpace(roleID) == "" {
		return nil
	}
	return s.GuildMemberRoleRemove(guildID, userID, roleID)
}

// MemberHasRole reports whether a member currently has the given role.
func MemberHasRole(member *discordgo.Member, roleID string) bool {
	if member == nil || roleID == "" {
		return false
	}
	for _, id := range member.Roles {
		if id == roleID {
			return true
		}
	}
	return false
}

// RoleHierarchyIssue is defined in package types (interactions.go).
type RoleHierarchyIssue = types.RoleHierarchyIssue

const (
	RoleHierarchyOK        RoleHierarchyIssue = ""
	RoleHierarchyManaged   RoleHierarchyIssue = "managed"
	RoleHierarchyDangerous RoleHierarchyIssue = "dangerous"
	RoleHierarchyExecutor  RoleHierarchyIssue = "executor"
	RoleHierarchyBot       RoleHierarchyIssue = "bot"
)

// GetRoleManageabilityIssue validates whether executor and bot can manage the target role.
func GetRoleManageabilityIssue(s *discordgo.Session, guildID string, role *discordgo.Role, executor, bot *discordgo.Member) RoleHierarchyIssue {
	if role == nil {
		return RoleHierarchyBot
	}
	if role.Managed {
		return RoleHierarchyManaged
	}
	if role.Permissions&discordgo.PermissionAdministrator != 0 {
		return RoleHierarchyDangerous
	}
	execPos := memberHighestRolePosition(s, guildID, executor)
	botPos := memberHighestRolePosition(s, guildID, bot)
	if executor != nil && !executorIsAdministrator(executor) && execPos <= role.Position {
		return RoleHierarchyExecutor
	}
	if bot != nil && botPos <= role.Position {
		return RoleHierarchyBot
	}
	return RoleHierarchyOK
}

// MemberManageabilityIssue validates whether the bot can moderate the target member.
func MemberManageabilityIssue(s *discordgo.Session, guildID string, target, bot *discordgo.Member) RoleHierarchyIssue {
	if target == nil || bot == nil {
		return RoleHierarchyOK
	}
	if target.User != nil && target.User.Bot {
		return RoleHierarchyManaged
	}
	targetPos := memberHighestRolePosition(s, guildID, target)
	botPos := memberHighestRolePosition(s, guildID, bot)
	if targetPos >= botPos {
		return RoleHierarchyBot
	}
	return RoleHierarchyOK
}

func memberHighestRolePosition(s *discordgo.Session, guildID string, member *discordgo.Member) int {
	if member == nil || len(member.Roles) == 0 {
		return 0
	}
	maxPos := 0
	for _, roleID := range member.Roles {
		role, err := s.State.Role(guildID, roleID)
		if err != nil || role == nil {
			continue
		}
		if role.Position > maxPos {
			maxPos = role.Position
		}
	}
	return maxPos
}

func executorIsAdministrator(member *discordgo.Member) bool {
	if member == nil {
		return false
	}
	return member.Permissions&discordgo.PermissionAdministrator != 0
}

// OptionString reads a top-level slash string option.
func OptionString(i *discordgo.InteractionCreate, name string) string {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionString {
			return strings.TrimSpace(opt.StringValue())
		}
	}
	return ""
}

// OptionInt reads a top-level slash integer option.
func OptionInt(i *discordgo.InteractionCreate, name string) int {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionInteger {
			return int(opt.IntValue())
		}
	}
	return 0
}

// OptionBool reads a top-level slash boolean option.
func OptionBool(i *discordgo.InteractionCreate, name string) (bool, bool) {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionBoolean {
			return opt.BoolValue(), true
		}
	}
	return false, false
}

// OptionUser reads a top-level slash user option.
func OptionUser(s *discordgo.Session, i *discordgo.InteractionCreate, name string) *discordgo.User {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionUser {
			// A prefix command can name a player instead of mentioning them (=stats nexus720): look the name up.
			if raw, ok := opt.Value.(string); ok && !prefixSnowflakePattern.MatchString(raw) {
				if user := userByPlayerName(i.GuildID, raw); user != nil {
					return user
				}
			}
			if raw, ok := opt.Value.(string); ok && !prefixSnowflakePattern.MatchString(raw) {
				return nil // a name no registered player has: not an ID to hand to the database
			}
			return opt.UserValue(s)
		}
	}
	return nil
}

// userByPlayerName finds the registered player of the guild whose Minecraft username (or nickname) is name.
func userByPlayerName(guildID, name string) *discordgo.User {
	svc := database.Get()
	if svc == nil || guildID == "" || strings.TrimSpace(name) == "" {
		return nil
	}
	config, err := svc.Player.GetByUsername(guildID, name)
	if err != nil {
		if config, err = svc.Player.GetByNickname(guildID, name); err != nil {
			return nil
		}
	}
	player, err := svc.Player.GetGlobal(sdk.UintToString(config.PlayerID))
	if err != nil || player.SnowflakeID == "" {
		return nil
	}
	return &discordgo.User{ID: player.SnowflakeID, Username: config.Username}
}

// ResolveUserOption reads a slash user option and falls back to the invoking user when
// the option is absent, so commands can target a user without requiring one.
func ResolveUserOption(s *discordgo.Session, i *discordgo.InteractionCreate, name string) *discordgo.User {
	if user := OptionUser(s, i, name); user != nil {
		return user
	}
	if OptionUserUnresolved(i, name) {
		return nil // they named someone nobody is registered as: not themselves
	}
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User
	}
	return i.User
}

// StatsAndRank loads a player's stats for the season (creating them when they have none yet) and their position on the leaderboard.
// The two are separate calls to the database; they are made together, so a stats card waits for one round trip instead of two.
func StatsAndRank(guildID, playerID string, seasonID uint) (*models.PlayerStatsModel, int, error) {
	position := make(chan int, 1)
	go func() {
		if rank, err := database.Must().Stats.Rank(guildID, playerID, seasonID); err == nil {
			position <- rank.Position
			return
		}
		position <- -1
	}()
	stats, err := database.Must().Stats.GetOrCreate(guildID, playerID, seasonID)
	pos := <-position
	if err != nil {
		return nil, 0, err
	}
	if pos < 0 { // a player with no stats yet has no position until they exist: ask again now that they do
		pos = 0
		if rank, err := database.Must().Stats.Rank(guildID, playerID, seasonID); err == nil {
			pos = rank.Position
		}
	}
	return stats, pos, nil
}
