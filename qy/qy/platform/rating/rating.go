// Package rating reads a tournament's name and dates from api.rating.chgk.info,
// so the admins can see whether a question is eligible. The API allows 600
// reads a minute; Client spaces its requests well under that.
package rating

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	baseURL     = "https://api.rating.chgk.info/tournaments/"
	minInterval = 500 * time.Millisecond
	httpTimeout = 15 * time.Second
	dayLen      = len("2006-01-02")
)

type Tournament struct {
	Title      string
	Start, End string // calendar days, YYYY-MM-DD
	Editors    []Person
	Tours      []Tour // who edited which questions, when the site says
}

// Tour is one tour's credit: the questions it holds, numbered through the
// whole tournament, and its editors.
type Tour struct {
	From, To int
	Editors  []Person
}

// EditorsOf are the editors of question n: its tour's when the site splits
// them by tour, else the whole tournament's.
func (t Tournament) EditorsOf(n int) []Person {
	for _, tour := range t.Tours {
		if n >= tour.From && n <= tour.To && len(tour.Editors) > 0 {
			return tour.Editors
		}
	}
	return t.Editors
}

// Person is someone the rating site lists on a tournament: their id there
// and their name and surname.
type Person struct {
	ID   int64
	Name string
}

type Source interface {
	Tournament(ctx context.Context, id int64) (Tournament, error)
}

type Client struct {
	HTTP *http.Client
	mu   sync.Mutex
	last time.Time
}

func NewClient() *Client { return &Client{HTTP: &http.Client{Timeout: httpTimeout}} }

func (c *Client) Tournament(ctx context.Context, id int64) (Tournament, error) {
	c.wait()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s%d", baseURL, id), nil)
	if err != nil {
		return Tournament{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Tournament{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Tournament{}, fmt.Errorf("rating: tournament %d: %s", id, resp.Status)
	}
	var body struct {
		Name      string       `json:"name"`
		DateStart string       `json:"dateStart"`
		DateEnd   string       `json:"dateEnd"`
		Editors   []sitePerson `json:"editors"`
		Credits   []struct {
			Questions []int `json:"questions"`
			Editors   []struct {
				Player sitePerson `json:"player"`
			} `json:"editors"`
		} `json:"credits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Tournament{}, err
	}
	t := Tournament{Title: strings.TrimSpace(body.Name), Start: day(body.DateStart), End: day(body.DateEnd)}
	for _, e := range body.Editors {
		t.Editors = append(t.Editors, e.person())
	}
	for _, c := range body.Credits {
		if len(c.Questions) != 2 {
			continue
		}
		tour := Tour{From: c.Questions[0], To: c.Questions[1]}
		for _, e := range c.Editors {
			tour.Editors = append(tour.Editors, e.Player.person())
		}
		t.Tours = append(t.Tours, tour)
	}
	return t, nil
}

type sitePerson struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Surname string `json:"surname"`
}

func (p sitePerson) person() Person {
	return Person{ID: p.ID, Name: strings.TrimSpace(p.Name + " " + p.Surname)}
}

// wait keeps one request at least minInterval after the previous one.
func (c *Client) wait() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := time.Until(c.last.Add(minInterval)); d > 0 {
		time.Sleep(d)
	}
	c.last = time.Now()
}

func day(ts string) string {
	if len(ts) < dayLen {
		return ""
	}
	return ts[:dayLen]
}

// Fixed is a Source of known tournaments, for tests.
type Fixed map[int64]Tournament

func (f Fixed) Tournament(_ context.Context, id int64) (Tournament, error) {
	t, ok := f[id]
	if !ok {
		return Tournament{}, fmt.Errorf("rating: tournament %d: not found", id)
	}
	return t, nil
}

// Cached remembers what a Source said for a while, so a form checked on
// every change does not ask the site each time.
type Cached struct {
	Src Source
	TTL time.Duration
	mu  sync.Mutex
	got map[int64]cachedTournament
}

type cachedTournament struct {
	t  Tournament
	at time.Time
}

func (c *Cached) Tournament(ctx context.Context, id int64) (Tournament, error) {
	c.mu.Lock()
	if e, ok := c.got[id]; ok && time.Since(e.at) < c.TTL {
		c.mu.Unlock()
		return e.t, nil
	}
	c.mu.Unlock()
	t, err := c.Src.Tournament(ctx, id)
	if err != nil {
		return t, err
	}
	c.mu.Lock()
	if c.got == nil {
		c.got = map[int64]cachedTournament{}
	}
	c.got[id] = cachedTournament{t, time.Now()}
	c.mu.Unlock()
	return t, nil
}
