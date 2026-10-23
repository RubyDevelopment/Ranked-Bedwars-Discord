package utility

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// ============================================================================
// Interfaces & Types
// ============================================================================

// AppLogger defines the standardized API interface for structured logging.
type AppLogger interface {
	Info(msg string, fields ...zap.Field)
	Warn(msg string, fields ...zap.Field)
	Error(msg string, fields ...zap.Field)
	Debug(msg string, fields ...zap.Field)
	Child(label string) AppLogger
}

// GameLogger provides separate diagnostic channels for dynamic match instances.
type GameLogger struct {
	Discord   AppLogger
	Minecraft AppLogger
}

// zapLogger implements the AppLogger interface using Zap's Logger.
type zapLogger struct {
	sugar *zap.Logger
}

func (l *zapLogger) Info(msg string, fields ...zap.Field)  { l.sugar.Info(msg, fields...) }
func (l *zapLogger) Warn(msg string, fields ...zap.Field)  { l.sugar.Warn(msg, fields...) }
func (l *zapLogger) Error(msg string, fields ...zap.Field) { l.sugar.Error(msg, fields...) }
func (l *zapLogger) Debug(msg string, fields ...zap.Field) { l.sugar.Debug(msg, fields...) }

// Child returns a new AppLogger instance tagged with a specific module label.
func (l *zapLogger) Child(label string) AppLogger {
	return &zapLogger{sugar: l.sugar.With(zap.String("moduleLabel", label))}
}

// ============================================================================
// Global State & Pre-configured Loggers
// ============================================================================

var (
	logDir          string
	mu              sync.RWMutex
	dynamicRotators = make(map[string]*lumberjack.Logger)
	logLevel        = zapcore.DebugLevel
	bufPool         = buffer.NewPool()

	// Global instances available application-wide.
	GlobalLogger      AppLogger
	DiscordLogger     AppLogger
	DatabaseLogger    AppLogger
	DatabaseAPILogger AppLogger
	EngineLogger      AppLogger
	Logger            AppLogger
)

// ============================================================================
// Initialization & Core Building
// ============================================================================

func init() {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	logDir = filepath.Join(cwd, "logs")

	logLevelStr := os.Getenv("LOG_LEVEL")
	if logLevelStr == "" {
		logLevelStr = "debug"
	}

	var level zapcore.Level
	if err := level.UnmarshalText([]byte(strings.ToLower(logLevelStr))); err != nil {
		level = zapcore.DebugLevel
	}
	logLevel = level

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = ""
	encoderConfig.LevelKey = ""

	customLineEncoder := func(isConsole bool) zapcore.Encoder {
		baseEncoder := zapcore.NewConsoleEncoder(encoderConfig)
		return &textLineEncoder{Encoder: baseEncoder, isConsole: isConsole}
	}

	globalRotate := &lumberjack.Logger{
		Filename:   filepath.Join(logDir, "global", "global.log"),
		MaxSize:    20,
		MaxBackups: 14,
		LocalTime:  true,
		Compress:   true,
	}
	discordRotate := &lumberjack.Logger{
		Filename:   filepath.Join(logDir, "discord", "discord-core.log"),
		MaxSize:    20,
		MaxBackups: 14,
		LocalTime:  true,
		Compress:   true,
	}
	engineRotate := &lumberjack.Logger{
		Filename:   filepath.Join(logDir, "engine", "engine-core.log"),
		MaxSize:    20,
		MaxBackups: 14,
		LocalTime:  true,
		Compress:   true,
	}
	databaseAPIRotate := &lumberjack.Logger{
		Filename:   filepath.Join(logDir, "database", "database-api.log"),
		MaxSize:    20,
		MaxBackups: 14,
		LocalTime:  true,
		Compress:   true,
	}

	GlobalLogger = &zapLogger{sugar: newChannelLogger(buildChannelCore(level, customLineEncoder, globalRotate, nil), "global", "Global")}
	DiscordLogger = &zapLogger{sugar: newChannelLogger(buildChannelCore(level, customLineEncoder, globalRotate, discordRotate), "discord", "DiscordCore")}
	DatabaseLogger = &zapLogger{sugar: newChannelLogger(buildChannelCore(level, customLineEncoder, globalRotate, nil), "database", "DatabaseCore")}
	DatabaseAPILogger = &zapLogger{sugar: newChannelLogger(buildFileOnlyCore(level, customLineEncoder, globalRotate, databaseAPIRotate), "database", "DatabaseAPI")}
	EngineLogger = &zapLogger{sugar: newChannelLogger(buildChannelCore(level, customLineEncoder, globalRotate, engineRotate), "engine", "MatchEngine")}

	// Default alias to EngineLogger
	Logger = EngineLogger
}

// buildChannelCore tees each log entry to the console (stderr, like the Database service, so the panel shows it) and global.log, plus an optional channel-specific file.
func buildChannelCore(
	level zapcore.Level,
	encode func(bool) zapcore.Encoder,
	globalRotate *lumberjack.Logger,
	channelRotate *lumberjack.Logger,
) zapcore.Core {
	cores := []zapcore.Core{
		zapcore.NewCore(encode(true), zapcore.Lock(os.Stderr), level),
		zapcore.NewCore(encode(false), zapcore.AddSync(globalRotate), level),
	}
	if channelRotate != nil {
		cores = append(cores, zapcore.NewCore(encode(false), zapcore.AddSync(channelRotate), level))
	}
	return zapcore.NewTee(cores...)
}

// buildFileOnlyCore tees each log entry to global.log only (no console output).
func buildFileOnlyCore(
	level zapcore.Level,
	encode func(bool) zapcore.Encoder,
	globalRotate *lumberjack.Logger,
	channelRotate *lumberjack.Logger,
) zapcore.Core {
	cores := []zapcore.Core{
		zapcore.NewCore(encode(false), zapcore.AddSync(globalRotate), level),
	}
	if channelRotate != nil {
		cores = append(cores, zapcore.NewCore(encode(false), zapcore.AddSync(channelRotate), level))
	}
	return zapcore.NewTee(cores...)
}

func newChannelLogger(core zapcore.Core, contextType, moduleLabel string) *zap.Logger {
	return zap.New(core).With(
		zap.String("contextType", contextType),
		zap.String("moduleLabel", moduleLabel),
	)
}

// ============================================================================
// Custom Encoder
// ============================================================================

// textLineEncoder wraps Zap's Encoder to format custom console and file log strings.
type textLineEncoder struct {
	zapcore.Encoder
	isConsole   bool
	contextType string
	moduleLabel string
}

func (t *textLineEncoder) AddString(key, value string) {
	switch key {
	case "contextType":
		t.contextType = strings.ToUpper(value)
	case "moduleLabel":
		t.moduleLabel = value
	}
	t.Encoder.AddString(key, value)
}

func (t *textLineEncoder) EncodeEntry(entry zapcore.Entry, fields []zapcore.Field) (*buffer.Buffer, error) {
	buf := bufPool.Get()

	// 1. Time string
	buf.WriteString(entry.Time.Format("2006-01-02 15:04:05 "))

	// 2. Log level (ANSI color formatted if output is console)
	lvlStr := entry.Level.String()
	if t.isConsole {
		switch entry.Level {
		case zapcore.DebugLevel:
			buf.WriteString("\033[36m[" + lvlStr + "]\033[0m ")
		case zapcore.InfoLevel:
			buf.WriteString("\033[32m[" + lvlStr + "]\033[0m ")
		case zapcore.WarnLevel:
			buf.WriteString("\033[33m[" + lvlStr + "]\033[0m ")
		case zapcore.ErrorLevel:
			buf.WriteString("\033[31m[" + lvlStr + "]\033[0m ")
		default:
			buf.WriteString("[" + lvlStr + "] ")
		}
	} else {
		buf.WriteString("[" + lvlStr + "] ")
	}

	// 3. Extract Context and Module Label overrides from inline fields
	contextType := t.contextType
	if contextType == "" {
		contextType = "GLOBAL"
	}
	moduleLabel := t.moduleLabel
	for _, f := range fields {
		if f.Key == "contextType" {
			contextType = strings.ToUpper(fieldValue(f))
		}
		if f.Key == "moduleLabel" {
			moduleLabel = fieldValue(f)
		}
	}

	// 4. Output channel context & module label
	buf.WriteString("(" + contextType + ") ")
	if moduleLabel != "" {
		buf.WriteString("[" + moduleLabel + "]: ")
	}

	// 5. Output message body
	buf.WriteString(entry.Message)

	// 6. Remaining fields as key=value
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range fields {
		if f.Key == "contextType" || f.Key == "moduleLabel" {
			continue
		}
		f.AddTo(enc)
	}
	keys := make([]string, 0, len(enc.Fields))
	for k := range enc.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(buf, " %s=%v", k, enc.Fields[k])
	}
	buf.WriteString("\n")

	return buf, nil
}

func (t *textLineEncoder) Clone() zapcore.Encoder {
	return &textLineEncoder{
		Encoder:     t.Encoder.Clone(),
		isConsole:   t.isConsole,
		contextType: t.contextType,
		moduleLabel: t.moduleLabel,
	}
}

// fieldValue converts a zapcore.Field to a string representation for custom string extraction.
func fieldValue(f zapcore.Field) string {
	if f.Type == zapcore.StringType {
		return f.String
	}
	if f.Interface != nil {
		return fmt.Sprintf("%v", f.Interface)
	}
	return ""
}

// ============================================================================
// Factory Functions & Dynamic Logger Creation
// ============================================================================

// CreateModuleLogger returns a child logger assigned under the given channel root.
func CreateModuleLogger(module string, channel string) AppLogger {
	var root AppLogger
	switch strings.ToLower(channel) {
	case "discord":
		root = DiscordLogger
	case "database":
		root = DatabaseLogger
	case "engine":
		root = EngineLogger
	case "global":
		root = GlobalLogger
	default:
		root = EngineLogger
	}
	return root.Child(module)
}

// CreateEngineGameLogger creates per-game rotating loggers for discord and minecraft channels.
func CreateEngineGameLogger(guildID string, gameID string, gameNumber any) GameLogger {
	logKey := sanitizePathPart(fmt.Sprintf("%v", gameNumber))
	if gameNumber == nil {
		logKey = sanitizePathPart(gameID)
	}
	guildID = sanitizePathPart(guildID)

	mu.Lock()
	defer mu.Unlock()

	minecraftPath := filepath.Join(logDir, "guilds", guildID, "games", fmt.Sprintf("minecraft-%s.log", logKey))
	discordPath := filepath.Join(logDir, "guilds", guildID, "discord", fmt.Sprintf("discord-%s.log", logKey))

	ctxDiscord := fmt.Sprintf("guild:%s:discord", guildID)
	ctxMinecraft := fmt.Sprintf("game:%s:%s:minecraft", guildID, gameID)

	mcRotator := getOrCreateRotator(minecraftPath)
	dcRotator := getOrCreateRotator(discordPath)

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = ""
	encoderConfig.LevelKey = ""

	customLineEncoder := func(isConsole bool) zapcore.Encoder {
		return &textLineEncoder{Encoder: zapcore.NewConsoleEncoder(encoderConfig), isConsole: isConsole}
	}

	mcCore := zapcore.NewCore(customLineEncoder(false), zapcore.AddSync(mcRotator), logLevel)
	dcCore := zapcore.NewCore(customLineEncoder(false), zapcore.AddSync(dcRotator), logLevel)

	mcZap := zap.New(mcCore).With(
		zap.String("contextType", ctxMinecraft),
		zap.String("moduleLabel", fmt.Sprintf("Game#%s/Minecraft", logKey)),
	)
	dcZap := zap.New(dcCore).With(
		zap.String("contextType", ctxDiscord),
		zap.String("moduleLabel", fmt.Sprintf("Game#%s/Discord", logKey)),
	)

	gl := GameLogger{
		Minecraft: &zapLogger{sugar: mcZap},
		Discord:   &zapLogger{sugar: dcZap},
	}

	gl.Discord.Info(fmt.Sprintf("Logger created for game #%s | guild=%s | channel=discord | path=%s", logKey, guildID, discordPath))
	gl.Minecraft.Info(fmt.Sprintf("Logger created for game #%s | guild=%s | channel=minecraft | path=%s", logKey, guildID, minecraftPath))

	return gl
}

// ClearEngineGameLogger closes and removes active file rotators for a finished game.
func ClearEngineGameLogger(guildID string, gameID string, gameNumber any) {
	logKey := sanitizePathPart(fmt.Sprintf("%v", gameNumber))
	if gameNumber == nil {
		logKey = sanitizePathPart(gameID)
	}
	guildID = sanitizePathPart(guildID)

	mu.Lock()
	defer mu.Unlock()

	minecraftPath := filepath.Join(logDir, "guilds", guildID, "games", fmt.Sprintf("minecraft-%s.log", logKey))
	discordPath := filepath.Join(logDir, "guilds", guildID, "discord", fmt.Sprintf("discord-%s.log", logKey))
	for _, path := range []string{minecraftPath, discordPath} {
		if rotator, exists := dynamicRotators[path]; exists {
			rotator.Close()
			delete(dynamicRotators, path)
		}
	}
}

// sanitizePathPart keeps [A-Za-z0-9._-], replaces everything else with '_',
// and neutralizes empty/dot-only values so it can never traverse directories.
func sanitizePathPart(v string) string {
	b := []byte(v)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			b[i] = '_'
		}
	}
	out := string(b)
	if out == "" || strings.Contains(out, "..") {
		return "_"
	}
	return out
}

// getOrCreateRotator manages (caller must hold mu) global state cache for Lumberjack dynamic log rotators.
func getOrCreateRotator(path string) *lumberjack.Logger {
	if r, exists := dynamicRotators[path]; exists {
		return r
	}
	r := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    10,
		MaxBackups: 7,
		LocalTime:  true,
		Compress:   true,
	}
	dynamicRotators[path] = r
	return r
}
