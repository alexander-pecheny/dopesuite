package tgbot

import (
	"context"
	"strings"
	"testing"
)

func TestErrorsLeaveTheTokenOut(t *testing.T) {
	const token = "123456:secret-token-part"
	c := New(Config{Token: token, APIBase: "http://127.0.0.1:1"})
	_, err := c.GetUpdates(context.Background(), 0)
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error carries the token: %v", err)
	}
	if !strings.Contains(err.Error(), redactedToken) {
		t.Fatalf("error lost the redacted URL: %v", err)
	}
}
