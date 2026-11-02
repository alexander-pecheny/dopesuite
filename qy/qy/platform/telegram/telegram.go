// Package telegram reads a public channel's post, for a question nominated
// by its link. It asks for the post's embed page, which t.me serves to
// anyone, and takes the post's text and pictures out of it.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	httpTimeout = 15 * time.Second
	maxPage     = 2 << 20
)

// Post is a post's text, with its line breaks, and its pictures' addresses.
type Post struct {
	Text   string
	Photos []string
}

type Source interface {
	Post(ctx context.Context, link string) (Post, error)
}

var ErrNotPost = errors.New("telegram: not a post")

// linkRe matches a public post: t.me/channel/123, or the t.me/s/ form.
var linkRe = regexp.MustCompile(`^https?://(?:www\.)?(?:t\.me|telegram\.me)/(?:s/)?([A-Za-z0-9_]{4,})/(\d+)`)

// IsPost says the link names a post in a public channel.
func IsPost(link string) bool { return linkRe.MatchString(strings.TrimSpace(link)) }

type Client struct{ HTTP *http.Client }

func NewClient() *Client { return &Client{HTTP: &http.Client{Timeout: httpTimeout}} }

func (c *Client) Post(ctx context.Context, link string) (Post, error) {
	m := linkRe.FindStringSubmatch(strings.TrimSpace(link))
	if m == nil {
		return Post{}, ErrNotPost
	}
	url := "https://t.me/" + m[1] + "/" + m[2] + "?embed=1&mode=tme"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Post{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Post{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Post{}, fmt.Errorf("telegram: %s: %s", url, resp.Status)
	}
	page, err := io.ReadAll(io.LimitReader(resp.Body, maxPage))
	if err != nil {
		return Post{}, err
	}
	return Extract(string(page))
}

var (
	textStartRe = regexp.MustCompile(`<div class="tgme_widget_message_text[^"]*"[^>]*>`)
	brRe        = regexp.MustCompile(`(?i)<br\s*/?>`)
	tagRe       = regexp.MustCompile(`<[^>]+>`)
	photoRe     = regexp.MustCompile(`tgme_widget_message_photo_wrap[^>]*background-image:url\('([^']+)'\)`)
	blankRe     = regexp.MustCompile(`\n{3,}`)
)

// Extract takes a post out of its embed page. A page with neither text nor a
// picture is not a post: t.me answers a missing one with an empty widget.
func Extract(page string) (Post, error) {
	var p Post
	if loc := textStartRe.FindStringIndex(page); loc != nil {
		body := page[loc[1]:]
		if end := strings.Index(body, "</div>"); end >= 0 {
			body = body[:end]
		}
		body = tagRe.ReplaceAllString(brRe.ReplaceAllString(body, "\n"), "")
		p.Text = strings.TrimSpace(blankRe.ReplaceAllString(html.UnescapeString(body), "\n\n"))
	}
	for _, m := range photoRe.FindAllStringSubmatch(page, -1) {
		p.Photos = append(p.Photos, html.UnescapeString(m[1]))
	}
	if p.Text == "" && len(p.Photos) == 0 {
		return p, ErrNotPost
	}
	return p, nil
}

// Unavailable answers every lookup with ErrNotPost, for tests.
type Unavailable struct{}

func (Unavailable) Post(context.Context, string) (Post, error) { return Post{}, ErrNotPost }

// Fixed answers from a map of links, for tests.
type Fixed map[string]Post

func (f Fixed) Post(_ context.Context, link string) (Post, error) {
	if p, ok := f[link]; ok {
		return p, nil
	}
	return Post{}, ErrNotPost
}
