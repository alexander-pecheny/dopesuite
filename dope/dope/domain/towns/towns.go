// Package towns answers which country a rating.chgk.info town is in, as the
// ISO-3166 alpha-2 code a flag is drawn from. buff mirrors every town nightly,
// so that is where the answer comes from; a town buff has not seen yet — one
// registered since its last run — is asked about directly, once.
package towns

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dope/dope/storage/buffdb"
	"dope/dope/storage/store"
)

const townURL = "https://api.rating.chgk.net/towns/"

// fetchLimit is how many towns one import will ask the rating site about. buff
// knows all but the newest, so a roster that goes over this is a sign that buff
// is stale rather than that the towns are new, and hammering the API would not
// fix that.
const fetchLimit = 25

// Resolver reads buff and, for what buff lacks, the rating site. With buff
// disabled it resolves nothing at all: every town would count as new, and a
// roster import would turn into a few hundred API calls.
type Resolver struct {
	buff   *buffdb.Store
	client *http.Client
}

func NewResolver(buff *buffdb.Store) *Resolver {
	return &Resolver{buff: buff, client: &http.Client{Timeout: 10 * time.Second}}
}

// Countries maps each town id to its country's ISO code. A town the rating site
// leaves without a country — Crimea, Abkhazia, South Ossetia, Karabakh — maps to
// the empty string, which reads as "no flag" rather than as a wrong one.
func (r *Resolver) Countries(ctx context.Context, townIDs []int64) map[int64]string {
	out := make(map[int64]string, len(townIDs))
	if r == nil || !r.buff.Enabled() {
		return out
	}
	fetched := 0
	for _, id := range townIDs {
		if id <= 0 {
			continue
		}
		if _, done := out[id]; done {
			continue
		}
		if iso, known := r.buff.TownCountry(ctx, id); known {
			out[id] = iso
			continue
		}
		if fetched >= fetchLimit {
			continue
		}
		fetched++
		if iso, ok := r.fetch(ctx, id); ok {
			out[id] = iso
		}
	}
	return out
}

// FestCityCountries is what the screen needs to draw flags for one fest: the
// ISO code of every city on its roster, keyed by the lowercased city. A team
// imported from rating.chgk.info carries its own country, resolved when it was
// imported; a city typed by hand is looked up in buff by name. A city neither
// knows is absent, and the page falls back to its own list.
func FestCityCountries(ctx context.Context, q store.Queryer, buff *buffdb.Store, festID int64) map[string]string {
	rows, err := q.QueryContext(ctx, `
select city, coalesce(country, '') from fest_teams
where fest_id = ? and deleted = 0 and trim(city) <> ''`, festID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]string{}
	var unresolved []string
	for rows.Next() {
		var city, iso string
		if err := rows.Scan(&city, &iso); err != nil {
			return out
		}
		key := strings.ToLower(strings.TrimSpace(city))
		if iso != "" {
			out[key] = iso
			continue
		}
		if _, done := out[key]; !done {
			unresolved = append(unresolved, city)
		}
	}
	for city, iso := range buff.TownCountries(ctx, unresolved) {
		if _, done := out[city]; !done {
			out[city] = iso
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type ratingTown struct {
	ID      int64 `json:"id"`
	Country *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"country"`
}

func (r *Resolver) fetch(ctx context.Context, townID int64) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, townURL+strconv.FormatInt(townID, 10), nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		return "", false
	}
	var town ratingTown
	if err := json.NewDecoder(resp.Body).Decode(&town); err != nil {
		return "", false
	}
	if town.Country == nil {
		return "", true
	}
	// buff mirrors the countries with their ISO codes, so dope never has to hold
	// an opinion about which code a country's name means.
	return r.buff.CountryISO(ctx, town.Country.ID), true
}
