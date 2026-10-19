// Package openrouter asks a chat model one question through openrouter.ai, the
// one door qy uses for the anonymisation pass whatever model sits behind it.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	endpoint    = "https://openrouter.ai/api/v1/chat/completions"
	httpTimeout = 2 * time.Minute
	maxBody     = 1 << 20
)

type Client struct {
	Key, Model string
	HTTP       *http.Client
}

func New(key, model string) *Client {
	return &Client{Key: key, Model: model, HTTP: &http.Client{Timeout: httpTimeout}}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Complete sends a system and a user message and answers the model's reply.
func (c *Client) Complete(ctx context.Context, system, user string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"model":       c.Model,
		"messages":    []message{{"system", system}, {"user", user}},
		"temperature": 0,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openrouter: %s: %s", resp.Status, raw)
	}
	var out struct {
		Choices []struct {
			Message message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", errors.New("openrouter: no choices in the reply")
	}
	return out.Choices[0].Message.Content, nil
}
