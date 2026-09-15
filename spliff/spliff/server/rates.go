package spliffserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"spliff/spliff/domain/money"
	"spliff/spliff/domain/rates"
	"spliff/spliff/storage/store"
)

// The Rate table service: one fetch per calendar day (UTC) from
// open.er-api.com, kept forever, and read back by nearest day. A failed fetch
// changes nothing — the previous table stays in use, which is the whole point
// of the nearest-day rule (spliff/docs/adr/0001).

// RateSource is where the day's table comes from. The free endpoint has no
// history, so there is exactly one URL and it always means "today".
const RateSource = "https://open.er-api.com/v6/latest/USD"

// retryAfterFailure is how long a failed fetch keeps us from trying again. The
// source publishes once a day; hammering it on every page view after an outage
// would turn one bad hour into a rate-limit ban.
const retryAfterFailure = 10 * time.Minute

type rateService struct {
	s      *server
	client *http.Client
	url    string

	mu       sync.Mutex
	lastFail time.Time
	now      func() time.Time
}

func newRateService(s *server, client *http.Client) *rateService {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &rateService{s: s, client: client, url: RateSource, now: time.Now}
}

// today is the UTC calendar day a fetch is filed under.
func (rs *rateService) today() string { return rs.now().UTC().Format(rates.DayFormat) }

// Start runs the daily ticker. It fires shortly after each UTC midnight, which
// is when the source has a new table; the fetch-on-first-need path covers the
// case where the server was down at the time.
func (rs *rateService) Start(ctx context.Context) {
	go func() {
		for {
			now := rs.now().UTC()
			next := now.Truncate(24 * time.Hour).Add(24*time.Hour + 5*time.Minute)
			select {
			case <-ctx.Done():
				return
			case <-time.After(next.Sub(now)):
				if err := rs.Ensure(ctx); err != nil {
					log.Printf("rates: daily fetch: %v", err)
				}
			}
		}
	}()
}

// Ensure fetches today's table if there is not one already. It is what every
// read of a Group calls first, and what the daily ticker calls; a failure is
// reported but never fatal, because the nearest existing table is still a
// perfectly good answer.
func (rs *rateService) Ensure(ctx context.Context) error {
	day := rs.today()
	have, err := store.HasRateTable(ctx, rs.s.db, day)
	if err != nil {
		return err
	}
	if have {
		return nil
	}
	rs.mu.Lock()
	if rs.now().Sub(rs.lastFail) < retryAfterFailure {
		rs.mu.Unlock()
		return nil
	}
	rs.mu.Unlock()

	table, err := rs.fetch(ctx)
	if err != nil {
		rs.mu.Lock()
		rs.lastFail = rs.now()
		rs.mu.Unlock()
		return err
	}
	now := rfc3339(rs.now())
	return rs.s.withWriteTx(ctx, "save-rate-table", func(ctx context.Context, tx *sql.Tx) error {
		return store.SaveRateTable(ctx, tx, day, rs.url, table, now)
	})
}

// rateResponse is the slice of open.er-api.com's answer we read. The rates
// arrive as JSON numbers and are kept as the literal text they were written
// with: a rate that has been through a float64 is a rate that has lost digits.
type rateResponse struct {
	Result string                 `json:"result"`
	Base   string                 `json:"base_code"`
	Rates  map[string]json.Number `json:"rates"`
}

func (rs *rateService) fetch(ctx context.Context) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rs.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := rs.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rates: %s answered %d", rs.url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return ParseRates(body)
}

// ParseRates reads the source's answer into a Rate table. It is exported so the
// fetcher can be checked against a saved response rather than against the
// network — no test in this module ever opens a socket.
func ParseRates(body []byte) (map[string]string, error) {
	var parsed rateResponse
	dec := json.NewDecoder(newReader(body))
	dec.UseNumber()
	if err := dec.Decode(&parsed); err != nil {
		return nil, err
	}
	if parsed.Result != "" && parsed.Result != "success" {
		return nil, fmt.Errorf("rates: source answered %q", parsed.Result)
	}
	if money.Normalise(parsed.Base) != "USD" {
		return nil, fmt.Errorf("rates: source is based on %q, not USD", parsed.Base)
	}
	if len(parsed.Rates) == 0 {
		return nil, fmt.Errorf("rates: source sent no rates")
	}
	out := make(map[string]string, len(parsed.Rates))
	for code, rate := range parsed.Rates {
		code = money.Normalise(code)
		if !money.ValidCode(code) {
			continue
		}
		out[code] = rate.String()
	}
	if _, ok := out["USD"]; !ok {
		return nil, fmt.Errorf("rates: source sent no USD rate")
	}
	return out, nil
}

// Book is every Rate table the ledger may need for one read, loaded lazily: the
// list of days up front (one small query), each day's table the first time a
// Transaction asks for it.
type Book struct {
	days   []string
	tables map[string]rates.Table
	load   func(day string) (map[string]string, error)
}

// Book reads the days a table exists for, after making sure today's is among
// them if it can be. A fetch that fails is logged and forgotten: the nearest
// table still answers.
func (rs *rateService) Book(ctx context.Context) (*Book, error) {
	if err := rs.Ensure(ctx); err != nil {
		log.Printf("rates: %v (falling back on the nearest table we have)", err)
	}
	days, err := store.RateDays(ctx, rs.s.db)
	if err != nil {
		return nil, err
	}
	return &Book{
		days:   days,
		tables: map[string]rates.Table{},
		load:   func(day string) (map[string]string, error) { return store.RateTable(ctx, rs.s.db, day) },
	}, nil
}

// For is the Rate table a Transaction dated `day` converts with: its own day's
// when there is one, else the nearest, the earlier one on a tie.
func (b *Book) For(day string) (rates.Table, error) {
	resolved, ok := rates.ResolveDay(b.days, day)
	if !ok {
		return rates.Table{}, rates.ErrNoTable
	}
	if t, cached := b.tables[resolved]; cached {
		return t, nil
	}
	raw, err := b.load(resolved)
	if err != nil {
		return rates.Table{}, err
	}
	t := rates.Table{Day: resolved, Rates: raw}
	b.tables[resolved] = t
	return t, nil
}

// Empty reports that there is no Rate table at all — a fresh instance before
// its first fetch, and nothing else. A Group in that state still lists its
// Transactions; it just cannot state a balance yet.
func (b *Book) Empty() bool { return len(b.days) == 0 }

// Currencies is every code the newest table carries, for the Group's
// base-currency picker: the set the source actually publishes, not a list of
// our own that would go stale.
func (b *Book) Currencies() ([]string, error) {
	if len(b.days) == 0 {
		return nil, nil
	}
	t, err := b.For(b.days[len(b.days)-1])
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(t.Rates))
	for code := range t.Rates {
		out = append(out, code)
	}
	return out, nil
}
