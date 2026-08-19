package server

import (
	"net/http"
	"testing"
)

// A child named in the path that is not there is a 404; one named in the body
// that is missing or on another board is a 400, and a PATCH that touches
// several columns lands as one row write.
func TestChildAccessAndPatch(t *testing.T) {
	c, board, listID := boardWithList(t)
	resp := c.do("PATCH", "/api/lists/999999", map[string]string{"rank": "z"})
	mustStatus(t, resp, http.StatusNotFound)

	resp = c.do("POST", "/api/lists/"+listID+"/cards", map[string]string{"description_enc": enc("q"), "rank": "m"})
	mustStatus(t, resp, 200)
	var card struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &card)
	cardID := itoa(card.ID)

	resp = c.do("PATCH", "/api/cards/"+cardID, map[string]any{"list_id": 999999})
	mustStatus(t, resp, http.StatusBadRequest)

	resp = c.do("POST", "/api/boards", map[string]string{
		"name": "other", "kdf_salt": enc("s"),
		"kdf_params": `{"kdf":"scrypt","N":1,"r":1,"p":1}`, "wrapped_key": enc("w"), "verify_token": enc("v"),
	})
	mustStatus(t, resp, 200)
	var other struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &other)
	resp = c.do("POST", "/api/boards/"+itoa(other.ID)+"/lists", map[string]string{"title_enc": enc("t"), "rank": "a"})
	mustStatus(t, resp, 200)
	var foreign struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &foreign)
	resp = c.do("PATCH", "/api/cards/"+cardID, map[string]any{"list_id": foreign.ID})
	mustStatus(t, resp, http.StatusBadRequest)

	resp = c.do("PATCH", "/api/cards/"+cardID, map[string]any{"rank": "k", "kind": "question", "alias_enc": enc("a")})
	mustStatus(t, resp, http.StatusNoContent)
	resp = c.do("GET", "/api/boards/"+board, nil)
	mustStatus(t, resp, 200)
	var snap struct {
		Cards []cardDTO `json:"cards"`
	}
	c.decode(resp, &snap)
	if len(snap.Cards) != 1 || snap.Cards[0].Rank != "k" || snap.Cards[0].Kind != "question" || snap.Cards[0].AliasEnc == nil {
		t.Fatalf("patched card = %+v", snap.Cards)
	}
}
