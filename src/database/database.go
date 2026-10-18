// Package database holds the process-wide Ranked Bedwars Database API client
// (see github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk). Every command
// and event handler reaches the database through database.Get() or
// database.Must(), then the domain fields on *sdk.Services directly, e.g.
// database.Must().Player.Register(...) or database.Must().Stats.Get(...).
package database

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"

	"ranked-bedwars/src/utility"
)

var global atomic.Pointer[sdk.Services]

// apiLoggerAdapter routes the SDK's request/response tracing to the file-only
// API logger, so it never spams the CLI.
type apiLoggerAdapter struct{ logger utility.AppLogger }

func (l *apiLoggerAdapter) Debug(msg string) { l.logger.Debug(msg) }
func (l *apiLoggerAdapter) Error(msg string) { l.logger.Error(msg) }

var _ sdk.Logger = (*apiLoggerAdapter)(nil)

// Manager coordinates the lifecycle of the process-wide SDK client.
type Manager struct {
	Services *sdk.Services
	Logger   utility.AppLogger
}

// NewClient creates a database Manager instance.
func NewClient() *Manager {
	return &Manager{Logger: utility.CreateModuleLogger("DATABASE_MANAGER", "Database")}
}

// startupHealthTimeout bounds the health check that gates bot startup.
const startupHealthTimeout = 10 * time.Second

// requestTimeout reads DATABASE_API_TIMEOUT (a Go duration such as "20s"); unset means sdk.DefaultTimeout.
func requestTimeout() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv("DATABASE_API_TIMEOUT"))
	if raw == "" {
		return sdk.DefaultTimeout, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("DATABASE_API_TIMEOUT %q must be a positive duration like 20s", raw)
	}
	return d, nil
}

// Initialize loads DATABASE_API_URL and DATABASE_API_KEY from the environment,
// connects the SDK client, stores it process-wide, and confirms it with a health check.
func (m *Manager) Initialize() error {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("DATABASE_API_URL")), "/")
	apiKey := strings.TrimSpace(os.Getenv("DATABASE_API_KEY"))
	if baseURL == "" {
		m.Logger.Error("Database API URL is not set in environment variables. Please set DATABASE_API_URL.")
		return fmt.Errorf("database api url is not set in environment variables")
	}
	if apiKey == "" {
		m.Logger.Error("Database API key is not set in environment variables. Please set DATABASE_API_KEY.")
		return fmt.Errorf("database api key is not set in environment variables")
	}

	timeout, err := requestTimeout()
	if err != nil {
		m.Logger.Error(err.Error())
		return err
	}
	svc := sdk.NewServicesWithOptions(sdk.Options{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Timeout: timeout,
		Logger:  &apiLoggerAdapter{logger: utility.DatabaseAPILogger},
	})
	ctx, cancel := context.WithTimeout(context.Background(), startupHealthTimeout)
	defer cancel()
	if err := svc.HealthContext(ctx); err != nil {
		m.Logger.Error(fmt.Sprintf("Database API health check failed: %v", err))
		return fmt.Errorf("database api health check failed: %w", err)
	}

	m.Services = svc
	Set(svc)
	m.Logger.Info("Database SDK initialized successfully.")
	return nil
}

// Start begins the database lifecycle by connecting the SDK client.
func (m *Manager) Start() error { return m.Initialize() }

// Stop clears the process-wide SDK instance.
func (m *Manager) Stop() error {
	Set(nil)
	m.Services = nil
	m.Logger.Info("Database SDK shut down successfully.")
	return nil
}

// Instance returns the initialized SDK services or an error if not yet initialized.
func (m *Manager) Instance() (*sdk.Services, error) {
	if m.Services == nil {
		m.Logger.Error("Database SDK is not initialized. Please call Initialize() first.")
		return nil, fmt.Errorf("database sdk is not initialized; call Initialize() first")
	}
	return m.Services, nil
}

// New creates an isolated SDK client instance without assigning it process-wide.
func New(baseURL, apiKey string) *sdk.Services { return sdk.NewServices(baseURL, apiKey) }

// NewWithAPILogger creates an isolated SDK client instance with request tracing.
func NewWithAPILogger(baseURL, apiKey string, logger utility.AppLogger) *sdk.Services {
	return sdk.NewWithLogger(baseURL, apiKey, &apiLoggerAdapter{logger: logger})
}

// Set replaces or clears (nil) the process-wide SDK instance.
func Set(svc *sdk.Services) { global.Store(svc) }

// Get returns the process-wide SDK instance, or nil if not initialized.
func Get() *sdk.Services { return global.Load() }

// Must returns the process-wide SDK instance or panics if it is unavailable.
// Safe in command handlers when RequiresDatabase middleware is enabled.
func Must() *sdk.Services {
	svc := Get()
	if svc == nil {
		panic("database SDK is not initialized; call database.Manager.Initialize first")
	}
	return svc
}
