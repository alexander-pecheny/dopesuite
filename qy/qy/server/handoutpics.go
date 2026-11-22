package qyserver

import (
	"context"
	"database/sql"
	"net/url"
	"path"
	"regexp"
	"strings"

	"qy/qy/domain/question"
	"qy/qy/storage/store"
)

// A picture the handout only links to (gotquestions' copy, a picture in a
// telegram post) becomes a file of the question, the way an admin attaches
// one, and its line leaves the handout: the card draws the file in the
// handout's place. The background pass does one link a step. A link that
// does not give a picture stays as it is, and is not tried again until the
// server restarts.

var loneLinkRe = regexp.MustCompile(`^https?://\S+$`)

// handoutLinks are the handout's lines that are a link and nothing else.
func handoutLinks(q question.Question) []string {
	handout, _ := question.SplitHandout(q.Text)
	var out []string
	for _, line := range strings.Split(handout, "\n") {
		if line = strings.TrimSpace(line); loneLinkRe.MatchString(line) {
			out = append(out, line)
		}
	}
	return out
}

// withoutHandoutLine is q's text with one line of its handout gone.
func withoutHandoutLine(text, link string) string {
	handout, rest := question.SplitHandout(text)
	var kept []string
	for _, line := range strings.Split(handout, "\n") {
		if strings.TrimSpace(line) != link {
			kept = append(kept, line)
		}
	}
	return question.WithHandout(rest, strings.TrimSpace(strings.Join(kept, "\n")))
}

// keepPictureOne fetches the first handout link not yet tried, and reports
// whether there was one.
func (s *server) keepPictureOne(ctx context.Context) (bool, error) {
	id, link, err := s.nextHandoutLink(ctx)
	if err != nil || link == "" {
		return false, err
	}
	pic, err := s.pics.Fetch(ctx, link)
	// A link that gave no picture keeps why, for a debugger to read.
	s.picsTried.Store(link, err)
	if err != nil {
		return true, nil
	}
	err = s.withWriteTx(ctx, "handout-picture", func(ctx context.Context, tx *sql.Tx) error {
		// The admin may have edited the question while it was fetched.
		q, err := store.GetQuestion(ctx, tx, id)
		if err != nil || !hasLink(handoutLinks(q.Clean), link) {
			return err
		}
		if err := store.AddFile(ctx, tx, store.File{QuestionID: id, Name: pictureName(link, pic.ContentType), ContentType: pic.ContentType}, pic.Data, s.stamp()); err != nil {
			return err
		}
		q.Clean.Text = withoutHandoutLine(q.Clean.Text, link)
		return store.SaveQuestion(ctx, tx, q, s.stamp())
	})
	return true, err
}

func (s *server) nextHandoutLink(ctx context.Context) (int64, string, error) {
	rows, err := s.db.QueryContext(ctx, `
select id, clean from questions where merged_into is null and json_extract(clean, '$.text') like '%http%' order by id`)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var clean string
		if err := rows.Scan(&id, &clean); err != nil {
			return 0, "", err
		}
		for _, link := range handoutLinks(question.FromJSON(clean)) {
			if _, tried := s.picsTried.Load(link); !tried {
				return id, link, nil
			}
		}
	}
	return 0, "", rows.Err()
}

func hasLink(links []string, link string) bool {
	for _, l := range links {
		if l == link {
			return true
		}
	}
	return false
}

// pictureName is the file's name: the link's last part, with the right
// extension.
func pictureName(link, contentType string) string {
	name := "handout"
	if u, err := url.Parse(link); err == nil && path.Base(u.Path) != "/" && path.Base(u.Path) != "." {
		name = strings.TrimSuffix(path.Base(u.Path), path.Ext(u.Path))
	}
	return name + "." + store.ImageTypes[contentType]
}
