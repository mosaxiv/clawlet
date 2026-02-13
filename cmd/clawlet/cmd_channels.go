package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/channels/whatsapp"
	"github.com/mosaxiv/clawlet/config"
	"github.com/urfave/cli/v3"
)

func cmdChannels() *cli.Command {
	return &cli.Command{
		Name:  "channels",
		Usage: "channel utilities",
		Commands: []*cli.Command{
			{
				Name:  "status",
				Usage: "show configured channel enablement",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, _, err := loadConfig()
					if err != nil {
						return err
					}
					fmt.Printf("discord.enabled=%v\n", cfg.Channels.Discord.Enabled)
					fmt.Printf("slack.enabled=%v\n", cfg.Channels.Slack.Enabled)
					fmt.Printf("telegram.enabled=%v\n", cfg.Channels.Telegram.Enabled)
					fmt.Printf("whatsapp.enabled=%v\n", cfg.Channels.WhatsApp.Enabled)
					return nil
				},
			},
			{
				Name:  "login",
				Usage: "perform channel login flow (currently supports whatsapp)",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "channel",
						Aliases:  []string{"c"},
						Required: true,
						Usage:    "channel name (e.g. whatsapp)",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, _, err := loadConfig()
					if err != nil {
						return err
					}
					channel := strings.ToLower(strings.TrimSpace(cmd.String("channel")))
					switch channel {
					case "whatsapp":
						return runWhatsAppLogin(ctx, cfg.Channels.WhatsApp)
					default:
						return fmt.Errorf("unsupported channel for login: %s", channel)
					}
				},
			},
		},
	}
}

func runWhatsAppLogin(ctx context.Context, cfg config.WhatsAppConfig) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	fmt.Println("starting whatsapp login flow")
	fmt.Println("scan QR code if shown. stop: Ctrl+C")

	ch := whatsapp.New(cfg, bus.New(8))
	err := ch.Start(ctx)
	if err == nil || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
