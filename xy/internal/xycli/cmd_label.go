package xycli

import (
	"encoding/json"
	"errors"
	"strings"
)

// Labels. An assignment may be scoped to a Playing (what the testers thought at
// that sitting); the CLI writes unscoped ones only — the author's own view —
// since it does not deal in Test Sessions.

func cmdLabel(a *app, args []string) error {
	return dispatch("label", map[string]func(*app, []string) error{
		"ls": labelList, "add": labelAdd, "assign": labelAssign,
	}, a, args)
}

func labelList(a *app, args []string) error {
	fs := a.flags("label ls", "Метки доски; с --card — метки одной карточки.")
	board := a.boardFlag(fs)
	card := fs.Int64("card", 0, "показать метки этой карточки")
	_, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	_, b, err := a.open(*board)
	if err != nil {
		return err
	}
	type row struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Color string `json:"color"`
		On    bool   `json:"on_card,omitempty"`
	}
	assigned := map[int64]bool{}
	for _, assignment := range b.CardLabels {
		if *card != 0 && assignment.CardID == *card {
			assigned[assignment.LabelID] = true
		}
	}
	rows := []row{}
	for _, l := range b.Labels {
		if *card != 0 && !assigned[l.ID] {
			continue
		}
		rows = append(rows, row{ID: l.ID, Name: l.Name, Color: l.Color, On: assigned[l.ID]})
	}
	return a.emit(rows, func() {
		for _, r := range rows {
			a.printf("%6d  %-24s %s\n", r.ID, r.Name, r.Color)
		}
	})
}

func labelAdd(a *app, args []string) error {
	fs := a.flags("label add", "Создать метку на доске.")
	board := a.boardFlag(fs)
	name := fs.String("name", "", "название метки")
	color := fs.String("color", "#8ec7ff", "цвет метки (hex)")
	_, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		return errors.New("нужен --name")
	}
	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	nameEnc, err := b.DK.EncField(*name)
	if err != nil {
		return err
	}
	colorEnc, err := b.DK.EncField(*color)
	if err != nil {
		return err
	}
	id, err := c.CreateLabel(b.ID, nameEnc, colorEnc)
	if err != nil {
		return err
	}
	return a.emit(map[string]any{"id": id}, func() { a.printf("метка %d «%s» создана\n", id, *name) })
}

func labelAssign(a *app, args []string) error {
	fs := a.flags("label assign", "xy-cli label assign <id карточки> --board B --label «готово» [--remove]")
	board := a.boardFlag(fs)
	label := fs.String("label", "", "метка: id или название")
	remove := fs.Bool("remove", false, "снять метку вместо того, чтобы поставить")
	rest, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || *label == "" {
		return errors.New("нужен id карточки и --label")
	}
	cardID, err := parseID(rest[0], "карточка")
	if err != nil {
		return err
	}
	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	if _, err := b.Card(cardID); err != nil {
		return err
	}
	target, err := b.Label(*label)
	if err != nil {
		return err
	}

	var next []cardLabelDTO
	had := false
	for _, assignment := range b.CardLabels {
		if assignment.CardID != cardID {
			continue
		}
		if assignment.LabelID == target.ID && assignment.SessionID == nil {
			had = true
			if *remove {
				continue
			}
		}
		next = append(next, assignment)
	}
	if !*remove && !had {
		next = append(next, cardLabelDTO{CardID: cardID, LabelID: target.ID})
	}
	if *remove && !had {
		return a.emit(map[string]any{"card_id": cardID, "label_id": target.ID, "changed": false}, func() {
			a.printf("метка «%s» и так не стоит\n", target.Name)
		})
	}
	if !*remove && had {
		return a.emit(map[string]any{"card_id": cardID, "label_id": target.ID, "changed": false}, func() {
			a.printf("метка «%s» и так стоит\n", target.Name)
		})
	}

	// The metadata trail: the лента says which label came and went, in the same
	// shape the browser writes.
	eventType := "label_add"
	if *remove {
		eventType = "label_remove"
	}
	payload, err := json.Marshal(map[string]any{"label": target.Name, "label_id": target.ID})
	if err != nil {
		return err
	}
	payloadEnc, err := b.DK.EncField(string(payload))
	if err != nil {
		return err
	}
	events := []map[string]any{{"type": eventType, "payload_enc": payloadEnc}}
	if err := c.SetCardLabels(cardID, next, events); err != nil {
		return err
	}
	verb := "поставлена"
	if *remove {
		verb = "снята"
	}
	return a.emit(map[string]any{"card_id": cardID, "label_id": target.ID, "changed": true}, func() {
		a.printf("метка «%s» %s на карточке %d\n", strings.TrimSpace(target.Name), verb, cardID)
	})
}
