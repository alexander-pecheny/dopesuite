package tg

import (
	"context"
	"database/sql"
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
	learnt := map[string]int64{}
	if channelID == 0 {
		if channelID, err = askForChannel(ctx, bot, channelName, say); err != nil {
			return t, err
		}
		learnt[channelName] = channelID
	}
	if chatID, err = askForChat(ctx, bot, chatID, channelID, chatName, say); err != nil {
		return t, err
	}
	if chatName != "" && cache[chatName] != bare(chatID) {
		learnt[chatName] = chatID
	}
	saveResolved(learnt)

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
	return bare(id), nil
}

// askForChat has the person post a code in the group until the bot sees it
// there and the group is not the channel itself. A chatID already known and
// distinct from the channel is returned as it is.
func askForChat(ctx context.Context, bot *Bot, chatID, channelID int64, name string, say Prompter) (int64, error) {
	s := xystrings.Default
	for chatID == 0 || bare(chatID) == bare(channelID) {
		if chatID != 0 {
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

// resolveSchema is chgksuite's resolve.db, word for word: the ids of the
// channels and groups named by username, which the two tools share. A channel
// is kept without its "-100", as chgksuite prefixes it again on reading; a
// group may be kept either way.
const resolveSchema = "CREATE TABLE IF NOT EXISTS resolve (username TEXT PRIMARY KEY, id INTEGER)"

// openResolveDB opens chgksuite's resolve.db as it is: a plain connection with
// a busy timeout and none of sqlitex's pragmas, so the file keeps the journal
// mode chgksuite gave it.
func openResolveDB() (*sql.DB, error) {
	path, err := sharedPath("resolve.db")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), cacheDirMode); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(resolveSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// resolveCache is resolve.db, read once at the start of a resolution and
// written back at its end. Without the file it is empty, and nothing is lost
// but the conversation that fills it again.
type resolveCache map[string]int64

func loadResolveCache() resolveCache {
	cache := resolveCache{}
	path, err := sharedPath("resolve.db")
	if err != nil {
		return cache
	}
	if _, err := os.Stat(path); err != nil {
		return cache
	}
	db, err := openResolveDB()
	if err != nil {
		return cache
	}
	defer db.Close()
	rows, err := db.Query("SELECT username, id FROM resolve")
	if err != nil {
		return cache
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var id int64
		if rows.Scan(&name, &id) == nil {
			cache[name] = bare(id)
		}
	}
	return cache
}

// saveResolved writes the names a resolution learnt into resolve.db.
func saveResolved(learnt map[string]int64) {
	if len(learnt) == 0 {
		return
	}
	db, err := openResolveDB()
	if err != nil {
		return
	}
	defer db.Close()
	for name, id := range learnt {
		_, _ = db.Exec("INSERT OR REPLACE INTO resolve (username, id) VALUES (?, ?)", name, bare(id))
	}
}

// bare is an id without the "-100" the Bot API puts before a channel's or a
// supergroup's, which is the form resolve.db keeps channels in.
func bare(id int64) int64 {
	s := idstr.Format(id)
	if rest, ok := strings.CutPrefix(s, "-100"); ok {
		if n, err := idstr.Parse(rest); err == nil {
			return n
		}
	}
	return id
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
