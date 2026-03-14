package main

import (
	"context"
	"fmt"

	"github.com/mosaxiv/clawlet/paths"
	"github.com/mosaxiv/clawlet/session"
	"github.com/urfave/cli/v3"
)

func cmdSessions() *cli.Command {
	return &cli.Command{
		Name:  "sessions",
		Usage: "inspect local session state",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "list stored session keys",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					dir := paths.SessionsDir()
					keys, err := session.ListKeys(dir)
					if err != nil {
						return err
					}
					if len(keys) == 0 {
						fmt.Printf("no sessions found in %s\n", dir)
						return nil
					}
					for _, k := range keys {
						fmt.Println(k)
					}
					return nil
				},
			},
		},
	}
}
