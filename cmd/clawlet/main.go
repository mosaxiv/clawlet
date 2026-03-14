package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/mosaxiv/clawlet/paths"
	"github.com/urfave/cli/v3"
)

func main() {
	// Get config directory for log file
	configDir, err := paths.ConfigDir()
	if err != nil {
		log.Fatal("Failed to get config directory:", err)
	}
	logPath := filepath.Join(configDir, "clawlet.log")

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal("Failed to open log file:", err)
	}
	defer file.Close()
	log.SetOutput(file)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	root := &cli.Command{
		Name:    "clawlet",
		Usage:   "minimal Go agent",
		Version: resolveVersion(),
		Commands: []*cli.Command{
			cmdVersion(),
			cmdOnboard(),
			cmdStatus(),
			cmdAgent(),
			cmdGateway(),
			cmdProvider(),
			cmdChannels(),
			cmdCron(),
			cmdStats(),
			cmdSessions(),
		},
	}

	if err := root.Run(context.Background(), os.Args); err != nil {
		cli.HandleExitCoder(err)
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
