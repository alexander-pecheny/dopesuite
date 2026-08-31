package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"xy/internal/chgk/imghost"
	"xy/internal/chgk/lj"
)

// composeLJ is `chgksuite compose lj`: a package as the LiveJournal posts it
// becomes. Without --login it writes them out as HTML instead of publishing;
// chgksuite always publishes, but a file is what you want to read first, and
// the posting half here has never been run against the live service.
func composeLJ(args []string) error {
	fs := flag.NewFlagSet("compose lj", flag.ContinueOnError)
	noSpoilers := fs.Bool("nospoilers", false, "print the answers openly instead of behind <lj-spoiler>")
	splitTours := fs.Bool("splittours", false, "a post per tour")
	genimp := fs.Bool("genimp", false, "add the «Общие впечатления» post")
	navigation := fs.Bool("navigation", false, "link the tours' posts to each other (needs --login)")
	login := fs.String("login", "", "livejournal user to post as; empty writes the HTML to files instead")
	password := fs.String("password", "", "that user's password; also read from $CHGKSUITE_LJ_PASSWORD")
	community := fs.String("community", "", "post to this community's journal")
	security := fs.String("security", "", "public, friends, or a friend-group mask; empty posts privately")
	clientID := fs.String("imgur_client_id", override("imgur_client_id", ""), "upload pictures as this imgur client instead of chgksuite's")
	addTS := fs.String("add_ts", override("add_ts", "off"), "append a timestamp to the output filename: on|off")
	merge := fs.Bool("merge", false, "export the input files as one package")
	language := languageFlag(fs)
	noBreak := noBreakFlags(fs)
	config := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := applyConfig(fs, *config); err != nil {
		return err
	}
	lang, labelsFile, err := language()
	if err != nil {
		return err
	}
	opts := lj.Options{
		NoSpoilers:         *noSpoilers,
		SplitTours:         *splitTours,
		GeneralImpressions: *genimp,
		Navigation:         *navigation,
		Language:           lang,
		LabelsFile:         labelsFile,
		NoBreak:            noBreak(),
	}
	sources, err := loadSources(fs.Args(), *merge)
	if err != nil {
		return err
	}
	host := imghost.NewImgur(*clientID)

	for _, s := range sources {
		images, err := loadImages(s.doc, s.dir)
		if err != nil {
			return err
		}
		groups, err := lj.Render(s.doc, images, host, opts)
		if err != nil {
			return err
		}
		if *login == "" {
			if err := writeLJ(groups, s.path, *addTS == "on"); err != nil {
				return err
			}
			continue
		}
		pw := *password
		if pw == "" {
			pw = os.Getenv("CHGKSUITE_LJ_PASSWORD")
		}
		if pw == "" {
			return fmt.Errorf("--login without a password: pass --password or set CHGKSUITE_LJ_PASSWORD")
		}
		client := lj.NewClient(lj.Account{Login: *login, Password: pw, Community: *community, Security: *security})
		if err := publishLJ(client, groups, opts.Navigation); err != nil {
			return err
		}
	}
	return nil
}

// writeLJ is what happens without --login: one .html per post group, so the
// posts can be read (or pasted) before anything is published.
func writeLJ(groups [][]lj.Post, path string, addTS bool) error {
	for i, posts := range groups {
		suffix := "_lj"
		if len(groups) > 1 {
			suffix = fmt.Sprintf("_lj_%d", i+1)
		}
		var b strings.Builder
		for _, p := range posts {
			if p.Header != "" {
				b.WriteString("<h3>" + p.Header + "</h3>\n")
			}
			b.WriteString(p.Content + "\n\n")
		}
		out := outputName(path, "html", suffix, addTS)
		if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
			return err
		}
		reportOutput(out)
	}
	return nil
}

// publishLJ is lj.py's export: each group posted, then — with --navigation —
// every post edited to carry the line linking it to the others.
func publishLJ(client *lj.Client, groups [][]lj.Post, navigation bool) error {
	ctx := context.Background()
	results := make([]lj.Result, len(groups))
	for i, posts := range groups {
		res, err := client.Publish(ctx, posts)
		if err != nil {
			return err
		}
		results[i] = res
		reportOutput(res.URL)
	}
	if !navigation || len(groups) < 2 {
		return nil
	}
	urls := make([]string, len(results))
	for i, r := range results {
		urls[i] = r.URL
	}
	for i, line := range lj.Navigation(groups, urls) {
		post := lj.Post{Header: groups[i][0].Header, Content: groups[i][0].Content + "\n\n" + line}
		if _, err := client.Edit(ctx, post, results[i].ItemID); err != nil {
			return err
		}
	}
	return nil
}
