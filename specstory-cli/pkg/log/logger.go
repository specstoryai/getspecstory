package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// ANSI color codes for terminal output
const (
	// Base ANSI escape sequence
	ANSIEscape = "\033["

	// Color codes
	ColorReset       = ANSIEscape + "0m"
	ColorBold        = ANSIEscape + "1m"
	ColorRed         = ANSIEscape + "31m"
	ColorGreen       = ANSIEscape + "32m"
	ColorYellow      = ANSIEscape + "33m"
	ColorBlue        = ANSIEscape + "34m"
	ColorMagenta     = ANSIEscape + "35m"
	ColorCyan        = ANSIEscape + "36m"
	ColorBrightRed   = ANSIEscape + "91m"
	ColorBrightGreen = ANSIEscape + "92m"
	ColorOrange      = ANSIEscape + "38;5;208m"

	// Combined styles
	ColorBoldGreen = ANSIEscape + "1;32m"
	ColorBoldCyan  = ANSIEscape + "1;36m"
)

// Logger configuration
var (
	logger        *slog.Logger
	logFileHandle *os.File
)

// UserMessage prints a plain message to stderr without any prefix or color
// This respects the silent flag - no output if silent mode is enabled
func UserMessage(format string, args ...interface{}) {
	if !isSilent() {
		message := fmt.Sprintf(format, args...)
		fmt.Fprint(os.Stderr, message)
	}
}

// UserWarn prints a warning message to stderr with orange color
// This respects the silent flag - no output if silent mode is enabled
func UserWarn(format string, args ...interface{}) {
	if !isSilent() {
		message := fmt.Sprintf(format, args...)
		fmt.Fprintf(os.Stderr, "\n%sWarning: %s\n%s", ColorOrange, message, ColorReset)
	}
}

// UserError prints an error message to stderr with bright red color
// This respects the silent flag - no output if silent mode is enabled
func UserError(format string, args ...interface{}) {
	if !isSilent() {
		message := fmt.Sprintf(format, args...)
		fmt.Fprintf(os.Stderr, "\n%sError: %s\n%s", ColorBrightRed, message, ColorReset)
	}
}

// SetupLogger configures slog based on flags
// console: enables logging to stdout
// logFile: enables logging to file
// logPath: path to the log file (required if logFile is true)
// debug: changes log level to Debug (only valid with console or logFile)
func SetupLogger(console, logFile, debug bool, logPath string) error {
	var handlers []slog.Handler

	// Determine log level
	logLevel := slog.LevelInfo
	if debug {
		logLevel = slog.LevelDebug
	}

	// Add file handler if --log flag is set
	if logFile {
		// Create directory if it doesn't exist
		dir := filepath.Dir(logPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create log directory: %v", err)
		}

		// Open log file for append
		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file: %v", err)
		}
		logFileHandle = file

		handlers = append(handlers, slog.NewTextHandler(file, &slog.HandlerOptions{
			Level: logLevel,
		}))
	}

	// Add stdout handler if --console flag is set
	if console {
		handlers = append(handlers, slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: logLevel,
		}))
	}

	// Create logger based on handlers
	switch len(handlers) {
	case 0:
		// No logging - discard everything
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	case 1:
		// Single handler
		logger = slog.New(handlers[0])
	default:
		// Multiple handlers
		logger = slog.New(&multiHandler{handlers: handlers})
	}

	// Set as default logger
	slog.SetDefault(logger)
	return nil
}

// CloseLogger closes the log file if open
func CloseLogger() {
	if logFileHandle != nil {
		_ = logFileHandle.Close()
		logFileHandle = nil
	}
}

// multiHandler allows writing to multiple destinations
type multiHandler struct {
	handlers []slog.Handler
}

func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, handler := range h.handlers {
		if err := handler.Handle(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithAttrs(attrs)
	}
	return &multiHandler{handlers: newHandlers}
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithGroup(name)
	}
	return &multiHandler{handlers: newHandlers}
}

// isSilent checks if silent mode is enabled
// This is set during logger setup
var silentMode bool

func isSilent() bool {
	return silentMode
}

// SetSilent sets the silent mode flag
func SetSilent(silent bool) {
	silentMode = silent
}
