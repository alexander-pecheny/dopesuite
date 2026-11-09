package qyserver

import (
	"context"
	"net/http"
	"strconv"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"

	"qy/qy/domain/contest"
	"qy/qy/storage/store"
	"qy/qy/web/route"
	"qy/qy/web/ui"
)

// ---- who nominated what ----

// nominatorRow is one person's nominations: who they are and their slots in
// order.
type nominatorRow struct {
	name, handle string
	rating       int64
	first        string
	slots        []nominatedSlot
}

type nominatedSlot struct {
	slot       int
	questionID int64
	appeal     string
}

// nominatorRows are the year's nominators in the order they first nominated,
// the order the nomination rule counts them in.
func (s *server) nominatorRows(ctx context.Context, year int) ([]nominatorRow, error) {
	rows, err := s.db.QueryContext(ctx, `
select n.user_id, coalesce(u.telegram_name, ''), coalesce(u.telegram_username, u.username, ''), coalesce(u.rating_id, 0),
  f.first, n.slot, n.question_id, n.appeal
from nominations n
join users u on u.id = n.user_id
join (select user_id, min(created_at) first, min(id) first_id from nominations where year = ? group by user_id) f on f.user_id = n.user_id
where n.year = ?
order by f.first, f.first_id, n.slot`, year, year)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []nominatorRow
	var last int64
	for rows.Next() {
		var user int64
		var r nominatorRow
		var sl nominatedSlot
		if err := rows.Scan(&user, &r.name, &r.handle, &r.rating, &r.first, &sl.slot, &sl.questionID, &sl.appeal); err != nil {
			return nil, err
		}
		if len(out) == 0 || user != last {
			out = append(out, r)
			last = user
		}
		out[len(out)-1].slots = append(out[len(out)-1].slots, sl)
	}
	return out, rows.Err()
}

func (s *server) handleNominations(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	people, err := s.nominatorRows(ctx, c.Year)
	if err != nil {
		return err
	}
	qs, err := store.Questions(ctx, s.db, c.Year, "id in (select question_id from nominations where year = ?)", c.Year)
	if err != nil {
		return err
	}
	byID := make(map[int64]store.Question, len(qs))
	for _, q := range qs {
		byID[q.ID] = q
	}
	n := 0
	head := []ui.Item{ui.Hcell(text(str.Admin.Nominations.Who()))}
	for slot := 1; slot <= c.MainSlots+c.ReserveSlots; slot++ {
		head = append(head, ui.Hcell(text(slotLabel(c, slot))))
	}
	rows := []ui.Item{ui.Scroll(), ui.Trow(head...)}
	for _, p := range people {
		n += len(p.slots)
		rows = append(rows, ui.Trow(append([]ui.Item{ui.Cell(nominatorHead(p)...)}, s.slotCells(c, p, byID)...)...))
	}
	y := strconv.Itoa(c.Year)
	body := []ui.Item{ui.Section(hint(str.Admin.Nominations.Empty()))}
	if len(people) > 0 {
		body = []ui.Item{ui.Section(ui.Data("nominations", ""), hint(str.Admin.Nominations.Count(len(people), n)), ui.Table(rows...))}
	}
	return s.ok(w, r, view{
		title: str.Admin.Contest.Nominations(),
		trail: adminTrail(crumb{contestHref(c.Year), str.Admin.Contests.Row(y)}, crumb{"", str.Admin.Contest.Nominations()}),
		body:  body,
	})
}

// nominatorHead is who the person is: their name, their handle, their rating
// ID and when they first nominated.
func nominatorHead(p nominatorRow) []ui.Item {
	var out []ui.Item
	if p.name != "" {
		out = append(out, ui.Strong(text(p.name)))
	}
	if p.handle != "" {
		out = append(out, ui.Row(ui.Button(ui.Href("https://t.me/"+p.handle), ui.Ghost, ui.Small(), text("@"+p.handle))))
	}
	if p.rating > 0 {
		id := idstr.Format(p.rating)
		out = append(out, ui.Row(ui.Button(ui.Href("https://rating.chgk.info/player/"+id), ui.Ghost, ui.Small(), ui.IconLink,
			text(str.Admin.Nominations.Rating(id)))))
	} else {
		out = append(out, hint(str.Admin.Nominations.NoRating()))
	}
	return append(out, hint(str.Admin.Nominations.First(sinceStamp(p.first))))
}

// slotCells are the person's slots, one cell each, empty where they left the
// slot empty. A question is shown as nominated: the text they gave, before
// the anonymiser and the admins touched it.
func (s *server) slotCells(c contest.Contest, p nominatorRow, byID map[int64]store.Question) []ui.Item {
	cells := make([]ui.Item, c.MainSlots+c.ReserveSlots)
	for i := range cells {
		cells[i] = ui.Cell()
	}
	for _, sl := range p.slots {
		if sl.slot < 1 || sl.slot > len(cells) {
			continue
		}
		q := byID[sl.questionID]
		items := []ui.Item{ui.Row(ui.Button(ui.Href(questionHref(q.ID)), ui.Ghost, ui.Small(),
			text(str.Admin.Nominations.Slot(questionName(q), statusName(q)))))}
		if q.Orig.Empty() {
			items = append(items, ui.Richtext(ui.Raw("<p>"+plain(q.SourceText)+"</p>")))
		} else {
			items = append(items, ui.Richtext(ui.Raw(questionHTML(card{qy: q.QY, q: q.Orig, tournament: q.TournamentTitle}))))
		}
		if sl.appeal != "" {
			items = append(items, hint(str.Admin.Nominations.Appeal(sl.appeal)))
		}
		cells[sl.slot-1] = ui.Cell(items...)
	}
	return cells
}
