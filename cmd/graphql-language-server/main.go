package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/skaji/graphql-language-server/internal/ls"
)

func main() {
	level := slog.LevelInfo
	if strings.TrimSpace(os.Getenv("DEBUG")) != "" {
		level = slog.LevelDebug
	}

	output := os.Stderr
	if logPath := strings.TrimSpace(os.Getenv("LOG_FILE")); logPath != "" {
		file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open LOG_FILE %q: %v\n", logPath, err)
		} else {
			output = file
			defer func() {
				if err := file.Close(); err != nil {
					fmt.Fprintf(os.Stderr, "failed to close LOG_FILE %q: %v\n", logPath, err)
				}
			}()
		}
	}

	handler := slog.NewTextHandler(output, &slog.HandlerOptions{
		Level:     level,
		AddSource: true,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key != slog.SourceKey {
				return attr
			}
			source, ok := attr.Value.Any().(*slog.Source)
			if !ok || source == nil {
				return attr
			}
			source.File = filepath.Base(source.File)
			return slog.Attr{Key: slog.SourceKey, Value: slog.AnyValue(source)}
		},
	})
	logger := slog.New(handler).With("pid", os.Getpid())
	slog.SetDefault(logger)

	slog.Debug("debug logging enabled")

	server := ls.New()
	if err := server.RunStdio(); err != nil {
		slog.Error("server failed", "error", err)
	}
}
