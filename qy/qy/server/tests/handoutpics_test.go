package tests

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"qy/qy/platform/picture"
)

// A picture the handout only links to is kept as a file of the question,
// and its line leaves the handout; a link that gives no picture stays.
func TestAHandoutPictureIsKeptOnTheSite(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	pic := "https://pics.example.org/1/pic_1.png"
	page := "https://drive.google.com/file/d/abc/view"
	w.ts.SetPictures(picture.Fixed{pic: {ContentType: "image/png", Data: png}})
	w.post(w.user("alice"), "/2026/nominate", slots("101", map[int]string{
		1: pasted("[Раздаточный материал: Стальной клинок.\n"+pic+"]\nЧему посвящена песня?", "мечу", "Алиса"),
		2: pasted("[Раздаточный материал: "+page+"]\nНазовите ИКС.", "икс", "Алиса"),
	}), http.StatusSeeOther)
	w.ts.ReviewPending()

	var text, name string
	song, other := w.questionID("мечу"), w.questionID("икс")
	w.ts.DB().QueryRow(`select json_extract(clean, '$.text') from questions where id = ?`, song).Scan(&text)
	if strings.Contains(text, pic) || !strings.Contains(text, "Стальной клинок.") {
		t.Errorf("the picture's link should leave the handout, and its words stay: %q", text)
	}
	if err := w.ts.DB().QueryRow(`select name from question_files where question_id = ?`, song).Scan(&name); err != nil || name != "pic_1.png" {
		t.Errorf("the picture should be a file of the question: %q, %v", name, err)
	}
	if !strings.Contains(w.get(w.admin, "/admin/question/"+strconv.FormatInt(song, 10), http.StatusOK), "/nominate/file/") {
		t.Error("the card should draw the kept picture")
	}
	w.ts.DB().QueryRow(`select json_extract(clean, '$.text') from questions where id = ?`, other).Scan(&text)
	if !strings.Contains(text, page) || w.count(`select count(*) from question_files where question_id = `+strconv.FormatInt(other, 10)) != 0 {
		t.Errorf("a link that is not a picture stays as it is: %q", text)
	}
}
