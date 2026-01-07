package main

import (
	"log/slog"
	"os"

	"github.com/skaji/graphql-language-server/internal/ls"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	server := ls.New()
	if err := server.RunStdio(); err != nil {
		slog.Error("server failed", "error", err)
	}
}
