// Package ratingvenues holds rating.chgk.info's venue catalogue whole. A
// Площадка in dope is one of theirs, so the create form has to search them —
// and their API has no search over venues, only pages. Three thousand rows
// arrive in seven of those and are kept for a day.
package ratingvenues

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Venue is a rating.chgk.info venue as dope shows and stores it.
type Venue struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Town string `json:"town"`
}

// ErrUnavailable is every way the rating site can fail to answer: what a host
// does about it is the same in each case.
var ErrUnavailable = errors.New("rating.chgk.info не ответил")

// ErrNoVenue is an id the rating site does not know.
var ErrNoVenue = errors.New("такой площадки нет на rating.chgk.info")

const (
	ttl      = 24 * time.Hour
	pageSize = 512
	maxPages = 40
)

// Catalogue is the cached copy. The zero value talks to the real site; a test
// points Base at its own.
type Catalogue struct {
	HTTP *http.Client
	Base string

	mu         sync.Mutex
	venues     []Venue
	at         time.Time
	refreshing bool
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

// Search answers what to offer for what has been typed: an id, a venue's name
// or the town it is in. What starts with the query comes before what merely
// contains it, so «Тбилиси» offers Тбилиси itself first.
func (c *Catalogue) Search(ctx context.Context, query string, limit int) ([]Venue, error) {
	all, err := c.all(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var hits []Venue
	for _, v := range all {
		if rank(v, query) < 3 {
			hits = append(hits, v)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return rank(hits[i], query) < rank(hits[j], query) })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// Lookup resolves the id a form posted. A venue registered since the copy was
// taken is not in it, so a miss asks the site itself.
func (c *Catalogue) Lookup(ctx context.Context, id int64) (Venue, error) {
	if id <= 0 {
		return Venue{}, ErrNoVenue
	}
	all, err := c.all(ctx)
	if err == nil {
		for _, v := range all {
			if v.ID == id {
				return v, nil
			}
		}
	}
	return c.fetchOne(ctx, id)
}

// rank is how well a venue answers the query: 0 its id, 1 its name, 2 its
// town, 3 not at all.
func rank(v Venue, query string) int {
	if query == "" {
		return 1
	}
	if strings.HasPrefix(strconv.FormatInt(v.ID, 10), query) {
		return 0
	}
	name := strings.ToLower(v.Name)
	if strings.HasPrefix(name, query) {
		return 1
	}
	if strings.Contains(name, query) || strings.Contains(strings.ToLower(v.Town), query) {
		return 2
	}
	return 3
}

// all is the copy. A day-old one is still answered with, and refetched behind
// the answer: nobody waits eight seconds for seven pages of venues.
func (c *Catalogue) all(ctx context.Context) ([]Venue, error) {
	c.mu.Lock()
	held, age := c.venues, time.Since(c.at)
	if len(held) > 0 {
		if age > ttl && !c.refreshing {
			c.refreshing = true
			go c.refresh()
		}
		c.mu.Unlock()
		return held, nil
	}
	defer c.mu.Unlock()
	fresh, err := c.fetchAll(ctx)
	if err != nil {
		return nil, err
	}
	c.venues, c.at = fresh, time.Now()
	return c.venues, nil
}

// refresh takes the copy again in the background; a failure leaves the old one,
// to be tried again a day later.
func (c *Catalogue) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fresh, err := c.fetchAll(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshing = false
	c.at = time.Now()
	if err == nil {
		c.venues = fresh
	}
}

func (c *Catalogue) fetchAll(ctx context.Context) ([]Venue, error) {
	var all []Venue
	for page := 1; page <= maxPages; page++ {
		url := fmt.Sprintf("%s/venues?itemsPerPage=%d&page=%d", c.base(), pageSize, page)
		var body []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Town struct {
				Name string `json:"name"`
			} `json:"town"`
		}
		// A page that is not there is not a missing venue; it is a site that
		// cannot be read.
		if err := c.get(ctx, url, &body); err != nil {
			return nil, ErrUnavailable
		}
		for _, v := range body {
			all = append(all, Venue{ID: v.ID, Name: strings.TrimSpace(v.Name), Town: strings.TrimSpace(v.Town.Name)})
		}
		if len(body) < pageSize {
			break
		}
	}
	if len(all) == 0 {
		return nil, ErrUnavailable
	}
	return all, nil
}

func (c *Catalogue) fetchOne(ctx context.Context, id int64) (Venue, error) {
	var body struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Town struct {
			Name string `json:"name"`
		} `json:"town"`
	}
	if err := c.get(ctx, fmt.Sprintf("%s/venues/%d", c.base(), id), &body); err != nil {
		return Venue{}, err
	}
	if body.Name == "" {
		return Venue{}, ErrNoVenue
	}
	return Venue{ID: id, Name: strings.TrimSpace(body.Name), Town: strings.TrimSpace(body.Town.Name)}, nil
}

func (c *Catalogue) get(ctx context.Context, url string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ErrUnavailable
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNoVenue
	}
	if resp.StatusCode != http.StatusOK {
		return ErrUnavailable
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(into); err != nil {
		return ErrUnavailable
	}
	return nil
}
