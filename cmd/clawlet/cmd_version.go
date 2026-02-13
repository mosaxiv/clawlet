package main

import (
	"context"
	"runtime/debug"
	"strings"

	"github.com/urfave/cli/v3"
)

var (
	version       = "dev"
	readBuildInfo = debug.ReadBuildInfo
)

func cmdVersion() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "print version (same as --version)",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cli.ShowVersion(cmd.Root())
			return nil
		},
	}
}

func resolveVersion() string {
	v := strings.TrimSpace(version)
	if v != "" && v != "dev" {
		return v
	}

	if bi, ok := readBuildInfo(); ok {
		mv := strings.TrimSpace(bi.Main.Version)
		if mv != "" && mv != "(devel)" {
			return mv
		}
	}

	return "dev"
}
