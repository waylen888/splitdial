package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

// expandHomePath expands ~ to the user's home directory.
func expandHomePath(path string) string {
	if len(path) == 0 || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// Config holds logging configuration.
type Config struct {
	// Level is the minimum log level (debug, info, warn, error)
	Level string
	// Format is the log format (json, text)
	Format string
	// Output is the output destination (stdout, stderr, or file path)
	Output string
	// FileConfig is used when Output is a file path
	FileConfig *FileConfig
}

// FileConfig holds file rotation configuration.
type FileConfig struct {
	// MaxSize is the maximum size in megabytes before rotation
	MaxSize int
	// MaxBackups is the maximum number of old log files to retain
	MaxBackups int
	// MaxAge is the maximum number of days to retain old log files
	MaxAge int
	// Compress determines if rotated files should be compressed
	Compress bool
}

// DefaultConfig returns default logging configuration.
func DefaultConfig() *Config {
	return &Config{
		Level:  "info",
		Format: "text",
		Output: "stdout",
		FileConfig: &FileConfig{
			MaxSize:    100, // 100 MB
			MaxBackups: 3,
			MaxAge:     28, // 28 days
			Compress:   true,
		},
	}
}

// Logger is a wrapper around slog.Logger with additional utilities.
type Logger struct {
	*slog.Logger
}

var defaultLogger *Logger
var levelVar = new(slog.LevelVar)

// SetLevel changes the log level dynamically.
func SetLevel(levelStr string) {
	var level slog.Level
	switch levelStr {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	levelVar.Set(level)
}

// Init initializes the global logger with the given configuration.
func Init(cfg *Config) error {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	// Parse log level
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	// Set the dynamic level
	levelVar.Set(level)

	// Setup output writer
	var writer io.Writer
	switch cfg.Output {
	case "stdout":
		writer = os.Stdout
	case "stderr":
		writer = os.Stderr
	default:
		// Assume it's a file path - expand ~ to home directory
		outputPath := expandHomePath(cfg.Output)
		if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
			return err
		}

		fc := cfg.FileConfig
		if fc == nil {
			fc = DefaultConfig().FileConfig
		}

		writer = &lumberjack.Logger{
			Filename:   outputPath,
			MaxSize:    fc.MaxSize,
			MaxBackups: fc.MaxBackups,
			MaxAge:     fc.MaxAge,
			Compress:   fc.Compress,
		}
	}

	// Create handler based on format
	opts := &slog.HandlerOptions{
		Level: levelVar,
	}

	var handler slog.Handler
	switch cfg.Format {
	case "json":
		handler = slog.NewJSONHandler(writer, opts)
	default:
		handler = slog.NewTextHandler(writer, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	defaultLogger = &Logger{Logger: logger}

	return nil
}

// Get returns the default logger.
func Get() *Logger {
	if defaultLogger == nil {
		// Initialize with defaults if not yet initialized
		Init(nil)
	}
	return defaultLogger
}

// Debug logs a debug message.
func Debug(msg string, args ...any) {
	Get().Debug(msg, args...)
}

// Info logs an info message.
func Info(msg string, args ...any) {
	Get().Info(msg, args...)
}

// Warn logs a warning message.
func Warn(msg string, args ...any) {
	Get().Warn(msg, args...)
}

// Error logs an error message.
func Error(msg string, args ...any) {
	Get().Error(msg, args...)
}

// With returns a new logger with the given attributes.
func With(args ...any) *Logger {
	return &Logger{Logger: Get().Logger.With(args...)}
}

// WithContext returns a logger from context or the default logger.
func WithContext(ctx context.Context) *Logger {
	// Future: could extract logger from context
	return Get()
}
