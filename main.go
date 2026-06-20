package main

import (
	"context"
	"fmt"
	"os"

	"executor/cmd"
	"executor/internal/app"
	"executor/internal/config"
	"executor/internal/system"
)

// main loads configuration and runs the CLI application.
func main() {
	cfg, err := config.Load(os.Args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	application := app.New(cfg, system.OS{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}, os.Stdout, os.Stderr, os.Stdin)

	if err := cmd.ExecuteContext(context.Background(), application, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
