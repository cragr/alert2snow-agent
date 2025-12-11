// Package logging provides structured JSON logging utilities.
package logging

import (
	"log/slog"
	"os"
)

// NewLogger creates a new structured JSON logger for the application.
func NewLogger() *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	return slog.New(handler)
}

// WithComponent returns a logger with a component field for categorizing log messages.
func WithComponent(logger *slog.Logger, component string) *slog.Logger {
	return logger.With("component", component)
}
