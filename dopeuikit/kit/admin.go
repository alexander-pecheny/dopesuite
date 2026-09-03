package kit

import (
	"strconv"
	"strings"
	"time"

	"pecheny.me/dopecore/adminusers"
	kitstrings "pecheny.me/dopeuikit/i18nstrings"
)

// SortHeader is a sortable column heading of the /admin/users table: a small
// ghost button carrying the direction this column would sort in next, and an
// arrow when it is the active one.
func SortHeader(key, label string, s adminusers.Sort) *Element {
	dir, arrow := s.Header(key)
	return Hcell(Button(Ghost, Small(),
		Href("/admin/users?sort="+key+"&dir="+dir), Text(label+arrow),
	))
}

// AdminTime renders a stored RFC3339 timestamp for the admin tables; a missing
// or unparsable value becomes a dash.
func AdminTime(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "—"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// AdminCreateUsers is the body of the /admin/create_users page both apps serve
// over dopecore/adminusers: the bulk-create form, plus (after a submit) the
// outcome — created credentials shown once, skipped usernames, validation
// errors. Each app wraps it in its own page chrome.
func AdminCreateUsers(data adminusers.CreateUsersData) []Item {
	s := kitstrings.Default
	var main []Item
	if data.Submitted {
		if len(data.Created) > 0 {
			main = append(main, createdSection(data))
		}
		if len(data.Skipped) > 0 {
			main = append(main, Section(Empty(Text(s.Admin.Create.SkippedLead()+strings.Join(data.Skipped, ", ")))))
		}
		if len(data.Errors) > 0 {
			main = append(main, errorsSection(data.Errors))
		}
		if len(data.Created) == 0 && len(data.Skipped) == 0 && len(data.Errors) == 0 {
			main = append(main, Empty(Text(s.Admin.Create.Empty())))
		}
	}
	return append(main, Section(
		Form(DirCol, SpaceMD, Method("post"), Action("/admin/create_users"), Autocomplete("off"),
			Field(Label(s.Admin.Create.UsernamesLabel()),
				Editor(Name("usernames"), Rows("10"), Placeholder("ivanov\npetrova\nsidorov"), Required()),
			),
			Row(Button(Submit(), Text(s.Admin.Create.Submit()))),
		),
	))
}

// createdSection is the one-time credentials table plus a copy-paste textarea
// (data-select-all: dope's pageforms.js selects it on click; inert elsewhere).
func createdSection(data adminusers.CreateUsersData) *Element {
	s := kitstrings.Default
	rows := []Item{Trow(Hcell(Text(s.Admin.Created.Username())), Hcell(Text(s.Admin.Created.Password())))}
	for _, u := range data.Created {
		rows = append(rows, Trow(Cell(Text(u.Username)), Cell(Code(Text(u.Password)))))
	}
	return Section(
		Hint(Text(s.Admin.Created.Hint())),
		Table(rows...),
		Field(Label(s.Admin.Created.CopyLabel()),
			Editor(Rows(strconv.Itoa(len(data.Created))), Readonly(), Data("select-all", ""), Text(data.Copyable())),
		),
	)
}

func errorsSection(errs []adminusers.RowError) *Element {
	rows := make([]Item, len(errs))
	for i, e := range errs {
		rows[i] = Listrow(Listtitle(Text(e.Username)), Muted(Text(e.Reason)))
	}
	return Section(Empty(Text(kitstrings.Default.Admin.Errors.Title())), List(rows...))
}
