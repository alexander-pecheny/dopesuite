package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	corei18n "pecheny.me/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"
)

// RatingAPI is the endpoint StatsAdder.get_tournament_results calls.
const RatingAPI = "https://api.rating.chgk.net"

// Fetch reads the results of every tournament in a comma-separated list of
// rating.chgk.info ids — two of them when a synchronous package also ran async.
func Fetch(ctx context.Context, ids string) ([]Result, error) {
	var out []Result
	for _, id := range strings.Split(ids, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		results, err := fetchTournament(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, results...)
	}
	return out, nil
}

func fetchTournament(ctx context.Context, id string) ([]Result, error) {
	url := fmt.Sprintf("%s/tournaments/%s/results.json?includeMasksAndControversials=1", RatingAPI, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, corei18n.User(xystrings.Default.Stats.Rating.Status(id, resp.Status))
	}
	var body []struct {
		Mask    string `json:"mask"`
		Current struct {
			Name string `json:"name"`
		} `json:"current"`
		Team struct {
			ID int `json:"id"`
		} `json:"team"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, corei18n.User(xystrings.Default.Stats.Rating.Decode(id, err.Error()))
	}
	out := make([]Result, 0, len(body))
	for _, r := range body {
		out = append(out, Result{TeamID: r.Team.ID, Name: r.Current.Name, Mask: r.Mask})
	}
	return out, nil
}
