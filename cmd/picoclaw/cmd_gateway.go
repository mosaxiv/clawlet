package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"

	"github.com/mosaxiv/picoclaw/agent"
	"github.com/mosaxiv/picoclaw/bus"
	"github.com/mosaxiv/picoclaw/channels"
	"github.com/mosaxiv/picoclaw/channels/discord"
	"github.com/mosaxiv/picoclaw/channels/slack"
	"github.com/mosaxiv/picoclaw/cron"
	"github.com/mosaxiv/picoclaw/heartbeat"
	"github.com/mosaxiv/picoclaw/paths"
	"github.com/mosaxiv/picoclaw/session"
	"github.com/urfave/cli/v3"
)

func cmdGateway() *cli.Command {
	return &cli.Command{
		Name:  "gateway",
		Usage: "run the long-lived agent gateway (channels + cron + heartbeat)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "listen", Usage: "HTTP listen address for Slack Events API (default from config.gateway.listen)"},
			&cli.StringFlag{Name: "workspace", Usage: "workspace directory (default: ~/.picoclaw/workspace or PICOCLAW_WORKSPACE)"},
			&cli.IntFlag{Name: "max-iters", Value: 20, Usage: "max tool-call iterations"},
			&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "verbose"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, _, err := loadConfig()
			if err != nil {
				return err
			}

			wsAbs, err := resolveWorkspace(cmd.String("workspace"))
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
			defer stop()

			b := bus.New(256)
			smgr := session.NewManager(paths.SessionsDir())

			var cronSvc *cron.Service
			if cfg.Cron.EnabledValue() {
				cronSvc = cron.NewService(paths.CronStorePath(), func(ctx context.Context, job cron.Job) (string, error) {
					if job.Payload.Kind != "" && job.Payload.Kind != "agent_turn" {
						return "", nil
					}
					ch := job.Payload.Channel
					to := job.Payload.To
					if !job.Payload.Deliver || strings.TrimSpace(ch) == "" || strings.TrimSpace(to) == "" {
						return "", nil
					}
					_ = b.PublishInbound(ctx, bus.InboundMessage{
						Channel:    ch,
						SenderID:   "cron:" + job.ID,
						ChatID:     to,
						Content:    job.Payload.Message,
						SessionKey: ch + ":" + to,
					})
					return "", nil
				})
			}

			loop, err := agent.NewLoop(agent.LoopOptions{
				Config:       cfg,
				WorkspaceDir: wsAbs,
				Model:        cfg.LLM.Model,
				MaxIters:     cmd.Int("max-iters"),
				Bus:          b,
				Sessions:     smgr,
				Cron:         cronSvc,
				Spawn:        nil,
				Verbose:      cmd.Bool("verbose"),
			})
			if err != nil {
				return err
			}

			sa := agent.NewSubagentManager(loop)
			loop.SetSpawn(sa.Spawn)

			if cronSvc != nil {
				if err := cronSvc.Start(ctx); err != nil {
					return err
				}
			}

			hb := heartbeat.New(wsAbs, heartbeat.Options{
				Enabled:     cfg.Heartbeat.EnabledValue(),
				IntervalSec: cfg.Heartbeat.IntervalSec,
				OnHeartbeat: func(ctx context.Context, prompt string) (string, error) {
					return loop.ProcessDirect(ctx, prompt, "heartbeat", "cli", "heartbeat")
				},
			})
			hb.Start(ctx)

			cm := channels.NewManager(b)
			if cfg.Channels.Discord.Enabled {
				cm.Add(discord.New(cfg.Channels.Discord, b))
			}
			var sl *slack.Channel
			if cfg.Channels.Slack.Enabled {
				if strings.TrimSpace(cfg.Channels.Slack.SigningSecret) == "" {
					return fmt.Errorf("slack enabled but signingSecret is empty")
				}
				sl = slack.New(cfg.Channels.Slack, b)
				cm.Add(sl)
			}

			if err := cm.StartAll(ctx); err != nil {
				return err
			}

			if cfg.Channels.Slack.Enabled {
				addr := cfg.Gateway.Listen
				if strings.TrimSpace(cmd.String("listen")) != "" {
					addr = strings.TrimSpace(cmd.String("listen"))
				}
				slPath := cfg.Channels.Slack.EventsPath
				if slPath == "" {
					slPath = "/slack/events"
				}
				go func() {
					_ = runSlackServer(ctx, addr, slPath, sl)
				}()
			}

			go func() { _ = loop.Run(ctx) }()

			fmt.Printf("gateway running\n- workspace: %s\n- sessions: %s\n", wsAbs, paths.SessionsDir())
			fmt.Println("stop: Ctrl+C")
			<-ctx.Done()

			_ = cm.StopAll()
			if cronSvc != nil {
				cronSvc.Stop()
			}
			hb.Stop()
			return nil
		},
	}
}

func runSlackServer(ctx context.Context, addr, slackPath string, sl *slack.Channel) error {
	mux := http.NewServeMux()
	if sl != nil {
		mux.HandleFunc(slackPath, sl.EventsHandler())
	}

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	return srv.ListenAndServe()
}
