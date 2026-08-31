package lj

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // LiveJournal's challenge scheme is md5; it is not ours to choose
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The posting half of lj.py: LiveJournal's XML-RPC interface, whose
// authentication is a challenge hashed together with an md5 of the password.
//
// UNVERIFIED against the live service: there is no account here to try it
// against, so what this has been checked is the request it builds, not the
// answer it gets. The rendering half (lj.go) is the oracle-tested one.

// Endpoint is the interface lj.py talks to.
const Endpoint = "http://www.livejournal.com/interface/xmlrpc"

// Account is who to post as, and where.
type Account struct {
	Login    string
	Password string
	// Community posts to a community's journal instead of the user's own.
	Community string
	// Security is --security: "public", "friends", or a friend-group mask.
	// Empty means private, as chgksuite's else branch does.
	Security string
}

// Client talks to LiveJournal.
type Client struct {
	account  Account
	endpoint string
	http     *http.Client
	// Pause is how long to wait after each call, as lj.py sleeps between them
	// so the service does not throttle the run.
	Pause time.Duration
}

func NewClient(a Account) *Client {
	return &Client{account: a, endpoint: Endpoint, http: &http.Client{Timeout: time.Minute}, Pause: 5 * time.Second}
}

// Result is what a post came back as.
type Result struct {
	ItemID  int
	DItemID int
	URL     string
}

// Publish posts one group: the first Post becomes the entry, the rest comments
// on it.
func (c *Client) Publish(ctx context.Context, posts []Post) (Result, error) {
	if len(posts) == 0 {
		return Result{}, fmt.Errorf("нечего публиковать")
	}
	res, err := c.postEvent(ctx, posts[0], 0)
	if err != nil {
		return Result{}, err
	}
	journal := c.account.Community
	if journal == "" {
		journal = c.account.Login
	}
	for _, comment := range posts[1:] {
		if err := c.addComment(ctx, journal, res.DItemID, comment); err != nil {
			return res, err
		}
	}
	return res, nil
}

// Edit rewrites an entry already posted, which is how the navigation line is
// added once every tour has a URL.
func (c *Client) Edit(ctx context.Context, post Post, itemID int) (Result, error) {
	return c.postEvent(ctx, post, itemID)
}

func (c *Client) postEvent(ctx context.Context, post Post, itemID int) (Result, error) {
	challenge, response, err := c.challenge(ctx)
	if err != nil {
		return Result{}, err
	}
	now := time.Now()
	params := map[string]any{
		"username":       c.account.Login,
		"auth_method":    "challenge",
		"auth_challenge": challenge,
		"auth_response":  response,
		"subject":        post.Header,
		"event":          post.Content,
		"year":           now.Format("2006"),
		"mon":            now.Format("01"),
		"day":            now.Format("02"),
		"hour":           now.Format("15"),
		"min":            now.Format("04"),
	}
	switch {
	case c.account.Community != "":
		params["usejournal"] = c.account.Community
	case c.account.Security == "public":
	case c.account.Security != "":
		mask := c.account.Security
		if mask == "friends" {
			mask = "1"
		}
		params["security"], params["allowmask"] = "usemask", mask
	default:
		params["security"] = "private"
	}
	method := "LJ.XMLRPC.postevent"
	if itemID != 0 {
		method = "LJ.XMLRPC.editevent"
		params["itemid"] = strconv.Itoa(itemID)
	}
	fields, err := c.call(ctx, method, params)
	if err != nil {
		return Result{}, err
	}
	res := Result{URL: fields["url"]}
	res.ItemID, _ = strconv.Atoi(fields["itemid"])
	res.DItemID, _ = strconv.Atoi(fields["ditemid"])
	return res, nil
}

func (c *Client) addComment(ctx context.Context, journal string, ditemID int, comment Post) error {
	challenge, response, err := c.challenge(ctx)
	if err != nil {
		return err
	}
	_, err = c.call(ctx, "LJ.XMLRPC.addcomment", map[string]any{
		"username":       c.account.Login,
		"auth_method":    "challenge",
		"auth_challenge": challenge,
		"auth_response":  response,
		"journal":        journal,
		"ditemid":        strconv.Itoa(ditemID),
		"parenttalkid":   "0",
		"body":           comment.Content,
		"subject":        comment.Header,
	})
	return err
}

// challenge is get_chal: the server's nonce, and md5(nonce + md5(password)).
func (c *Client) challenge(ctx context.Context) (string, string, error) {
	fields, err := c.call(ctx, "LJ.XMLRPC.getchallenge", nil)
	if err != nil {
		return "", "", err
	}
	chal := fields["challenge"]
	if chal == "" {
		return "", "", fmt.Errorf("livejournal не выдал challenge")
	}
	pw := md5.Sum([]byte(c.account.Password))                //nolint:gosec // the scheme is theirs
	sum := md5.Sum([]byte(chal + hex.EncodeToString(pw[:]))) //nolint:gosec
	return chal, hex.EncodeToString(sum[:]), nil
}

// call sends one methodCall whose single argument is a struct of strings, and
// reads the flat struct that comes back.
func (c *Client) call(ctx context.Context, method string, params map[string]any) (map[string]string, error) {
	body, err := encodeCall(method, params)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("livejournal ответил %s", resp.Status)
	}
	if c.Pause > 0 {
		select {
		case <-time.After(c.Pause):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return decodeResponse(raw)
}

// encodeCall writes the methodCall. Every value goes as a <string>, which is
// what xmlrpc.client does for the strings lj.py passes.
func encodeCall(method string, params map[string]any) ([]byte, error) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?>` + "\n<methodCall><methodName>")
	if err := xml.EscapeText(&b, []byte(method)); err != nil {
		return nil, err
	}
	b.WriteString("</methodName><params>")
	if params != nil {
		b.WriteString("<param><value><struct>")
		keys := make([]string, 0, len(params))
		for k := range params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString("<member><name>")
			if err := xml.EscapeText(&b, []byte(k)); err != nil {
				return nil, err
			}
			b.WriteString("</name><value><string>")
			if err := xml.EscapeText(&b, []byte(fmt.Sprintf("%v", params[k]))); err != nil {
				return nil, err
			}
			b.WriteString("</string></value></member>")
		}
		b.WriteString("</struct></value></param>")
	}
	b.WriteString("</params></methodCall>")
	return []byte(b.String()), nil
}

// decodeResponse reads the flat struct LiveJournal answers with, and turns a
// <fault> into an error.
func decodeResponse(raw []byte) (map[string]string, error) {
	var doc struct {
		Fault *struct {
			Members []member `xml:"value>struct>member"`
		} `xml:"fault"`
		Members []member `xml:"params>param>value>struct>member"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("ответ livejournal: %w", err)
	}
	if doc.Fault != nil {
		fields := flatten(doc.Fault.Members)
		return nil, fmt.Errorf("livejournal: %s (код %s)", fields["faultString"], fields["faultCode"])
	}
	return flatten(doc.Members), nil
}

type member struct {
	Name  string `xml:"name"`
	Value struct {
		String string `xml:"string"`
		Int    string `xml:"int"`
		I4     string `xml:"i4"`
		Text   string `xml:",chardata"`
	} `xml:"value"`
}

func flatten(members []member) map[string]string {
	out := make(map[string]string, len(members))
	for _, m := range members {
		switch {
		case m.Value.String != "":
			out[m.Name] = m.Value.String
		case m.Value.Int != "":
			out[m.Name] = m.Value.Int
		case m.Value.I4 != "":
			out[m.Name] = m.Value.I4
		default:
			out[m.Name] = strings.TrimSpace(m.Value.Text)
		}
	}
	return out
}
