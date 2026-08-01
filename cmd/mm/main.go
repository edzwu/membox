package main

import (
	"context"
	"os"
	"os/signal"

	"golang.org/x/term"

	"membox/internal/infrastructure/platform"
	"membox/internal/interfaces/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	isTTY := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, platform.Launcher{}, isTTY))
}
