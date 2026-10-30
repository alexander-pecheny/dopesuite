package dopeserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"dope/dope/domain/contested"
	"dope/dope/domain/core"
	"dope/dope/domain/edit"
	"dope/dope/domain/games"
	"dope/dope/web/route"
)

// A Game's contested answers, with their text: what the hosts' suggest list and the
// jury's tab read. Without the fest's switch on there are none.
func (s *server) scopedContested(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	list := []contested.Answer{}
	if on, err := contested.Enabled(r.Context(), s.eng.DB, sc.FestID); err != nil {
		return err
	} else if on {
		if list, err = contested.List(r.Context(), s.eng.DB, sc.FestID, sc.GameID); err != nil {
			return err
		}
	}
	return route.JSON(w, list)
}

// contestedRequest is one contested answer: the question (from 0) and the team's
// number in this Game for a host's write, the question and the answer for a
// ruling. A save may name several teams in Numbers, since teams often give
// the same answer; they are written together or not at all.
type contestedRequest struct {
	Question int     `json:"question"`
	Number   int64   `json:"number"`
	Numbers  []int64 `json:"numbers"`
	Answer   string  `json:"answer"`
	Status   string  `json:"status"`
}

func (s *server) scopedContestedSave(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	return s.contestedWrite(w, r, sc, func(ctx context.Context, tx *sql.Tx, req contestedRequest) error {
		numbers := req.Numbers
		if len(numbers) == 0 {
			numbers = []int64{req.Number}
		}
		for _, number := range numbers {
			if err := contested.SaveTx(ctx, tx, sc.FestID, sc.GameID, sc.User.UserID, sc.SheetVenues, req.Question, number, req.Answer); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *server) scopedContestedDelete(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	return s.contestedWrite(w, r, sc, func(ctx context.Context, tx *sql.Tx, req contestedRequest) error {
		return contested.DeleteTx(ctx, tx, sc.FestID, sc.GameID, sc.SheetVenues, req.Question, req.Number)
	})
}

func (s *server) scopedContestedRule(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	return s.contestedWrite(w, r, sc, func(ctx context.Context, tx *sql.Tx, req contestedRequest) error {
		return contested.RuleTx(ctx, tx, sc.GameID, sc.User.UserID, req.Question, req.Answer, req.Status)
	})
}

// contestedWrite runs one change to the contested answers, writes them into the Game's
// document and sends it to the open pages, and answers with the whole list.
func (s *server) contestedWrite(w http.ResponseWriter, r *http.Request, sc route.Scope,
	apply func(ctx context.Context, tx *sql.Tx, req contestedRequest) error) error {
	var req contestedRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	_, err := s.pageServer().Commit(r.Context(), sc.FestID, "contested", nil, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		write := core.FestWrite{Event: "contested", Payload: req}
		if on, err := contested.Enabled(ctx, tx, sc.FestID); err != nil {
			return write, err
		} else if !on {
			return write, route.NotFound
		}
		if err := apply(ctx, tx, req); err != nil {
			return write, err
		}
		state, err := contested.RefreshTx(ctx, tx, sc.FestID, sc.GameID)
		if state != nil {
			write.Broadcast.States = []core.GameState{{GameID: sc.GameID, StateJSON: state}}
		}
		return write, err
	})
	if errors.Is(err, contested.ErrOtherVenue) {
		return route.ErrOtherVenue
	}
	if err != nil {
		return err
	}
	return s.scopedContested(w, r, sc)
}

// keepContested refuses a patch that writes the contested answers: only the server
// writes them, from its tables.
func keepContested(req edit.PatchRequest) error {
	for _, op := range req.Ops {
		var head string
		if len(op.Path) > 0 && json.Unmarshal(op.Path[0], &head) == nil && head == games.ContestedKey {
			return route.BadRequest("contested is written by its own endpoint")
		}
	}
	return nil
}
