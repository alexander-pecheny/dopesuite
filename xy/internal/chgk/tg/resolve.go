package tg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/idstr"
	xystrings "xy/i18nstrings"
)

// Where a package goes is given as a numeric id, a t.me link or an @username.
// The first two are arithmetic; a username is not — a bot cannot look one up, so
// the person driving the export has to show the bot the channel and the group
// from the inside. That conversation is what this file holds, plus the cache
// that means it only happens once per target.

const (
	// promptTimeout is how long the bot waits for the person to do what it asked.
	promptTimeout = 5 * time.Minute
	// shortCodeRange bounds a short code to seven hex digits.
	shortCodeRange = 0xFFFFFFF
	hexBase        = 16
	cacheDirMode   = 0o755
	cacheFileMode  = 0o600
	// Stand-in ids a dry run posts to when it was given names.
	dryRunChannelID = 1111111111
	dryRunChatID    = 2222222222
)

var (
	reChannelLink = regexp.MustCompile(`^https?://t\.me/c/(\d+)`)
	rePublicLink  = regexp.MustCompile(`^https?://t\.me/([^/]+)`)
)

// parseTargetRef reads a channel or chat reference. It returns either an id or a
// username to resolve.
func parseTargetRef(ref string) (id int64, username string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, "", fmt.Errorf("no channel or chat given")
	}
	if n, convErr := idstr.Parse(ref); convErr == nil {
		if s := strings.TrimPrefix(ref, "-100"); s != ref {
			n, _ = idstr.Parse(s)
		}
		return n, "", nil
	}
	if name, ok := strings.CutPrefix(ref, "@"); ok {
		return 0, name, nil
	}
	if m := reChannelLink.FindStringSubmatch(ref); m != nil {
		n, _ := idstr.Parse(m[1])
		return n, "", nil
	}
	if m := rePublicLink.FindStringSubmatch(ref); m != nil {
		return 0, m[1], nil
	}
	return 0, ref, nil
}

// prefixed is the "-100…" form the Bot API wants for a channel or supergroup.
func prefixed(id int64) string {
	s := idstr.Format(id)
	if strings.HasPrefix(s, "-100") {
		return s
	}
	return "-100" + strings.TrimPrefix(s, "-")
}

// Prompter is how the resolution talks to the person running the export: it
// asks them to do something in Telegram, and they do it.
type Prompter func(format string, args ...any)

// ResolveTarget turns the two references into the ids the export posts to,
// asking for help only for a username it has not seen before.
func ResolveTarget(ctx context.Context, bot *Bot, channelRef, chatRef string, say Prompter) (Target, error) {
	var t Target
	channelID, channelName, err := parseTargetRef(channelRef)
	if err != nil {
		return t, fmt.Errorf("channel: %w", err)
	}
	chatID, chatName, err := parseTargetRef(chatRef)
	if err != nil {
		return t, fmt.Errorf("chat: %w", err)
	}
	cache := loadResolveCache()

	if channelID == 0 {
		channelID = cache[channelName]
	}
	if chatID == 0 {
		chatID = cache[chatName]
	}
	if channelID == 0 || chatID == 0 {
		if err := introduce(ctx, bot, say); err != nil {
			return t, err
		}
	}
	if channelID == 0 {
		if channelID, err = askForChannel(ctx, bot, channelName, say); err != nil {
			return t, err
		}
		cache[channelName] = channelID
	}
	if chatID, err = askForChat(ctx, bot, chatID, channelID, chatName, say); err != nil {
		return t, err
	}
	if chatName != "" {
		cache[chatName] = chatID
	}
	saveResolveCache(cache)

	t = Target{ChannelID: prefixed(channelID), ChatID: prefixed(chatID)}
	return t, verifyTarget(ctx, bot, t)
}

// askForChannel has the person forward a post from the channel to the bot.
func askForChannel(ctx context.Context, bot *Bot, name string, say Prompter) (int64, error) {
	say("%s", xystrings.Default.Tg.Resolve.Forward(name))
	id, err := bot.WaitForForwardedChannel(ctx, promptTimeout)
	if err != nil {
		return 0, fmt.Errorf("channel %s: %w", name, err)
	}
	return id, nil
}

// askForChat has the person post a code in the group until the bot sees it
// there and the group is not the channel itself. A chatID already known and
// distinct from the channel is returned as it is.
func askForChat(ctx context.Context, bot *Bot, chatID, channelID int64, name string, say Prompter) (int64, error) {
	s := xystrings.Default
	for chatID == 0 || chatID == channelID {
		if chatID == channelID {
			say("%s", s.Tg.Resolve.SameChannel())
		}
		code := shortCode()
		say("%s", s.Tg.Resolve.GroupCode(name, code))
		say("%s", s.Tg.Resolve.GroupCodeHint())
		var err error
		if chatID, err = bot.WaitForChatMessage(ctx, code, promptTimeout); err != nil {
			return 0, fmt.Errorf("chat %s: %w", name, err)
		}
	}
	return chatID, nil
}

// verifyTarget checks the bot may post to both the channel and the chat.
func verifyTarget(ctx context.Context, bot *Bot, t Target) error {
	s := xystrings.Default
	if err := verifyAccess(ctx, bot, t.ChannelID, s.Tg.Verify.WhatChannel(), s.Tg.Verify.OfChannel()); err != nil {
		return err
	}
	return verifyAccess(ctx, bot, t.ChatID, s.Tg.Verify.WhatChat(), s.Tg.Verify.OfChat())
}

// introduce is chgksuite's authenticate_user: before asking the person to do
// things in Telegram, make sure the bot can hear them at all.
func introduce(ctx context.Context, bot *Bot, say Prompter) error {
	s := xystrings.Default
	code := shortCode()
	say("%s", s.Tg.Resolve.PrivateCode(code))
	chatID, err := bot.WaitForCode(ctx, code, promptTimeout)
	if err != nil {
		return fmt.Errorf("authentication: %w", err)
	}
	bot.Client().Send(ctx, chatID, s.Tg.Resolve.Done())
	return nil
}

// verifyAccess checks the bot is an administrator where it is about to post,
// which is what Telegram requires of it and the commonest thing to have missed.
// The place is named twice because the two failures decline it differently.
func verifyAccess(ctx context.Context, bot *Bot, chatID, dative, genitive string) error {
	res, err := bot.Client().Call(ctx, "getChatAdministrators", map[string]any{"chat_id": chatID})
	if err != nil {
		return corei18n.User(xystrings.Default.Tg.Verify.NotMember(dative, err.Error()))
	}
	var admins []struct {
		User struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(res, &admins); err != nil {
		return err
	}
	me, err := bot.Client().Call(ctx, "getMe", map[string]any{})
	if err != nil {
		return err
	}
	var self struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(me, &self); err != nil {
		return err
	}
	for _, a := range admins {
		if a.User.ID == self.ID {
			return nil
		}
	}
	return corei18n.User(xystrings.Default.Tg.Verify.NotAdmin(genitive))
}

// shortCode is a one-off word the person types back, so the bot knows which
// message is theirs.
func shortCode() string {
	return strconv.FormatInt(time.Now().UnixNano()%shortCodeRange, hexBase)
}

// resolveCachePath is where the ids of named channels are kept, beside
// chgksuite's own resolve.db rather than inside it: one tool per file.
func resolveCachePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".chgksuite", "tg-resolve.json")
}

func loadResolveCache() map[string]int64 {
	cache := map[string]int64{}
	data, err := os.ReadFile(resolveCachePath())
	if err != nil {
		return cache
	}
	_ = json.Unmarshal(data, &cache)
	return cache
}

func saveResolveCache(cache map[string]int64) {
	path := resolveCachePath()
	if path == "" || len(cache) == 0 {
		return
	}
	data, err := json.MarshalIndent(cache, "", " ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), cacheDirMode)
	_ = os.WriteFile(path, data, cacheFileMode)
}

// DryRunTarget is where a dry run pretends to post: the ids it was given, or
// stand-ins when it was given names, since nothing is resolved without a bot.
func DryRunTarget(channelRef, chatRef string) Target {
	id := func(ref string, fallback int64) string {
		if n, _, err := parseTargetRef(ref); err == nil && n != 0 {
			return prefixed(n)
		}
		return prefixed(fallback)
	}
	return Target{ChannelID: id(channelRef, dryRunChannelID), ChatID: id(chatRef, dryRunChatID)}

}
