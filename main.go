package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/utility"
)

// ============================================================================
// Types
// ============================================================================

// Application holds the core services and loggers required to run the application.
type Application struct {
	Discord  *discord.Manager
	Database *database.Manager
	Logger   utility.AppLogger
}

// ============================================================================
// Entry Point
// ============================================================================

func main() {
	// Load environment variables from .env if present.
	if err := godotenv.Load(); err != nil {
		fmt.Fprintln(os.Stderr, "No .env file found. Proceeding with system environment variables.")
	}

	app := &Application{
		Discord:  discord.NewClient(),
		Database: database.NewClient(),
		Logger:   utility.CreateModuleLogger("RANKED_BEDWARS", "Global"),
	}

	if err := app.Start(); err != nil {
		app.Logger.Error(fmt.Sprintf("Application exited with error: %v", err))
		os.Exit(1)
	}
}

// ============================================================================
// Lifecycle Management
// ============================================================================

// Start initializes application subsystems and blocks until an OS shutdown signal is received.
func (app *Application) Start() error {
	app.Logger.Info("Starting application...")

	// Start Database Manager
	if err := app.Database.Start(); err != nil {
		app.Logger.Error("Failed to start Database manager.")
		return fmt.Errorf("failed to start database manager: %w", err)
	}

	// Initialize Discord Manager
	if err := app.Discord.Initialize(); err != nil {
		app.Logger.Error("Failed to initialize Discord manager.")
		return fmt.Errorf("failed to initialize Discord manager: %w", err)
	}

	// The engine bridge: the Minecraft side of games (Ranked-Bedwars-Engine) connects here.
	if addr := os.Getenv("ENGINE_BRIDGE_ADDR"); addr != "" {
		go func() {
			if err := helpers.Engine.Listen(context.Background(), addr, os.Getenv("ENGINE_BRIDGE_SECRET")); err != nil {
				app.Logger.Error(fmt.Sprintf("Engine bridge stopped: %v", err))
			}
		}()
	} else {
		app.Logger.Info("ENGINE_BRIDGE_ADDR is not set: games are hosted by hand.")
	}

	app.Logger.Info("Application started successfully.")

	// Block until an interrupt or termination signal is received
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sigChan

	app.Shutdown()
	return nil
}

// Shutdown gracefully stops all running application subsystems.
func (app *Application) Shutdown() {
	app.Logger.Info("Shutting down application...")

	if err := app.Discord.Stop(); err != nil {
		app.Logger.Error(fmt.Sprintf("Error stopping Discord manager: %v", err))
	}

	if err := app.Database.Stop(); err != nil {
		app.Logger.Error(fmt.Sprintf("Error stopping Database manager: %v", err))
	}

	app.Logger.Info("Application shutdown complete.")
}
