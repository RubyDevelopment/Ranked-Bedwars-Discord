package general

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Party-Code Registration
// ============================================================================

// In a guild whose registration_verification.mode is "party_code", /player_register proves the user owns the
// Minecraft account before linking it:
//
//	/player_register <ign> → the engine's bot invites <ign> to a party and says a 6-digit code in party chat
//	→ the user presses "Enter code" and submits it in a modal → the account is linked.
//
// The code and its limits live in helpers.RegistrationVerifier; the bot's side is the engine's verify.js.

// engineVerifyTimeout covers the engine joining the server, the invite and the player accepting it.
const engineVerifyTimeout = 4 * time.Minute

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.RegisterEvent(PlayerRegisterVerifyEvent)
}

// PlayerRegisterVerifyEvent answers the "Enter code" button and the modal it opens.
var PlayerRegisterVerifyEvent = &types.Event{
	Name:        "Registration Code Entry",
	System:      true,
	Description: "Opens the verification code form when a player presses Enter code, and finishes their registration when the code is right.",
	Type:        "interaction_create",
	Execute:     handlePlayerRegisterVerify,
}

// ============================================================================
// Event Handling
// ============================================================================

// startPartyCodeRegistration sends the code to the player's party chat and offers the button to enter it.
func startPartyCodeRegistration(s *discordgo.Session, i *discordgo.InteractionCreate, userID, username string) error {
	if helpers.IsPrefixInteraction(i) {
		return helpers.EditInteractionEmbed(s, i, registerUseSlashEmbed())
	}
	if !helpers.Engine.Connected() {
		return helpers.EditInteractionEmbed(s, i, registerVerifyUnavailableEmbed())
	}

	// A name somebody already has can never be registered, so don't invite anyone for it.
	if _, err := database.Must().Player.GetByUsername(i.GuildID, username); err == nil {
		return helpers.EditInteractionEmbed(s, i, playerRegisterConflictEmbed(username))
	} else if !sdk.IsNotFound(err) {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerRegisterEmbed())
	}

	code, err := helpers.RegistrationVerifier.Begin(i.GuildID, userID, username)
	var limited *helpers.RateLimitError
	if errors.As(err, &limited) {
		return helpers.EditInteractionEmbed(s, i, registerRateLimitedEmbed(limited.Wait))
	} else if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerRegisterEmbed())
	}

	_ = helpers.EditInteractionEmbed(s, i, registerContactingEmbed(username))
	ctx, cancel := context.WithTimeout(context.Background(), engineVerifyTimeout)
	defer cancel()
	message := fmt.Sprintf("Your Ranked Bedwars verification code is %s. Enter it in Discord to finish registering. Never share it.", code)
	res, err := helpers.Engine.Verify(ctx, i.GuildID, username, code, message)
	if err != nil || res.Status != "sent" {
		helpers.RegistrationVerifier.Discard(i.GuildID, userID)
		return helpers.EditInteractionEmbed(s, i, registerVerifyFailedEmbed(username, res.Status))
	}

	return helpers.EditInteractionComponents(s, i, registerCodePromptEmbed(username), []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Enter code", Style: discordgo.PrimaryButton, CustomID: helpers.RegVerifyOpenID},
		}},
	})
}

func handlePlayerRegisterVerify(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil || i.GuildID == "" {
		return
	}
	switch i.Type {
	case discordgo.InteractionMessageComponent:
		if i.MessageComponentData().CustomID == helpers.RegVerifyOpenID {
			openRegisterCodeModal(s, i)
		}
	case discordgo.InteractionModalSubmit:
		if i.ModalSubmitData().CustomID == helpers.RegVerifyModalID {
			submitRegisterCode(s, i)
		}
	}
}

func openRegisterCodeModal(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !helpers.RegistrationVerifier.Pending(i.GuildID, helpers.InteractionUserID(i)) {
		respondEphemeral(s, i, registerNoPendingEmbed())
		return
	}
	_ = helpers.RespondModal(s, i, helpers.RegVerifyModalID, "Verify your Minecraft account",
		helpers.ModalTextInput(helpers.RegVerifyCodeField, "6-digit code from the party chat", "", "123456", false, true, 6))
}

func submitRegisterCode(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := helpers.InteractionUserID(i)
	input := helpers.ModalValues(i)[helpers.RegVerifyCodeField]

	username, result, left := helpers.RegistrationVerifier.Check(i.GuildID, userID, input)
	if result != helpers.CodeOK {
		respondEphemeral(s, i, registerCodeRejectedEmbed(result, left))
		return
	}

	// The code is right: turn the prompt message into the result (registering takes a few calls, so acknowledge first).
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate}); err != nil {
		return
	}
	var embed *discordgo.MessageEmbed
	if existing, err := database.Must().Player.Get(i.GuildID, userID); err == nil && existing != nil {
		embed = playerRegisterAlreadyRegisteredEmbed(existing.Config.Username)
	} else {
		embed = registerNewPlayer(s, i.GuildID, userID, username)
	}
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds:     &[]*discordgo.MessageEmbed{embed},
		Components: &[]discordgo.MessageComponent{},
	})
}

func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, emb *discordgo.MessageEmbed) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{emb}, Flags: discordgo.MessageFlagsEphemeral},
	})
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ============================================================================
// Embed Builders
// ============================================================================

func registerContactingEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Searching For Player",
		Description: fmt.Sprintf("A bot is inviting **%s** to a party on the Minecraft server. Make sure you are online and **accept the party invite**. This can take up to a minute or two.", username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func registerCodePromptEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Check Your Party Chat",
		Description: fmt.Sprintf("A 6-digit code was sent to **%s** in the Minecraft party chat. Press **Enter code** and type it in to finish registering. The code expires in 10 minutes.", username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func registerUseSlashEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Slash Command Required",
		Description: "This guild verifies Minecraft accounts with a code, which needs buttons. Please use `/player_register` instead of the prefix command.",
	})
}

func registerVerifyUnavailableEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Verification Unavailable",
		Description: "Account verification is not available right now. Please try again in a few minutes, or contact an administrator if it keeps happening.",
	})
}

func registerRateLimitedEmbed(wait time.Duration) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Slow Down",
		Description: fmt.Sprintf("A verification code was requested very recently. Please try again in **%s**.", wait.Round(time.Second)),
	})
}

func registerNoPendingEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Nothing To Verify",
		Description: "You have no registration waiting for a code, or it expired. Run `/player_register` to start again.",
	})
}

func registerCodeRejectedEmbed(result helpers.CodeResult, attemptsLeft int) *discordgo.MessageEmbed {
	switch result {
	case helpers.CodeWrong:
		return helpers.Preset("error", types.PresetOptions{
			Title:       "Invalid Code",
			Description: fmt.Sprintf("That code is not correct. You have **%d** attempt%s left.", attemptsLeft, plural(attemptsLeft)),
		})
	case helpers.CodeInvalid:
		return helpers.Preset("error", types.PresetOptions{
			Title:       "Invalid Code",
			Description: "The code is 6 digits long. Check the Minecraft party chat and try again.",
		})
	case helpers.CodeLocked:
		return helpers.Preset("error", types.PresetOptions{
			Title:       "Too Many Attempts",
			Description: "That was the last attempt. Run `/player_register` again to get a new code.",
		})
	}
	return registerNoPendingEmbed()
}

func registerVerifyFailedEmbed(username, status string) *discordgo.MessageEmbed {
	title, description := "Verification Unavailable", "Account verification is not available right now. Please try again in a few minutes, or contact an administrator if it keeps happening."
	switch status {
	case "offline":
		title = "Join The Server First"
		description = fmt.Sprintf("**%s** does not appear to be online on the Minecraft server. Join the server, then run `/player_register` again.", username)
	case "not_joined":
		title = "Party Invite Not Accepted"
		description = fmt.Sprintf("A party invite was sent to **%s**, but you did not join it in time. Make sure you are on the server and accept the invite, then run `/player_register` again.", username)
	case "no_bot":
		title = "Verification Busy"
		description = "Every verification bot is busy right now. Please try again in a minute."
	}
	return helpers.Preset("error", types.PresetOptions{Title: title, Description: strings.TrimSpace(description)})
}
