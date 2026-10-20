package server

import (
	"context"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/stats"
	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"

	xystrings "xy/i18nstrings"
)

// The add-stats action: chgksuite's add_stats for a tour that lives in cards. The
// page copies the tour and appends a line to each question card's comment;
// this hands it the lines, one per question in order, from the results on the
// tournament site or from a csv/xlsx the reader uploads. Nothing is stored.

const (
	maxStatsUpload = 16 << 20
	maxStatsTable  = 64 << 20
	statsTimeout   = 2 * time.Minute
	maxStatsCount  = 1000
	maxTeamsToName = 99
)

// ratingIDs is what the tournament field may hold: ids, comma-separated.
var ratingIDs = regexp.MustCompile(`^\s*\d+(\s*,\s*\d+)*\s*$`)

type statsResponse struct {
	Lines    []string `json:"lines"`
	Warnings []string `json:"warnings"`
}

func (s *server) handleStatsLines(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxStatsUpload)
	form, err := readMultipart(r, maxStatsUpload)
	if err != nil {
		handleUser(w, err)
		return
	}
	count, err := strconv.Atoi(form.Value("count"))
	if err != nil || count < 0 || count > maxStatsCount {
		httpError(w, http.StatusBadRequest, "bad count")
		return
	}
	opts, err := statsOptions(form)
	if err != nil {
		handleUser(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), statsTimeout)
	defer cancel()
	results, warnings, err := s.statsResults(ctx, form)
	if err == nil {
		var resp statsResponse
		if resp.Lines, err = stats.Lines(results, opts, count); err == nil {
			resp.Warnings = append([]string{}, warnings...)
			writeJSON(w, resp)
			return
		}
	}
	handleUser(w, err)
}

func statsOptions(form *memForm) (stats.Options, error) {
	opts := stats.DefaultOptions()
	opts.QuestionRange = strings.TrimSpace(form.Value("question_range"))
	if v := strings.TrimSpace(form.Value("team_naming_threshold")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > maxTeamsToName {
			return opts, corei18n.User(xystrings.Default.Server.Stats.BadThreshold())
		}
		opts.TeamNamingThreshold = n
	}
	return opts, nil
}

// statsResults reads the results from the site or from the uploaded table,
// whichever the form names.
func (s *server) statsResults(ctx context.Context, form *memForm) ([]stats.Result, []string, error) {
	if files := form.Files("results"); len(files) > 0 {
		f := files[0]
		if strings.EqualFold(filepath.Ext(f.Filename), ".xlsx") && !zipWithin(f.Data, maxStatsTable) {
			return nil, nil, corei18n.User(xystrings.Default.Server.Stats.BadFile())
		}
		results, warnings, err := stats.ReadUpload(f.Filename, f.Data)
		if _, isUser := corei18n.AsUser(err); err != nil && !isUser {
			err = corei18n.User(xystrings.Default.Server.Stats.BadFile())
		}
		return results, warnings, err
	}
	ids := form.Value("rating_ids")
	if strings.TrimSpace(ids) == "" {
		return nil, nil, corei18n.User(xystrings.Default.Server.Stats.NoSource())
	}
	if !ratingIDs.MatchString(ids) {
		return nil, nil, corei18n.User(xystrings.Default.Server.Stats.BadIds())
	}
	results, err := stats.FetchFrom(ctx, s.ratingBase(), ids)
	if _, isUser := corei18n.AsUser(err); err != nil && !isUser {
		err = corei18n.User(xystrings.Default.Server.Stats.FetchFailed(err.Error()))
	}
	return results, nil, err
}
