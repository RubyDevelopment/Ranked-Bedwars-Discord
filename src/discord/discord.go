package discord

import (
	"fmt"
	"os"

	"github.com/bwmarrin/discordgo"

	_ "ranked-bedwars/src/discord/commands/admin"
	_ "ranked-bedwars/src/discord/commands/general"
	_ "ranked-bedwars/src/discord/commands/staff"
	_ "ranked-bedwars/src/discord/events"
	"ranked-bedwars/src/discord/events/features"
	_ "ranked-bedwars/src/discord/events/interactions"
	_ "ranked-bedwars/src/discord/events/punishments"
	_ "ranked-bedwars/src/discord/events/setup"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/utility"
)

// ============================================================================
// Types & Constructor
// ============================================================================

// Manager controls the Discord session lifecycle, event registrations, and command syncing.
type Manager struct {
	BotID   string
	Session *discordgo.Session
	Logger  utility.AppLogger
}

// NewClient initializes and returns a new Discord Manager instance.
func NewClient() *Manager {
	botID := os.Getenv("DISCORD_BOT_ID")
	if botID == "" {
		botID = "UNKNOWN_BOT_ID"
		_ = os.Setenv("DISCORD_BOT_ID", botID)
	}

	return &Manager{
		BotID:  botID,
		Logger: utility.CreateModuleLogger("DISCORD_MANAGER", "Discord"),
	}
}

// ============================================================================
// Lifecycle Management
// ============================================================================

// Initialize creates the Discord session, registers event handlers, opens the connection,
// and schedules slash command syncing upon receiving the Ready event.
func (manager *Manager) Initialize() error {
	botToken := os.Getenv("DISCORD_BOT_TOKEN")
	if botToken == "" {
		manager.Logger.Error("Discord bot token is not set in environment variables. Please set DISCORD_BOT_TOKEN.")
		return fmt.Errorf("discord bot token is not set in environment variables")
	}

	session, err := discordgo.New("Bot " + botToken)
	if err != nil {
		manager.Logger.Error("Failed to create Discord session.")
		return fmt.Errorf("failed to create discord session: %w", err)
	}

	manager.Session = session
	manager.Session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMembers |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildMessageReactions |
		discordgo.IntentsGuildVoiceStates |
		discordgo.IntentsMessageContent |
		// The dashboard's custom events: bans, invites, Discord's scheduled events and AutoMod actions.
		discordgo.IntentsGuildBans |
		discordgo.IntentsGuildInvites |
		discordgo.IntentsGuildScheduledEvents |
		discordgo.IntentAutoModerationExecution
	// Keeps recent messages so an edited or deleted one can say what it was.
	manager.Session.State.MaxMessageCount = 500

	// Set up Ready handler for logging and command syncing
	manager.Session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		manager.Logger.Info(fmt.Sprintf("Discord bot logged in as %s#%s", r.User.Username, r.User.Discriminator))
		manager.syncSlashCommands(s, r.User.ID, os.Getenv("DEVELOPMENT_GUILD_ID"))
	})

	manager.registerEventHandlers()

	if err := manager.Session.Open(); err != nil {
		manager.Logger.Error("Failed to open Discord session.")
		return fmt.Errorf("failed to open discord session: %w", err)
	}

	return nil
}

// Start serves as a placeholder for lifecycle completeness. Open() in Initialize() starts listening.
func (manager *Manager) Start() error {
	return nil
}

// Stop safely closes the active Discord session and cleans up resources.
func (manager *Manager) Stop() error {
	if manager.Session != nil {
		if err := manager.Session.Close(); err != nil {
			manager.Logger.Error("Failed to close Discord session.")
			return fmt.Errorf("failed to close discord session: %w", err)
		}
		manager.Logger.Info("Discord session closed successfully.")
	}
	return nil
}

// Instance returns the underlying active discordgo.Session.
func (manager *Manager) Instance() (*discordgo.Session, error) {
	if manager.Session == nil {
		manager.Logger.Error("Discord session is not initialized. Please call Initialize() first.")
		return nil, fmt.Errorf("discord session is not initialized; call Initialize() first")
	}
	return manager.Session, nil
}

// ============================================================================
// Internal Helpers
// ============================================================================

// registerEventHandlers iterates over registered events and attaches active handlers to the session.
func (manager *Manager) registerEventHandlers() {
	for _, event := range helpers.EventRegistry {
		if event == nil || event.Disabled || event.Execute == nil {
			continue
		}

		manager.Session.AddHandler(event.Execute)
	}
}

// syncSlashCommands overwrites application commands globally or for a target guild.
func (manager *Manager) syncSlashCommands(session *discordgo.Session, appID, guildID string) {
	commandsToRegister := make([]*discordgo.ApplicationCommand, 0, len(helpers.CommandRegistry))
	seen := make(map[string]struct{}, len(helpers.CommandRegistry))

	for _, cmd := range helpers.CommandRegistry {
		if cmd == nil || cmd.Disabled {
			continue
		}

		// Initialize Data if nil
		if cmd.Data == nil {
			cmd.Data = &discordgo.ApplicationCommand{}
		}

		// Auto-populate Name and Description from Command struct if not set
		if cmd.Data.Name == "" {
			cmd.Data.Name = cmd.Name
		}
		if cmd.Data.Description == "" {
			cmd.Data.Description = cmd.Description
		}

		// Skip if still no name (required by Discord)
		if cmd.Data.Name == "" {
			continue
		}

		if _, exists := seen[cmd.Data.Name]; exists {
			continue
		}

		seen[cmd.Data.Name] = struct{}{}
		commandsToRegister = append(commandsToRegister, cmd.Data)
	}

	if _, err := session.ApplicationCommandBulkOverwrite(appID, guildID, commandsToRegister); err != nil {
		manager.Logger.Error(fmt.Sprintf("Failed to sync slash commands: %v", err))
		return
	}

	// Guilds' own commands from the dashboard's Command Builder are registered per guild.
	features.StartCustomCommandSync(session, appID, guildID, commandsToRegister)
}
