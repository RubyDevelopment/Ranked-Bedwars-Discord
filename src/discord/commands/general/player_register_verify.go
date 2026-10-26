package general

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	cmdcfg "ranked-bedwars/src/discord/config"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Party-Code Registration
// ============================================================================

// In a guild whose registration_verification.mode is "party_code", /player_register proves the user owns the
// Minecraft account before linking it:
//
//	/player_register <ign> → the engine's bot invites <ign> to a party and says a one-time code in party chat
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
func startPartyCodeRegistration(s *discordgo.Session, i *discordgo.InteractionCreate, userID, username string, cfg *types.InGameCode) error {
	if helpers.IsPrefixInteraction(i) {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.RegisterUseSlashEmbed(i))
	}
	if !helpers.Engine.Connected() {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.RegisterVerifyUnavailableEmbed(i))
	}

	// A name somebody already has can never be registered, so don't invite anyone for it.
	if _, err := database.Must().Player.GetByUsername(i.GuildID, username); err == nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerRegisterConflictEmbed(i, username))
	} else if !sdk.IsNotFound(err) {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorPlayerRegisterEmbed(i))
	}

	code, err := helpers.RegistrationVerifier.Begin(i.GuildID, userID, username, cfg)
	var limited *helpers.RateLimitError
	if errors.As(err, &limited) {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.RegisterRateLimitedEmbed(i, limited.Wait))
	} else if err != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorPlayerRegisterEmbed(i))
	}

	_ = helpers.EditInteractionEmbed(s, i, cmdcfg.RegisterContactingEmbed(i, username))
	ctx, cancel := context.WithTimeout(context.Background(), engineVerifyTimeout)
	defer cancel()
	message := fmt.Sprintf("Your Ranked Bedwars verification code is %s. Enter it in Discord to finish registering. Never share it.", code)
	res, err := helpers.Engine.Verify(ctx, i.GuildID, username, code, message)
	if err != nil || res.Status != "sent" {
		helpers.RegistrationVerifier.Discard(i.GuildID, userID)
		return helpers.EditInteractionEmbed(s, i, cmdcfg.RegisterVerifyFailedEmbed(i, username, res.Status))
	}

	return helpers.EditInteractionComponents(s, i, cmdcfg.RegisterCodePromptEmbed(i, username, len(code), cfg), []discordgo.MessageComponent{
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
		respondEphemeral(s, i, cmdcfg.RegisterNoPendingEmbed(i))
		return
	}
	_ = helpers.RespondModal(s, i, helpers.RegVerifyModalID, "Verify your Minecraft account",
		helpers.ModalTextInput(helpers.RegVerifyCodeField, "Code from the party chat", "", "", false, true, 12))
}

func submitRegisterCode(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := helpers.InteractionUserID(i)
	input := helpers.ModalValues(i)[helpers.RegVerifyCodeField]

	username, result, left := helpers.RegistrationVerifier.Check(i.GuildID, userID, input)
	if result != helpers.CodeOK {
		respondEphemeral(s, i, cmdcfg.RegisterCodeRejectedEmbed(i, result, left))
		return
	}

	// The code is right: turn the prompt message into the result (registering takes a few calls, so acknowledge first).
	if err := helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate}); err != nil {
		return
	}
	var embed *discordgo.MessageEmbed
	if existing, err := database.Must().Player.Get(i.GuildID, userID); err == nil && existing != nil {
		embed = cmdcfg.PlayerRegisterAlreadyRegisteredEmbed(i, existing.Config.Username)
	} else {
		embed = registerNewPlayer(i, s, i.GuildID, userID, username)
	}
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds:     &[]*discordgo.MessageEmbed{embed},
		Components: &[]discordgo.MessageComponent{},
	})
}

func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, emb *discordgo.MessageEmbed) {
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{emb}, Flags: discordgo.MessageFlagsEphemeral},
	})
}
