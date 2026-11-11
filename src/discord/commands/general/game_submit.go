package general

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/match"
	"ranked-bedwars/src/discord/helpers/proofread"
	"ranked-bedwars/src/types"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

// minResultDuration is how long a match must have run before a result can be submitted.
const minResultDuration = 3 * time.Minute

func init() {
	helpers.Register(GameSubmitCommand)
	go proofread.Warm() // load the screenshot reader now, so the first screenshot does not wait for it
}

// ============================================================================
// Command Definition
// ============================================================================

// GameSubmitCommand Submits match results or proof for verification and ELO calculation.
var GameSubmitCommand = &types.Command{
	Name:        "submit",
	Description: "Submits match results or proof for verification and ELO calculation (game channel only).",
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "screenshot",
				Description: "Screenshot showing the win / game end screen",
				Type:        discordgo.ApplicationCommandOptionAttachment,
				Required:    true,
			},
			{
				Name:        "match_id",
				Description: "Optional Match ID to submit proof for",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
			{
				Name:        "proof_url",
				Description: "Optional screenshot or video link proving match outcome",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Execute: executeGameSubmitCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameSubmitCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	actor := helpers.InteractionUserID(i)

	// 1. Validate the Game Channel & Participant
	game, ok := match.Games.ByChannel(i.ChannelID)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, SubmitResultNotGameChannelEmbed(i))
	}
	player, isParticipant := game.Player(actor)
	if !isParticipant {
		return helpers.EditInteractionEmbed(s, i, SubmitResultNotInMatchEmbed(i))
	}

	// 2. Validate the Attachment (fallback to proof_url string if attachment is absent)
	attachment := attachmentOption(i, "screenshot")
	var proofURL string
	if attachment != nil {
		if !isImageAttachment(attachment.ContentType, attachment.Filename) {
			return helpers.EditInteractionEmbed(s, i, SubmitResultInvalidAttachmentEmbed(i))
		}
		proofURL = attachment.URL
	} else {
		proofURL = helpers.OptionString(i, "proof_url")
		if proofURL == "" {
			return helpers.EditInteractionEmbed(s, i, SubmitResultMissingAttachmentEmbed(i))
		}
	}

	// Whatever the answer is, the screenshot they sent is shown with it.
	fail := func(e *discordgo.MessageEmbed) error {
		return helpers.EditInteractionEmbed(s, i, helpers.WithProofImage(e, proofURL))
	}

	// 3. Validate the Game State
	// A game already with staff can be submitted again: the newest screenshot replaces the one before.
	resubmit := game.State == match.GamePostGame && game.End == match.EndStaffPending
	if game.State != match.GameInGame && !resubmit {
		return fail(SubmitResultNotInProgressEmbed(i))
	}
	if !game.StartedAt.IsZero() {
		if elapsed := time.Since(game.StartedAt); elapsed < minResultDuration {
			return fail(SubmitResultTooEarlyEmbed(i, minResultDuration-elapsed))
		}
	}
	if player.Team == "" {
		return fail(SubmitResultNoTeamEmbed(i))
	}

	// 4. Read the screenshot's chat: the winners, the losers, the bed and the MVP are checked against the game, for staff to see.
	ctx, sub := beginSubmission(s, game.ID) // from here on this is the submission that counts; the one before is cancelled
	defer sub.cancel()
	replaced := func() error {
		return helpers.EditInteractionEmbed(s, i, helpers.WithProofImage(submissionReplacedEmbed(), proofURL))
	}
	helpers.ChainPrefixReplies(i) // the answer below replaces the "reading" message instead of following it
	var review *proofread.Result
	unread := false
	flow := match.SubmitFlowOf(i.GuildID)
	if attachment != nil && flow.Mode != match.SubmitManual && proofread.Available() {
		_ = helpers.EditInteractionEmbed(s, i, helpers.WithProofImage(SubmitResultProcessingEmbed(), proofURL)) // shown while the picture is read
		check, err := readScreenshot(ctx, s, attachment.URL, game, player.Team)
		switch {
		case err != nil:
			log.Printf("[Submit] could not read the screenshot of game %d: %v", game.ID, err)
			unread = true
		case !check.HasEvidence():
			unread = true // the submission still goes through: staff see the screenshot and score it
		default:
			review = &check
		}
	}
	reply := func(e *discordgo.MessageEmbed, staffReview bool) error {
		if review == nil {
			if unread {
				e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Screenshot reading", Value: "The game chat could not be read from this screenshot, so staff score the game by hand."})
			}
			return fail(e)
		}
		e.Fields = append(e.Fields, reviewFields(game, *review)...)
		if staffReview {
			return helpers.EditInteractionComponents(s, i, helpers.WithProofImage(e, proofURL), reviewButtons(game, *review, sub.seq))
		}
		return fail(e)
	}

	if !sub.isLatest(game.ID) {
		return replaced() // a newer screenshot came in while this one was being read
	}

	// 5. Auto mode scores a clear reading at once; anything unclear goes to staff to verify, with the reason
	if game.Mode.Rated() && flow.Mode == match.SubmitAuto && review != nil {
		if why := autoScoreBlocker(game, *review, player.Team, flow.MinPlayersPercent); why == "" {
			result, err := match.Games.Score(sdk.SystemActor, i.GuildID, game.ID, review.Winner, match.ScoreOptions{MVP: mvpOf(game, *review)})
			if err == nil {
				return fail(autoScoredEmbed(game, actor, review.Winner, result.Results, *review, proofURL))
			}
			log.Printf("[Submit] could not auto-score game %d: %v", game.ID, err)
			review.Mismatch = "automatic scoring failed: " + err.Error()
		} else {
			review.Held = why
		}
	}

	// 6. Ranked games wait for staff, casual games are scored immediately
	if game.Mode.Rated() {
		// The claim is recorded on the game: it moves to staff scoring and its players are free to queue again.
		submit := match.Games.Submit
		if resubmit {
			submit = match.Games.Resubmit
		}
		if _, err := submit(game.ID, actor); err != nil {
			return fail(match.GameActionErrorEmbed("submit the result", err))
		}
		e := helpers.SubmitResultStaffScoringEmbed(i, game.Number, player.Team, proofURL)
		if resubmit {
			e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Updated", Value: "This replaces the earlier screenshot for this game."})
		}
		err := reply(e, true)
		sub.answered(helpers.ReplyMessage(s, i))
		return err
	}
	if _, err := match.Games.Score(actor, i.GuildID, game.ID, player.Team, match.ScoreOptions{}); err != nil {
		return fail(match.GameActionErrorEmbed("score the casual match", err))
	}
	return reply(helpers.SubmitResultCasualEmbed(i, game.Number, player.Team, proofURL), false)
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// attachmentOption resolves an attachment option of the command, or nil when it was not provided.
func attachmentOption(i *discordgo.InteractionCreate, name string) *discordgo.MessageAttachment {
	data := i.ApplicationCommandData()
	for _, opt := range data.Options {
		if opt.Name != name || opt.Type != discordgo.ApplicationCommandOptionAttachment {
			continue
		}
		id, _ := opt.Value.(string)
		if data.Resolved == nil {
			return nil
		}
		return data.Resolved.Attachments[id]
	}
	return nil
}

// teamColorsOf is the in-game color each team of the game was given by its map ("Team A" -> "Blue"); empty when the map has none.
func teamColorsOf(game match.Game) map[string]string {
	maps, err := helpers.GuildMetaMaps(game.GuildID)
	if err != nil {
		return nil
	}
	for _, cfg := range maps {
		if !strings.EqualFold(strings.TrimSpace(cfg.Name), strings.TrimSpace(game.Map)) {
			continue
		}
		out := map[string]string{}
		for i, color := range cfg.TeamsFor(game.Mode.Name) {
			if i < len(game.Teams) && strings.TrimSpace(color) != "" {
				out[game.Teams[i]] = color
			}
		}
		return out
	}
	return nil
}

// readScreenshot reads the chat of the screenshot against the game's roster, using the server profile's own chat rules.
func readScreenshot(ctx context.Context, s *discordgo.Session, imageURL string, game match.Game, claimedTeam string) (proofread.Result, error) {
	var profile proofread.Profile
	if raw, err := database.Must().GameConfig.Field(game.GuildID, "server"); err == nil {
		profile = proofread.LoadProfile(raw)
	}
	return proofread.Check(ctx, imageURL, profile, rosterOf(s, game), claimedTeam, teamColorsOf(game))
}

// rosterOf lists every name each player of the game goes by, since chat shows their Minecraft name while the game records their server nickname:
// the nickname without its [elo] tags, the Minecraft name the guild has on file, and their Discord nickname, display name and username.
func rosterOf(s *discordgo.Session, game match.Game) []proofread.Member {
	var roster []proofread.Member
	for _, p := range game.Players {
		seen := map[string]bool{}
		add := func(name string) {
			name = proofread.PlainName(name)
			if k := strings.ToLower(name); name != "" && !seen[k] {
				seen[k] = true
				roster = append(roster, proofread.Member{Name: name, Team: p.Team, ID: p.UserID})
			}
		}
		if view, err := database.Must().Player.Get(game.GuildID, p.UserID); err == nil {
			add(view.Config.Username) // the Minecraft name is what chat shows, so it comes first and is how the player is reported
		}
		add(p.Username)
		if m, _ := helpers.FetchGuildMember(s, game.GuildID, p.UserID); m != nil {
			add(m.Nick)
			if m.User != nil {
				add(m.User.GlobalName)
				add(m.User.Username)
			}
		}
	}
	return roster
}

func isImageAttachment(contentType, filename string) bool {
	if contentType == "" {
		// Fallback to filename check
		switch strings.ToLower(filepath.Ext(filename)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp":
			return true
		}
		return false
	}
	return strings.HasPrefix(contentType, "image/")
}

// ============================================================================
// Helper functions
// ============================================================================

func SubmitResultNotGameChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not a Game Channel",
		Description: "This command can only be used in a game text channel. Run it in the text channel of your match.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func SubmitResultNotInMatchEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not in Match",
		Description: "You must be a participant in this game to submit a result.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func SubmitResultMissingAttachmentEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Missing Attachment",
		Description: "You must provide a screenshot of the win screen as an attachment or provide a valid proof URL.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func SubmitResultInvalidAttachmentEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Attachment",
		Description: "The attachment must be an image file (PNG, JPG, GIF or WebP).",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func SubmitResultNotInProgressEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not In Progress",
		Description: "This game is not currently in progress, so a result cannot be submitted for it.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func SubmitResultTooEarlyEmbed(i *discordgo.InteractionCreate, remaining time.Duration) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Too Early",
		Description: fmt.Sprintf("You must wait **%s** before submitting a result for this game.", remaining.Round(time.Second)),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func SubmitResultAlreadySubmittedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Already Submitted",
		Description: "A result has already been submitted for this game and is waiting for staff to score it.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func SubmitResultNoTeamEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Team",
		Description: "You are not assigned to a team in this game, so you cannot submit a result.",
		Thumbnail:   helpers.ThumbFlag,
	})
}
