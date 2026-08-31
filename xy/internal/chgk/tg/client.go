package tg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"strconv"
	"time"

	"golang.org/x/image/draw"

	"pecheny.me/dopecore/tgbot"
	"xy/internal/chgk/imgconv"
)

// client posts through the Bot API, with the bot above watching for the copies
// Telegram makes of what it posts.
type client struct {
	bot     *Bot
	settle  time.Duration // how long a channel post is given to reach the group
	pace    time.Duration // the pause between posts
	channel int64         // the channel's numeric id, for matching the copies
	chat    int64
}

// NewPoster returns a Poster that posts for real, through bot.
func NewPoster(bot *Bot, t Target) (Poster, error) {
	channel, err := numericID(t.ChannelID)
	if err != nil {
		return nil, err
	}
	chat, err := numericID(t.ChatID)
	if err != nil {
		return nil, err
	}
	return &client{bot: bot, settle: 90 * time.Second, pace: 5 * time.Second,
		channel: channel, chat: chat}, nil
}

// dryRun is `--dry_run`: it writes what it would send to the log, sends nothing,
// and hands back ids of its own so the navigation post still links up.
type dryRun struct{ nextID int64 }

// NewDryRunPoster returns a Poster that posts nothing.
func NewDryRunPoster() Poster { return &dryRun{} }

func (d *dryRun) PostRich(_ context.Context, chatID, html string, media []Media, _ int64) (int64, error) {
	log.Printf("[dry run] rich message to %s (%d picture(s)): %s", chatID, len(media), html)
	return d.id(), nil
}

func (d *dryRun) PostText(_ context.Context, chatID, text string, _ int64) (int64, error) {
	log.Printf("[dry run] message to %s: %s", chatID, text)
	return d.id(), nil
}

func (d *dryRun) DiscussionMessage(context.Context, string, int64) (int64, error) {
	return d.id(), nil
}

func (d *dryRun) Call(_ context.Context, method string, data map[string]any) error {
	log.Printf("[dry run] %s %v", method, data)
	return nil
}

func (d *dryRun) id() int64 { d.nextID++; return d.nextID }

func numericID(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("not a telegram id: %q", s)
	}
	return n, nil
}

func (c *client) PostRich(ctx context.Context, chatID, html string, media []Media, replyTo int64) (int64, error) {
	type richMedia struct {
		ID    string            `json:"id"`
		Media map[string]string `json:"media"`
	}
	rich := map[string]any{"html": html}
	var files []tgbot.FilePart
	var items []richMedia
	for _, m := range media {
		data, err := prepareImage(m.Data, richImgHeightP)
		if err != nil {
			return 0, fmt.Errorf("picture %s: %w", m.Name, err)
		}
		field := "f" + m.ID
		files = append(files, tgbot.FilePart{Field: field, Filename: m.ID + ".jpg", Data: data})
		items = append(items, richMedia{ID: m.ID, Media: map[string]string{
			"type": "photo", "media": "attach://" + field,
		}})
	}
	if len(items) > 0 {
		rich["media"] = items
	}

	var res json.RawMessage
	var err error
	if len(files) > 0 {
		blob, jsonErr := json.Marshal(rich)
		if jsonErr != nil {
			return 0, jsonErr
		}
		fields := map[string]string{
			"chat_id": chatID, "rich_message": string(blob), "disable_notification": "true",
		}
		if replyTo != 0 {
			fields["reply_parameters"] = fmt.Sprintf(`{"message_id":%d}`, replyTo)
		}
		res, err = c.bot.Client().CallMultipart(ctx, "sendRichMessage", fields, files)
	} else {
		payload := map[string]any{
			"chat_id": chatID, "rich_message": rich, "disable_notification": true,
		}
		if replyTo != 0 {
			payload["reply_parameters"] = map[string]any{"message_id": replyTo}
		}
		res, err = c.bot.Client().Call(ctx, "sendRichMessage", payload)
	}
	if err != nil {
		return 0, err
	}
	c.wait(ctx)
	return messageID(res)
}

func (c *client) PostText(ctx context.Context, chatID, text string, replyTo int64) (int64, error) {
	payload := map[string]any{
		"chat_id": chatID, "text": text, "parse_mode": "HTML",
		"disable_web_page_preview": true, "disable_notification": true,
	}
	if replyTo != 0 {
		payload["reply_to_message_id"] = replyTo
	}
	res, err := c.bot.Client().Call(ctx, "sendMessage", payload)
	if err != nil {
		return 0, err
	}
	c.wait(ctx)
	return messageID(res)
}

// wait paces the export. A packet is a few dozen posts, and Telegram answers a
// channel posted to as fast as it can be with a 429 and a retry_after; going at
// chgksuite's cadence instead means never being told to wait.
func (c *client) wait(ctx context.Context) {
	if c.pace <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(c.pace):
	}
}

func (c *client) DiscussionMessage(ctx context.Context, _ string, messageID int64) (int64, error) {
	return c.bot.WaitForDiscussionCopy(ctx, c.channel, c.chat, messageID, c.settle)
}

func (c *client) Call(ctx context.Context, method string, data map[string]any) error {
	if _, err := c.bot.Client().Call(ctx, method, data); err != nil {
		return err
	}
	c.wait(ctx)
	return nil
}

func messageID(res json.RawMessage) (int64, error) {
	var msg struct {
		MessageID int64 `json:"message_id"`
	}
	if err := json.Unmarshal(res, &msg); err != nil {
		return 0, err
	}
	return msg.MessageID, nil
}

// prepareImage is prepare_image_for_telegram: hold the picture to a height, keep
// it away from the aspect ratio and pixel count Telegram refuses, and send it as
// a JPEG. The resampling is Go's rather than Pillow's, so the bytes differ from
// chgksuite's while the rules do not.
func prepareImage(raw []byte, maxHeight int) ([]byte, error) {
	img, err := imgconv.Decode(raw)
	if err != nil {
		return nil, err
	}
	if b := img.Bounds(); maxHeight > 0 && b.Dy() > maxHeight {
		w := max(b.Dx()*maxHeight/b.Dy(), 1)
		img = scale(img, w, maxHeight)
	}
	img = padExtremeRatio(img)
	if b := img.Bounds(); b.Dx()+b.Dy() >= 10000 {
		f := 10000.0 / float64(b.Dx()+b.Dy())
		w, h := int(float64(b.Dx())*f), int(float64(b.Dy())*f)
		if m := max(w, h); m > 1000 {
			w, h = w*1000/m, h*1000/m
		}
		img = scale(img, max(w, 1), max(h, 1))
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// padExtremeRatio pads a sliver of an image with white until it is squarer than
// the 1:20 Telegram rejects.
func padExtremeRatio(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 || float64(max(w, h))/float64(min(w, h)) < 20 {
		return img
	}
	nw, nh := w, h
	if w > h {
		nh = w / 19
	} else {
		nw = h / 19
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	at := image.Pt((nw-w)/2, (nh-h)/2)
	draw.Draw(dst, image.Rect(at.X, at.Y, at.X+w, at.Y+h), img, b.Min, draw.Src)
	return dst
}

func scale(img image.Image, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	return dst
}
