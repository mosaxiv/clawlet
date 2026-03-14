package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mosaxiv/clawlet/llm"
	"github.com/urfave/cli/v3"
)

func cmdStats() *cli.Command {
	return &cli.Command{
		Name:  "stats",
		Usage: "print persisted statistics",
		Commands: []*cli.Command{
			{
				Name:  "tokens",
				Usage: "print token usage (per day)",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "date", Usage: "YYYY-MM-DD (default: all dates)"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					date := strings.TrimSpace(cmd.String("date"))
					store := llm.DefaultTokenUsageStore()
					all := store.All()
					if len(all) == 0 {
						fmt.Println("no token usage recorded")
						return nil
					}

					keys := make([]string, 0, len(all))
					for k := range all {
						if date != "" && k != date {
							continue
						}
						keys = append(keys, k)
					}
					sort.Strings(keys)
					if len(keys) == 0 {
						fmt.Printf("no token usage recorded for %s\n", date)
						return nil
					}

					fmt.Printf("date       calls prompt completion total\n")
					for _, d := range keys {
						u := all[d]
						fmt.Printf("%s %5d %6d %10d %5d\n", d, u.Calls, u.PromptTokens, u.CompletionTokens, u.TotalTokens)
					}
					return nil
				},
			},
		},
	}
}
