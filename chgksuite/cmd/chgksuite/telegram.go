package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/fsource"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/stats"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/tg"
)

// errNoStats is --stop_if_no_stats refusing a packet. chgksuite-app answers it
// by asking whether to publish anyway, and runs again with the check off.
var errNoStats = errors.New("don't publish questions without stats")

// composeTelegram is `chgksuite compose telegram`: post a packet to a channel,
// question by question, with its comments in the linked discussion group.
func composeTelegram(args []string) error {
	fs := newFlagSet("compose telegram")
	channel := fs.String("tgchannel", "", "channel to post to: an id, a t.me link or @username")
	dryRun := fs.Bool("dry_run", false, "print what would be posted, post nothing")
	noSpoilers := fs.Bool("nospoilers", false, "print the answers openly")
	disableAsterisks := fs.Bool("disable_asterisks_processing", false, "leave * alone")
	skipUntil := fs.Int("skip_until", 0, "start at question N")
	commentFirst := commentFirstFlag(fs)
	addPolls := fs.Bool("add_polls", false, "post a poll after each question, tour and the packet")
	pollConfig := fs.String("poll_config", "", "poll config TOML (required with --add_polls)")
	account := fs.String("tgaccount", override("tgaccount", defaultAccount), "which of the bot tokens saved in ~/.chgksuite/telegram.toml to post with")
	token := fs.String("token", "", "bot token, saved in ~/.chgksuite/telegram.toml when the account has none yet; empty is $CHGKSUITE_TG_TOKEN or the saved one")
	stopIfNoStats := fs.Bool("stop_if_no_stats", setting("stop_if_no_stats") == "true", "refuse to publish a package whose questions carry no stats line")
	language := languageFlag(fs)
	config := configFlag(fs)
	if err := parseConfigured(fs, args, *config); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("compose telegram takes exactly one .4s file")
	}
	in := fs.Arg(0)

	lang, labelsFile, langErr := language()
	if langErr != nil {
		return langErr
	}
	opts := tg.Options{
		Language:         lang,
		LabelsFile:       labelsFile,
		NoSpoilers:       *noSpoilers,
		DisableAsterisks: *disableAsterisks,
		SkipUntil:        *skipUntil,
		CommentFirst:     *commentFirst,
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
		return errNoStats
	}
	images, err := loadImages(doc, filepath.Dir(in))
	if err != nil {
		return err
	}

	ctx := context.Background()
	req := tg.Request{Doc: doc, Images: images, Options: opts, Polls: polls}
	if *dryRun {
		// The id given, so the links in the navigation post look like the ones a
		// real run would write; placeholders otherwise.
		req.Target = tg.DryRunTarget(*channel)
		return tg.Export(ctx, tg.NewDryRunPoster(), req)
	}

	tok, err := botToken(*token, *account)
	if err != nil {
		return err
	}
	bot := tg.NewBot(tok)
	stop, err := bot.Start(ctx)
	if err != nil {
		return err
	}
	defer stop()

	target, err := tg.ResolveTarget(ctx, bot, *channel)
	if err != nil {
		return err
	}
	poster, err := tg.NewPoster(bot, target)
	if err != nil {
		return err
	}
	req.Target = target
	reportNote("%s", fmt.Sprintf("posting to %s, comments in %s", target.ChannelID, target.ChatID))
	start := time.Now()
	if err := tg.Export(ctx, poster, req); err != nil {
		return err
	}
	reportDone("%s", fmt.Sprintf("done in %s", time.Since(start).Round(time.Second).String()))
	return nil
}
