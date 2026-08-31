package tg

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/i18n"
)

// Poster is everything the export needs from Telegram. The flow is written
// against it so it can be replayed against chgksuite's own transcript.
type Poster interface {
	// PostRich sends one rich message and returns its id.
	PostRich(ctx context.Context, chatID, html string, media []Media, replyTo int64) (int64, error)
	// PostText sends one plain HTML message and returns its id.
	PostText(ctx context.Context, chatID, text string, replyTo int64) (int64, error)
	// DiscussionMessage waits for a channel post to reach the linked group and
	// returns its id there, which is what replies to that post reply to.
	DiscussionMessage(ctx context.Context, channelID string, messageID int64) (int64, error)
	// Call is the rest of the Bot API: polls, pinning, reactions.
	Call(ctx context.Context, method string, data map[string]any) error
}

// Target is where a package goes: a channel and the group its comments live in.
type Target struct {
	// ChannelID and ChatID are Telegram's own "-100…" ids.
	ChannelID, ChatID string
}

// exporter is one run of the export.
type exporter struct {
	f      *formatter
	p      Poster
	t      Target
	polls  *PollConfig
	opts   Options
	buffer []string // headings and loose text, waiting for the next post

	heading          string // the package's title, for the navigation post
	section          bool   // the next post opens a section, so it gets a nav link
	sectionLinks     []sectionLink
	tourNumber       string
	lastDiscussionID int64
	tourDiscussionID int64
	qcount           int
	// lastNumber is the number of the last question seen, which is what
	// --skip_until measures everything against, headings included.
	lastNumber int
}

type sectionLink struct {
	link, tour string
}

// Request is one export: the package, the pictures it names, where it goes and
// how it is written.
type Request struct {
	Doc fsource.Doc
	// Images maps a picture's name, as the (img …) directives spell it, to its
	// bytes.
	Images  map[string][]byte
	Options Options
	Target  Target
	// Polls, when set, adds a poll after each question, each tour and the
	// package itself.
	Polls *PollConfig
}

// Export posts a parsed package to a channel.
func Export(ctx context.Context, p Poster, r Request) error {
	t, polls := r.Target, r.Polls
	e := &exporter{
		f: &formatter{opts: r.Options, images: r.Images, labels: i18n.LabelsForOrDefault(r.Options.Language, r.Options.LabelsFile)}, p: p, t: t, polls: polls,
		opts: r.Options, qcount: 1, lastNumber: 1,
	}
	if polls != nil {
		if err := p.Call(ctx, "setChatAvailableReactions", map[string]any{
			"chat_id": t.ChannelID, "available_reactions": "[]",
		}); err != nil {
			return err
		}
	}
	for _, el := range r.Doc {
		if err := e.element(ctx, el); err != nil {
			return err
		}
	}
	if err := e.flush(ctx); err != nil {
		return err
	}
	if err := e.tourPoll(ctx); err != nil {
		return err
	}
	if r.Options.SkipUntil > 0 {
		return nil
	}
	return e.navigation(ctx)
}

// element is tg_process_element: a question is posted on its own, everything
// else waits in the buffer for the next one.
func (e *exporter) element(ctx context.Context, el fsource.Pair) error {
	q, isQuestion := el.Content.(*fsource.Question)
	if isQuestion && el.Type == "Question" {
		return e.question(ctx, q)
	}
	if e.skipping() {
		return nil
	}
	switch el.Type {
	case "heading":
		text, err := e.f.value(el.Content)
		if err != nil {
			return err
		}
		if e.heading == "" {
			e.heading = text
		}
		e.buffer = append(e.buffer, heading(text))
	case "section":
		if err := e.flush(ctx); err != nil {
			return err
		}
		if err := e.tourPoll(ctx); err != nil {
			return err
		}
		text, err := e.f.value(el.Content)
		if err != nil {
			return err
		}
		e.tourNumber = tourNumber(text)
		e.buffer = append(e.buffer, heading(text))
		e.section = true
	default:
		text, err := e.f.value(el.Content)
		if err != nil {
			return err
		}
		if text != "" {
			e.buffer = append(e.buffer, text)
		}
	}
	return nil
}

func (e *exporter) question(ctx context.Context, q *fsource.Question) error {
	if v, ok := q.Get("setcounter").(string); ok {
		if n, err := strconv.Atoi(v); err == nil {
			e.qcount = n
		}
	}
	number := fmt.Sprintf("%v", e.qcount)
	if n := q.Get("number"); n != nil {
		number = fmt.Sprintf("%v", n)
	} else {
		// chgksuite counts a numberless question twice, once here and once
		// while rendering its label. Faithfully, then.
		e.qcount++
	}
	e.qcount++
	n, err := strconv.Atoi(number)
	e.lastNumber = 0
	if err == nil {
		e.lastNumber = n
	}
	if e.opts.SkipUntil > 0 && (err != nil || n < e.opts.SkipUntil) {
		return nil
	}
	if err := e.flush(ctx); err != nil {
		return err
	}
	html, err := e.f.question(q, number)
	if err != nil {
		return fmt.Errorf("question %s: %w", number, err)
	}
	if err := e.postGroup(ctx, html); err != nil {
		return err
	}
	return e.questionPoll(ctx, number)
}

// skipping reports whether --skip_until has not been reached yet. chgksuite
// measures it by the last question number it saw, so the headings before the
// first kept question are dropped with it.
func (e *exporter) skipping() bool {
	return e.opts.SkipUntil > 0 && (e.lastNumber == 0 || e.lastNumber < e.opts.SkipUntil)
}

// flush posts whatever the buffer holds as one message.
func (e *exporter) flush(ctx context.Context) error {
	if len(e.buffer) == 0 {
		return nil
	}
	html := e.f.buffered(e.buffer)
	e.buffer = nil
	if html == "" {
		return nil
	}
	return e.postGroup(ctx, html)
}

// postGroup posts one message to the channel, waits for its copy in the
// discussion group (which is what a poll or a reply hangs off), and remembers
// the link when the message opens a section.
func (e *exporter) postGroup(ctx context.Context, html string) error {
	final, media := e.f.finalize(html)
	id, err := e.p.PostRich(ctx, e.t.ChannelID, final, media, 0)
	if err != nil {
		return err
	}
	discussionID, err := e.p.DiscussionMessage(ctx, e.t.ChannelID, id)
	if err != nil {
		return err
	}
	e.lastDiscussionID = discussionID
	if e.section {
		e.sectionLinks = append(e.sectionLinks, sectionLink{
			link: messageLink(e.t.ChannelID, id), tour: e.tourNumber,
		})
		e.tourDiscussionID = discussionID
	}
	e.section = false
	return nil
}

// navigation posts the pinned index: the package's title, an invitation to
// comment, and a link per tour.
func (e *exporter) navigation(ctx context.Context) error {
	lines := []string{e.f.labels.Text("general_impressions_text")}
	if e.heading != "" {
		lines = append([]string{"<b>" + e.heading + "</b>", ""}, lines...)
	}
	for _, s := range e.sectionLinks {
		lines = append(lines, e.f.labels.Text("section")+" "+s.tour+": "+s.link)
	}
	id, err := e.p.PostText(ctx, e.t.ChannelID, strings.TrimSpace(strings.Join(lines, "\n")), 0)
	if err != nil {
		return err
	}
	if e.polls != nil && e.polls.Packet != nil {
		discussionID, err := e.p.DiscussionMessage(ctx, e.t.ChannelID, id)
		if err != nil {
			return err
		}
		if err := e.poll(ctx, e.polls.Packet, map[string]string{"TITLE": e.heading}, discussionID); err != nil {
			return err
		}
	}
	return e.p.Call(ctx, "pinChatMessage", map[string]any{
		"chat_id": e.t.ChannelID, "message_id": id, "disable_notification": true,
	})
}

func (e *exporter) questionPoll(ctx context.Context, number string) error {
	if e.polls == nil || e.polls.Question == nil {
		return nil
	}
	return e.poll(ctx, e.polls.Question, map[string]string{"NUMBER": number}, e.lastDiscussionID)
}

func (e *exporter) tourPoll(ctx context.Context) error {
	if e.polls == nil || e.polls.Tour == nil || e.tourNumber == "" {
		return nil
	}
	return e.poll(ctx, e.polls.Tour, map[string]string{"NUMBER": e.tourNumber}, e.tourDiscussionID)
}

// poll posts one poll: in the discussion group under the message it belongs to
// when the config says "comment", in the channel itself otherwise.
func (e *exporter) poll(ctx context.Context, cfg *Poll, subs map[string]string, replyTo int64) error {
	question := cfg.Text
	for k, v := range subs {
		question = strings.ReplaceAll(question, "{"+k+"}", v)
	}
	chatID, reply := e.t.ChannelID, int64(0)
	if e.polls.Mode == "comment" && replyTo != 0 {
		chatID, reply = e.t.ChatID, replyTo
	}
	options, err := jsonArray(cfg.Variants)
	if err != nil {
		return err
	}
	data := map[string]any{
		"chat_id": chatID, "question": question, "options": options,
		"is_anonymous": cfg.IsAnonymous, "disable_notification": true,
	}
	if cfg.QuizRightAnswerSet {
		if i := indexOf(cfg.Variants, cfg.QuizRightAnswer); i >= 0 {
			data["type"] = "quiz"
			data["correct_option_id"] = i
		} else {
			data["type"] = "regular"
		}
	} else {
		data["type"] = "regular"
		if cfg.AllowsRevotingSet {
			data["allows_revoting"] = cfg.AllowsRevoting
		}
	}
	if reply != 0 {
		data["reply_to_message_id"] = reply
	}
	return e.p.Call(ctx, "sendPoll", data)
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// messageLink is get_message_link for a private channel: the "-100" prefix is
// not part of the link.
func messageLink(chatID string, messageID int64) string {
	return fmt.Sprintf("https://t.me/c/%s/%d", strings.TrimPrefix(chatID, "-100"), messageID)
}

var reDigits = regexp.MustCompile(`\d+`)

// tourNumber pulls "3" out of "Тур 3", and settles for the whole text when the
// section carries no number.
func tourNumber(section string) string {
	if m := reDigits.FindString(section); m != "" {
		return m
	}
	return section
}
