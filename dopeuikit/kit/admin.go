package kit

import (
	"strconv"
	"strings"
	"time"

	"pecheny.me/dopecore/adminusers"
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
	var main []Item
	if data.Submitted {
		if len(data.Created) > 0 {
			main = append(main, createdSection(data))
		}
		if len(data.Skipped) > 0 {
			main = append(main, Section(Empty(Text("Уже существуют (пропущены): "+strings.Join(data.Skipped, ", ")))))
		}
		if len(data.Errors) > 0 {
			main = append(main, errorsSection(data.Errors))
		}
		if len(data.Created) == 0 && len(data.Skipped) == 0 && len(data.Errors) == 0 {
			main = append(main, Empty(Text("Не указано ни одного логина.")))
		}
	}
	return append(main, Section(
		Form(DirCol, SpaceMD, Method("post"), Action("/admin/create_users"), Autocomplete("off"),
			Field(Label("Логины (по одному в строке)"),
				Editor(Name("usernames"), Rows("10"), Placeholder("ivanov\npetrova\nsidorov"), Required()),
			),
			Row(Button(Submit(), Text("Создать"))),
		),
	))
}

// createdSection is the one-time credentials table plus a copy-paste textarea
// (data-select-all: dope's pageforms.js selects it on click; inert elsewhere).
func createdSection(data adminusers.CreateUsersData) *Element {
	rows := []Item{Trow(Hcell(Text("Логин")), Hcell(Text("Пароль")))}
	for _, u := range data.Created {
		rows = append(rows, Trow(Cell(Text(u.Username)), Cell(Code(Text(u.Password)))))
	}
	return Section(
		Hint(Text("Пароли показаны один раз. Скопируйте и разошлите — пользователи сменят их сами.")),
		Table(rows...),
		Field(Label("Для копирования (логин ⇥ пароль)"),
			Editor(Rows(strconv.Itoa(len(data.Created))), Readonly(), Data("select-all", ""), Text(data.Copyable())),
		),
	)
}

func errorsSection(errs []adminusers.UserError) *Element {
	rows := make([]Item, len(errs))
	for i, e := range errs {
		rows[i] = Listrow(Listtitle(Text(e.Username)), Muted(Text(e.Reason)))
	}
	return Section(Empty(Text("Ошибки:")), List(rows...))
}
