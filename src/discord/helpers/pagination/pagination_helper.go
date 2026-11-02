package pagination

import (
	"fmt"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
	"strconv"
	"strings"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"
)

const historyPagePrefix = "pgv1|"

// HistoryPageKind is defined in package types (interactions.go).
type HistoryPageKind = types.HistoryPageKind

const (
	HistoryPageStrikes     HistoryPageKind = "strikes"
	HistoryPagePunishments HistoryPageKind = "punishments"
	HistoryPageGames       HistoryPageKind = "games"
	HistoryPageDecays      HistoryPageKind = "decays"
)

// historyJumpSize is how many pages the jump buttons move by.
const historyJumpSize = 5

// IsHistoryPaginationCustomID reports whether a component custom ID belongs to
// the shared history pagination buttons.
func IsHistoryPaginationCustomID(customID string) bool {
	return strings.HasPrefix(customID, historyPagePrefix)
}

// BuildHistoryPageComponents builds the pagination button row for a history
// embed. The middle button displays the current page and is non-clickable. The
// Back 5 / Next 5 buttons are only shown when there are at least 5 pages.
func BuildHistoryPageComponents(kind HistoryPageKind, snowflake string, page, totalPages int, opts []string) []discordgo.MessageComponent {
	if totalPages <= 1 {
		return nil
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}

	row := &discordgo.ActionsRow{Components: make([]discordgo.MessageComponent, 0, 5)}

	if totalPages >= historyJumpSize {
		back5 := page - historyJumpSize
		if back5 < 1 {
			back5 = 1
		}
		row.Components = append(row.Components, historyPageButton(kind, snowflake, back5, "Back 5", "back5", opts, page <= historyJumpSize))
	}

	back := page - 1
	if back < 1 {
		back = 1
	}
	row.Components = append(row.Components, historyPageButton(kind, snowflake, back, "Back", "back", opts, page == 1))

	row.Components = append(row.Components, &discordgo.Button{
		Label:    fmt.Sprintf("%d/%d", page, totalPages),
		Style:    discordgo.SecondaryButton,
		Disabled: true,
		CustomID: historyPagePrefix + "page",
	})

	next := page + 1
	if next > totalPages {
		next = totalPages
	}
	row.Components = append(row.Components, historyPageButton(kind, snowflake, next, "Next", "next", opts, page == totalPages))

	if totalPages >= historyJumpSize {
		next5 := page + historyJumpSize
		if next5 > totalPages {
			next5 = totalPages
		}
		row.Components = append(row.Components, historyPageButton(kind, snowflake, next5, "Next 5", "next5", opts, page > totalPages-historyJumpSize))
	}

	return []discordgo.MessageComponent{row}
}

// historyPageButton builds one pagination button. A button that cannot be pressed (the jump buttons clamp to the same
// page as Back/Next at either end) gets an inert id of its own: Discord refuses a message in which two components
// share a custom id, and a disabled button's id is never read. slot names the button's place in the row.
func historyPageButton(kind HistoryPageKind, snowflake string, targetPage int, label, slot string, opts []string, disabled bool) discordgo.MessageComponent {
	customID := encodeHistoryPageCustomID(kind, snowflake, targetPage, opts)
	if disabled {
		customID = historyPagePrefix + "off-" + slot
	}
	return &discordgo.Button{
		CustomID: customID,
		Label:    label,
		Style:    discordgo.SecondaryButton,
		Disabled: disabled,
	}
}

// encodeHistoryPageCustomID encodes a button target into a custom ID:
// pgv1|kind|snowflake|page|option...
func encodeHistoryPageCustomID(kind HistoryPageKind, snowflake string, page int, opts []string) string {
	parts := make([]string, 0, 4+len(opts))
	parts = append(parts, "pgv1", string(kind), snowflake, strconv.Itoa(page))
	parts = append(parts, opts...)
	return strings.Join(parts, "|")
}

func decodeHistoryPageCustomID(customID string) (kind HistoryPageKind, snowflake string, page int, opts []string, ok bool) {
	if !IsHistoryPaginationCustomID(customID) {
		return "", "", 0, nil, false
	}
	parts := strings.Split(customID, "|")
	if len(parts) < 4 {
		return "", "", 0, nil, false
	}
	page, err := strconv.Atoi(parts[3])
	if err != nil || page < 1 {
		return "", "", 0, nil, false
	}
	return HistoryPageKind(parts[1]), parts[2], page, parts[4:], true
}

func historyOptionAt(opts []string, index int) string {
	if index < 0 || index >= len(opts) {
		return ""
	}
	return opts[index]
}

func clampHistoryPage(page, totalPages int) int {
	if totalPages < 1 {
		return 1
	}
	if page < 1 {
		return 1
	}
	if page > totalPages {
		return totalPages
	}
	return page
}

// HandleHistoryPageClick responds to a Next/Back click on a strike,
// punishment or game history embed (see BuildHistoryPageComponents). It
// reports false when customID isn't one of these buttons, so the caller can
// fall through to other component handling.
func HandleHistoryPageClick(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	kind, snowflake, page, opts, ok := decodeHistoryPageCustomID(customID)
	if !ok {
		return false
	}

	// Only the person who ran the command turns its pages.
	if owner := historyOwner(i); owner != "" && owner != helpers.InteractionUserID(i) {
		_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Flags:  discordgo.MessageFlagsEphemeral,
				Embeds: []*discordgo.MessageEmbed{helpers.Preset("error", types.PresetOptions{Title: "Permission Denied", Description: "Only the player who ran this command can change pages."})},
			},
		})
		return true
	}

	emb, components, err := renderHistoryPage(i.GuildID, kind, snowflake, page, opts)
	if err != nil {
		emb = helpers.Preset("error", types.PresetOptions{
			Title:       "Error",
			Description: "Could not load that page. Please re-run the original command.",
		})
		components = nil
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{emb},
			Components: components,
		},
	})
	return true
}

// historyOwner is who ran the command a history message answers ("" when Discord doesn't say).
func historyOwner(i *discordgo.InteractionCreate) string {
	if i.Message == nil {
		return ""
	}
	if meta := i.Message.InteractionMetadata; meta != nil && meta.User != nil {
		return meta.User.ID
	}
	if i.Message.Interaction != nil && i.Message.Interaction.User != nil {
		return i.Message.Interaction.User.ID
	}
	// A prefix command's answer is a reply to the message that ran it.
	if ref := i.Message.ReferencedMessage; ref != nil && ref.Author != nil {
		return ref.Author.ID
	}
	return ""
}

func renderHistoryPage(guildID string, kind HistoryPageKind, snowflake string, page int, opts []string) (*discordgo.MessageEmbed, []discordgo.MessageComponent, error) {
	svc := database.Get()
	if svc == nil {
		return nil, nil, fmt.Errorf("database service not initialized")
	}
	view, err := svc.Player.Get(guildID, snowflake)
	if err != nil {
		return nil, nil, err
	}
	username := view.Config.Username

	switch kind {
	case HistoryPageStrikes:
		duration, _ := helpers.ParseTimeFilter(historyOptionAt(opts, 0))
		entries, err := svc.Strikes.AllByPlayer(guildID, sdk.UintToString(view.Player.ID), 0, false)
		if err != nil {
			return nil, nil, err
		}
		filtered := helpers.FilterStrikeHistoryEntries(entries, duration)
		totalPages := helpers.HistoryTotalPages(len(filtered))
		page = clampHistoryPage(page, totalPages)
		emb := helpers.BuildStrikeHistoryEmbed(username, filtered, page-1)
		components := BuildHistoryPageComponents(HistoryPageStrikes, snowflake, page, totalPages, opts)
		return emb, components, nil

	case HistoryPagePunishments:
		activeOnly := historyOptionAt(opts, 0) == "1"
		duration, _ := helpers.ParseTimeFilter(historyOptionAt(opts, 1))
		entries, err := svc.Punishments.ByPlayer(guildID, sdk.UintToString(view.Player.ID), false, "", sdk.Page{})
		if err != nil {
			return nil, nil, err
		}
		filtered := helpers.FilterPunishmentHistoryEntries(entries.Data, activeOnly, duration)
		totalPages := helpers.HistoryTotalPages(len(filtered))
		page = clampHistoryPage(page, totalPages)
		emb := helpers.BuildPunishmentHistoryEmbed(username, filtered, page-1, activeOnly)
		components := BuildHistoryPageComponents(HistoryPagePunishments, snowflake, page, totalPages, opts)
		return emb, components, nil

	case HistoryPageDecays:
		duration, _ := helpers.ParseTimeFilter(historyOptionAt(opts, 0))
		_, seasonID, err := helpers.ActiveSeasonKey(guildID)
		if err != nil {
			return nil, nil, err
		}
		entries, err := svc.Decays.AllByPlayer(guildID, sdk.UintToString(view.Player.ID), seasonID, false)
		if err != nil {
			return nil, nil, err
		}
		filtered := helpers.FilterDecayHistoryEntries(entries, duration)
		totalPages := helpers.HistoryTotalPages(len(filtered))
		page = clampHistoryPage(page, totalPages)
		emb := helpers.BuildDecayHistoryEmbed(username, filtered, page-1)
		components := BuildHistoryPageComponents(HistoryPageDecays, snowflake, page, totalPages, opts)
		return emb, components, nil

	case HistoryPageGames:
		duration, _ := helpers.ParseTimeFilter(historyOptionAt(opts, 0))
		hideVoided := historyOptionAt(opts, 1) == "1"
		_, seasonID, err := helpers.ActiveSeasonKey(guildID)
		if err != nil {
			return nil, nil, err
		}
		participants, err := svc.Player.AllGames(guildID, sdk.UintToString(view.Player.ID))
		if err != nil {
			return nil, nil, err
		}
		entries := helpers.FilterGameHistoryEntries(participants, seasonID, duration, hideVoided)
		totalPages := helpers.HistoryTotalPages(len(entries))
		page = clampHistoryPage(page, totalPages)
		playersByGame, err := helpers.LoadGameHistoryPlayers(svc, guildID, entries, page-1)
		if err != nil {
			return nil, nil, err
		}
		emb := helpers.BuildGameHistoryEmbedWithPlayers(guildID, username, entries, page-1, playersByGame)
		components := BuildHistoryPageComponents(HistoryPageGames, snowflake, page, totalPages, opts)
		return emb, components, nil
	}
	return nil, nil, fmt.Errorf("unknown history page kind %q", kind)
}

// ── merged from interaction_ack.go ──
