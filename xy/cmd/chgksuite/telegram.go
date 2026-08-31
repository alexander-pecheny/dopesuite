package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/stats"
	"xy/internal/chgk/tg"
)

// composeTelegram is `chgksuite compose telegram`: post a package to a channel,
// question by question, with its comments in the linked discussion group.
func composeTelegram(args []string) error {
	fs := flag.NewFlagSet("compose telegram", flag.ContinueOnError)
	channel := fs.String("tgchannel", "", "channel to post to: an id, a t.me link or @username")
	chat := fs.String("tgchat", "", "the discussion group linked to that channel")
	dryRun := fs.Bool("dry_run", false, "print what would be posted, post nothing")
	noSpoilers := fs.Bool("nospoilers", false, "print the answers openly")
	disableAsterisks := fs.Int("disable_asterisks_processing", 0, "leave * alone (non-zero)")
	skipUntil := fs.Int("skip_until", 0, "start at question N")
	addPolls := fs.Bool("add_polls", false, "post a poll after each question, tour and the package")
	pollConfig := fs.String("poll_config", "", "poll config TOML (required with --add_polls)")
	token := fs.String("token", "", "bot token; defaults to $CHGKSUITE_TG_TOKEN")
	stopIfNoStats := fs.Bool("stop_if_no_stats", false, "refuse to publish a package whose questions carry no «Взятия:»")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("compose telegram takes exactly one .4s file")
	}
	in := fs.Arg(0)

	opts := tg.Options{
		NoSpoilers:       *noSpoilers,
		DisableAsterisks: *disableAsterisks != 0,
		SkipUntil:        *skipUntil,
	}
	var polls *tg.PollConfig
	if *addPolls {
		var err error
		if *pollConfig == "" {
			if polls, err = tg.DefaultPollConfig(); err != nil {
				return err
			}
		} else {
			raw, readErr := os.ReadFile(*pollConfig)
			if readErr != nil {
				return readErr
			}
			if polls, err = tg.ParsePollConfig(string(raw)); err != nil {
				return fmt.Errorf("%s: %w", *pollConfig, err)
			}
		}
	}

	src, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	if game := gameOf(in); game != "chgk" {
		return fmt.Errorf("the telegram export is ported for chgk only, not %s", game)
	}
	doc := fsource.Parse(string(src), "chgk")
	if *stopIfNoStats && !stats.HasStats(doc) {
		return fmt.Errorf("don't publish questions without stats")
	}
	images, err := loadImages(doc, filepath.Dir(in))
	if err != nil {
		return err
	}

	ctx := context.Background()
	req := tg.Request{Doc: doc, Images: images, Options: opts, Polls: polls}
	if *dryRun {
		// Whatever ids were given, so the links in the navigation post look like
		// the ones a real run would write; placeholders when they were names.
		req.Target = tg.DryRunTarget(*channel, *chat)
		return tg.Export(ctx, tg.NewDryRunPoster(), req)
	}

	botToken := strings.TrimSpace(*token)
	if botToken == "" {
		botToken = strings.TrimSpace(os.Getenv("CHGKSUITE_TG_TOKEN"))
	}
	if botToken == "" {
		return fmt.Errorf("no bot token: pass --token or set CHGKSUITE_TG_TOKEN")
	}
	bot := tg.NewBot(botToken)
	stop, err := bot.Start(ctx)
	if err != nil {
		return err
	}
	defer stop()

	say := func(format string, a ...any) { fmt.Fprintf(os.Stderr, "\n"+format+"\n", a...) }
	target, err := tg.ResolveTarget(ctx, bot, *channel, *chat, say)
	if err != nil {
		return err
	}
	poster, err := tg.NewPoster(bot, target)
	if err != nil {
		return err
	}
	req.Target = target
	fmt.Printf("Posting to %s (comments in %s)\n", target.ChannelID, target.ChatID)
	start := time.Now()
	if err := tg.Export(ctx, poster, req); err != nil {
		return err
	}
	fmt.Printf("Done in %s\n", time.Since(start).Round(time.Second))
	return nil
}
