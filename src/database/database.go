// Package database holds the process-wide Ranked Bedwars Database API client
// (see github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk). Every command
// and event handler reaches the database through database.Get() or
// database.Must(), then the domain fields on *sdk.Services directly, e.g.
// database.Must().Player.Register(...) or database.Must().Stats.Get(...).
package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
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

	stop     chan struct{}
	stopOnce sync.Once
}

// NewClient creates a database Manager instance.
func NewClient() *Manager {
	return &Manager{Logger: utility.CreateModuleLogger("DATABASE_MANAGER", "Database"), stop: make(chan struct{})}
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
		return fmt.Errorf("%w: database api url is not set in environment variables", errConfig)
	}
	if apiKey == "" {
		m.Logger.Error("Database API key is not set in environment variables. Please set DATABASE_API_KEY.")
		return fmt.Errorf("%w: database api key is not set in environment variables", errConfig)
	}

	timeout, err := requestTimeout()
	if err != nil {
		m.Logger.Error(err.Error())
		return fmt.Errorf("%w: %v", errConfig, err)
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

// Start connects the SDK client and then watches the connection. While the database cannot be reached it keeps trying on
// the RetryDelay schedule instead of giving up; only a configuration error (missing URL or key) ends it.
func (m *Manager) Start() error {
	if err := retryUntil(m.Initialize, m.sleep, func(err error, wait time.Duration) {
		m.Logger.Warn(fmt.Sprintf("Database is unreachable, trying again in %s.", wait))
	}); err != nil {
		return err
	}
	go m.watch()
	return nil
}

// healthCheckInterval is how often a connected client confirms the database still answers.
const healthCheckInterval = 30 * time.Second

// watch checks the database while the bot runs. The client keeps working once the database is back, so reconnecting is
// just waiting (on the RetryDelay schedule) until a health check passes again.
func (m *Manager) watch() {
	check := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), startupHealthTimeout)
		defer cancel()
		return m.Services.HealthContext(ctx)
	}
	for m.sleep(healthCheckInterval) {
		if check() == nil {
			continue
		}
		m.Logger.Error("Lost the connection to the database.")
		err := retryUntil(check, m.sleep, func(err error, wait time.Duration) {
			m.Logger.Warn(fmt.Sprintf("Database is unreachable (%v), trying again in %s.", err, wait))
		})
		if err != nil {
			return
		}
		m.Logger.Info("Reconnected to the database.")
	}
}

// sleep waits d and reports false when the manager was stopped first.
func (m *Manager) sleep(d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-m.stop:
		return false
	}
}

var (
	errConfig  = errors.New("invalid database configuration")
	errStopped = errors.New("database manager stopped")
)

// RetryDelay is how long to wait after the n-th failure in a row: 1 minute for the first five, then 2, 3, 4 and 5
// minutes, and 5 minutes from then on.
func RetryDelay(failures int) time.Duration {
	if failures <= 5 {
		return time.Minute
	}
	return min(time.Duration(failures-4), 5) * time.Minute
}

// retryUntil runs check until it passes, waiting RetryDelay between failures. It stops early with a configuration error
// (no point retrying) or when sleep reports the manager was stopped.
func retryUntil(check func() error, sleep func(time.Duration) bool, onFail func(error, time.Duration)) error {
	for failures := 1; ; failures++ {
		err := check()
		if err == nil || errors.Is(err, errConfig) {
			return err
		}
		wait := RetryDelay(failures)
		onFail(err, wait)
		if !sleep(wait) {
			return errStopped
		}
	}
}

// Stop clears the process-wide SDK instance.
func (m *Manager) Stop() error {
	m.stopOnce.Do(func() { close(m.stop) })
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
