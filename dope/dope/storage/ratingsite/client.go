// Package ratingsite is rating.chgk.info's API as dope uses it: the reads
// the import, the team editor and the export share, and the export's writes
// through the site's «ввод» (ADR-0036).
package ratingsite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBase is the site's API. The server tests point it at a fake, so
// none of them reaches the network.
var DefaultBase = "https://api.rating.chgk.info"

// TournamentPage is a tournament's page on the site, for people.
const TournamentPage = "https://rating.chgk.info/tournament/%d"

const timeout = 30 * time.Second

// Client asks the site, as the user whose token it carries when it has one.
type Client struct {
	// Base is the API's address; empty is DefaultBase.
	Base  string
	Token string
	HTTP  *http.Client
}

// Error is the site's refusal, in the site's own words.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("%d: %s", e.Status, e.Message) }

// NotFound reports whether err is the site saying it has no such thing.
func NotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

func (c Client) get(ctx context.Context, path string, into any) error {
	return c.do(ctx, http.MethodGet, path, nil, into)
}

func (c Client) post(ctx context.Context, path string, body, into any) error {
	return c.do(ctx, http.MethodPost, path, body, into)
}

func (c Client) do(ctx context.Context, method, path string, body, into any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	base := c.Base
	if base == "" {
		base = DefaultBase
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return &Error{Status: resp.StatusCode, Message: refusal(raw, resp.Status)}
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(raw, into)
}

// refusal is what the site said in a refused answer: each violation, else
// its detail, else the bare status.
func refusal(raw []byte, status string) string {
	var problem struct {
		Detail     string `json:"detail"`
		Violations []struct {
			PropertyPath string `json:"propertyPath"`
			Message      string `json:"message"`
		} `json:"violations"`
	}
	if json.Unmarshal(raw, &problem) != nil {
		return status
	}
	var lines []string
	for _, v := range problem.Violations {
		if v.PropertyPath != "" {
			lines = append(lines, v.PropertyPath+": "+v.Message)
		} else {
			lines = append(lines, v.Message)
		}
	}
	if len(lines) > 0 {
		return strings.Join(lines, "\n")
	}
	if problem.Detail != "" {
		return problem.Detail
	}
	return status
}
