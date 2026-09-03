package board

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	corei18n "pecheny.me/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"
	"xy/internal/xycli"
)

// The API both services answer. Trello's is the original; xy serves the three
// calls chgksuite makes (trello_compat.go), with every text field as a base64
// ciphertext envelope plus the key material to open it.

// TrelloAPI is where Trello's is.
const TrelloAPI = "https://trello.com/1"

// TrelloAppKey is chgksuite's registered application key (resources/trello.json).
const TrelloAppKey = "1d4fe71dd193855686196e7768aa4b05"

// TrelloConnectURL is the page that mints a token for that key.
const TrelloConnectURL = "https://trello.com/1/connect" +
	"?key=" + TrelloAppKey + "&name=Chgk&scope=read,write&response_type=token"

// JSON is a board as Trello shapes it, which is what the download reads. xy's
// answer is folded into the same shape, decrypted.
type JSON struct {
	Keymeta xycli.Keymeta `json:"keymeta"`
	Lists   []List        `json:"lists"`
	Cards   []Card        `json:"cards"`
}

type List struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Closed bool   `json:"closed"`
}

type Card struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Desc   string  `json:"desc"`
	IDList string  `json:"idList"`
	Closed bool    `json:"closed"`
	Labels []Label `json:"labels"`
}

type Label struct {
	Name string `json:"name"`
}

// Client talks to whichever service the board is on.
type Client struct {
	http *http.Client
	// dk is the xy board's data key once the passphrase has opened it.
	dk xycli.DataKey
	// trelloAPI is TrelloAPI, overridden by the tests.
	trelloAPI string
}

func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: time.Minute}, trelloAPI: TrelloAPI}
}

// Fetch reads the whole board. For xy it needs the passphrase, which it uses to
// derive the data key and decrypt every field before returning.
func (c *Client) Fetch(ctx context.Context, b Board) (*JSON, error) {
	if b.Service == XY {
		return c.fetchXY(ctx, b)
	}
	params := url.Values{
		"key": {b.Key}, "token": {b.Token},
		"fields": {"all"}, "cards": {"all"}, "card_fields": {"all"},
		"card_attachments": {"true"}, "labels": {"all"},
		"lists": {"all"}, "list_fields": {"all"}, "organization": {"false"},
	}
	var out JSON
	return &out, c.getJSON(ctx, c.trelloAPI+"/boards/"+b.ID, params, &out)
}

// FetchKeymeta reads an xy board's key material without needing its passphrase,
// which is what the upload does before asking for one.
func (c *Client) FetchKeymeta(ctx context.Context, b Board) (xycli.Keymeta, error) {
	var out JSON
	err := c.getJSON(ctx, b.BaseURL+"/1/boards/"+b.ID, url.Values{"token": {b.Token}}, &out)
	return out.Keymeta, err
}

// Unlock derives the board's data key from its passphrase, which every later
// call on an xy board needs.
func (c *Client) Unlock(passphrase string, km xycli.Keymeta) error {
	dk, err := xycli.Unlock(passphrase, km)
	if err != nil {
		return err
	}
	c.dk = dk
	return nil
}

func (c *Client) fetchXY(ctx context.Context, b Board) (*JSON, error) {
	var out JSON
	if err := c.getJSON(ctx, b.BaseURL+"/1/boards/"+b.ID, url.Values{"token": {b.Token}}, &out); err != nil {
		return nil, err
	}
	if c.dk == nil {
		if err := c.Unlock(b.Passphrase, out.Keymeta); err != nil {
			return nil, err
		}
	}
	for i := range out.Lists {
		name, err := c.dk.DecField(out.Lists[i].Name)
		if err != nil {
			return nil, corei18n.User(xystrings.Default.Boardsync.Fetch.List(out.Lists[i].ID, err.Error()))
		}
		out.Lists[i].Name = name
	}
	for i := range out.Cards {
		desc, err := c.dk.DecField(out.Cards[i].Desc)
		if err != nil {
			return nil, corei18n.User(xystrings.Default.Boardsync.Fetch.Card(out.Cards[i].ID, err.Error()))
		}
		out.Cards[i].Desc = desc
		// xy has no card titles of its own; it derives them from the text.
		for j := range out.Cards[i].Labels {
			if name, err := c.dk.DecField(out.Cards[i].Labels[j].Name); err == nil {
				out.Cards[i].Labels[j].Name = name
			}
		}
	}
	return &out, nil
}

// Lists reads just the board's lists, which is all the upload needs.
func (c *Client) Lists(ctx context.Context, b Board) ([]List, error) {
	var lists []List
	if b.Service == XY {
		if err := c.getJSON(ctx, b.BaseURL+"/1/boards/"+b.ID+"/lists", url.Values{"token": {b.Token}}, &lists); err != nil {
			return nil, err
		}
		for i := range lists {
			name, err := c.dk.DecField(lists[i].Name)
			if err != nil {
				return nil, err
			}
			lists[i].Name = name
		}
		return lists, nil
	}
	params := url.Values{"key": {b.Key}, "token": {b.Token}}
	return lists, c.getJSON(ctx, c.trelloAPI+"/boards/"+b.ID+"/lists", params, &lists)
}

// PostCard creates one card in a list. On xy the text is encrypted first — the
// server never sees it.
func (c *Client) PostCard(ctx context.Context, b Board, listID, name, desc string) error {
	form := url.Values{"name": {name}, "desc": {desc}}
	target := c.trelloAPI + "/lists/" + listID + "/cards"
	if b.Service == XY {
		enc, err := c.dk.EncField(desc)
		if err != nil {
			return err
		}
		form = url.Values{"token": {b.Token}, "name": {name}, "desc": {enc}}
		target = b.BaseURL + "/1/lists/" + listID + "/cards"
	} else {
		form.Set("key", b.Key)
		form.Set("token", b.Token)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, target string, params url.Values, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, into)
}
