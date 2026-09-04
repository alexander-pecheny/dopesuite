// Package ratingtournaments answers what a Representative needs in order to
// choose between the tournaments playable at a Slot's time: who edited each
// one, how hard its editors expect it to be, and how many teams have asked to
// play it. buff's mirror carries none of that, so it comes from
// rating.chgk.info per tournament and is kept for a few hours.
package ratingtournaments

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Detail is one tournament as the picker shows it. Difficulty is zero when its
// editors forecast none, and Teams is zero when nobody has asked to play yet:
// both read as "not said" rather than as a low number.
type Detail struct {
	ID         int64    `json:"id"`
	Editors    []string `json:"editors"`
	Difficulty float64  `json:"difficulty"`
	Teams      int      `json:"teams"`
}

const (
	ttl      = 6 * time.Hour
	parallel = 8
)

// Catalogue is the cached copy. The zero value talks to the real site; a test
// points Base at its own.
type Catalogue struct {
	HTTP *http.Client
	Base string

	mu   sync.Mutex
	seen map[int64]entry
}

type entry struct {
	detail Detail
	at     time.Time
}

var std = &Catalogue{}

// Default is the catalogue a server keeps unless it was given one.
func Default() *Catalogue { return std }

func (c *Catalogue) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Catalogue) base() string {
	if c.Base != "" {
		return c.Base
	}
	return "https://api.rating.chgk.net"
}

// Details answers for every id it can. The rating site is asked about each one
// separately, so an id it will not answer for is left out rather than failing
// the whole list — a card with no forecast is still a card.
func (c *Catalogue) Details(ctx context.Context, ids []int64) map[int64]Detail {
	out := map[int64]Detail{}
	var want []int64
	now := time.Now()
	c.mu.Lock()
	for _, id := range ids {
		if e, ok := c.seen[id]; ok && now.Sub(e.at) < ttl {
			out[id] = e.detail
			continue
		}
		want = append(want, id)
	}
	c.mu.Unlock()
	if len(want) == 0 {
		return out
	}
	fetched := c.fetchAll(ctx, want)
	c.mu.Lock()
	if c.seen == nil {
		c.seen = map[int64]entry{}
	}
	for id, d := range fetched {
		c.seen[id] = entry{detail: d, at: now}
		out[id] = d
	}
	c.mu.Unlock()
	return out
}

func (c *Catalogue) fetchAll(ctx context.Context, ids []int64) map[int64]Detail {
	var mu sync.Mutex
	out := map[int64]Detail{}
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			d, err := c.fetchOne(ctx, id)
			if err != nil {
				return
			}
			mu.Lock()
			out[id] = d
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return out
}

type apiPerson struct {
	Name       string `json:"name"`
	Patronymic string `json:"patronymic"`
	Surname    string `json:"surname"`
}

type apiTournament struct {
	Editors    []apiPerson `json:"editors"`
	Difficulty float64     `json:"difficultyForecast"`
}

type apiRequest struct {
	Status string `json:"status"`
	Teams  int    `json:"approximateTeamsCount"`
}

func (c *Catalogue) fetchOne(ctx context.Context, id int64) (Detail, error) {
	var t apiTournament
	if err := c.get(ctx, fmt.Sprintf("/tournaments/%d", id), &t); err != nil {
		return Detail{}, err
	}
	d := Detail{ID: id, Difficulty: t.Difficulty, Editors: people(t.Editors)}
	var reqs []apiRequest
	// A tournament nobody has asked to play answers with an empty list, and one
	// whose requests the site will not serve still has its editors worth showing.
	if err := c.get(ctx, fmt.Sprintf("/tournaments/%d/requests", id), &reqs); err == nil {
		d.Teams = teamsAsked(reqs)
	}
	return d, nil
}

// teamsAsked projects how many teams will play: every request that has not been
// turned down, whether or not it has been accepted yet.
func teamsAsked(reqs []apiRequest) int {
	n := 0
	for _, r := range reqs {
		if r.Status != "R" {
			n += r.Teams
		}
	}
	return n
}

func (c *Catalogue) get(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rating: %s: %s", path, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, into)
}

// people names a tournament's editors the way a Representative reads them:
// surname first, then initials, in the order the site lists them.
func people(list []apiPerson) []string {
	out := make([]string, 0, len(list))
	for _, p := range list {
		if name := Initials(p.Surname, p.Name, p.Patronymic); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// Initials is a surname followed by whatever initials fit beside it.
func Initials(surname, name, patronymic string) string {
	parts := []string{strings.TrimSpace(surname)}
	for _, p := range []string{name, patronymic} {
		if r := []rune(strings.TrimSpace(p)); len(r) > 0 {
			parts = append(parts, string(r[0])+".")
		}
	}
	if parts[0] == "" {
		parts = parts[1:]
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}
