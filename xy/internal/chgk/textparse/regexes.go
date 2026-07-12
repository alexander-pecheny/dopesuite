package textparse

import "regexp"

// The field-marker regexes are chgksuite's resources/regexes_ru.json, transcribed.
// Two systematic changes are needed for RE2:
//   - Python's \s is Unicode-aware and matches NBSP, which docx text is full of;
//     Go's is ASCII-only, so every \s becomes the ws class below.
//   - Python's $ also matches just before a trailing newline. Every pattern that
//     relies on it is only ever applied to a single already-trimmed line, so
//     Go's end-of-text $ is equivalent here.
const ws = `[\s\x{00a0}]`

var (
	reBattle   = regexp.MustCompile(`(?i)^(?:Бой` + ws + `+(?:\d+(?:\.\d+)?|за` + ws + `+третье` + ws + `+место)|(?:Первый|Второй)` + ws + `+разминочный` + ws + `+бой(?:` + ws + `*\(повтор\))?|Перестрелка)` + ws + `*$`)
	reTour     = regexp.MustCompile(`^(#` + ws + `+)?Т[Уу][Рр]` + ws + `?№?` + ws + `?([0-9IVXLCDM]*)([\.:])?$`)
	reTourrev  = regexp.MustCompile(`^([0-9IVXLCDM]+|[Пп][Ее][Рр][Вв][Ыы][Йй]|[Вв][Тт][Оо][Рр][Оо][Йй]|[Тт][Рр][Ее][Тт][Ии][Йй]|[Чч][Ее][Тт][Вв][Ее][Рр][Тт][Ыы][Йй]|[Пп][Яя][Тт][Ыы][Йй]|[Шш][Ее][Сс][Тт][Оо][Йй]|[Сс][Ее][Дд][Ьь][Мм][Оо][Йй]|[Вв][Оо][Сс][Ьь][Мм][Оо][Йй]|[Дд][Ее][Вв][Яя][Тт][Ыы][Йй]|[Дд][Ее][Сс][Яя][Тт][Ыы][Йй])` + ws + `[Тт][Уу][Рр]([\.:])?$`)
	reQuestion = regexp.MustCompile(`^([Нн][Уу][Лл][Ее][Вв][Оо][Йй]|[Рр][Аа][Зз][Мм][Ии][Нн][Оо][Чч][Нн][Ыы][Йй])? ?[Вв][Оо][Пп][Рр][Оо][Сс]` + ws + `?[№N]?(?P<number>[0-9\s\x{00a0}]*)` + ws + `?([\.:]|\n|\r\n|$)`)
	reHandout  = regexp.MustCompile(`^Р[Аа][Зз][Дд][Аа][Тт][Оо][Чч][Нн][Ыы][Йй]` + ws + `+[Мм][Аа][Тт][Ее][Рр][Ии][Аа][Лл][\.:]`)
	reAnswer   = regexp.MustCompile(`О[Тт][Вв][Ее][Тт][Ыы]?` + ws + `?[№N]?([0-9]+)?` + ws + `?[\.:]`)
	reZachet   = regexp.MustCompile(`З[Аа][Чч][ЕеЁё][Тт]` + ws + `?[\.:]`)
	reNezachet = regexp.MustCompile(`Н[Ее][Зз][Аа][Чч][ЕеЁё][Тт]` + ws + `?[\.:]`)
	reComment  = regexp.MustCompile(`К[Оо][Мм][Мм]?([Ее][Нн][Тт]([Аа][Рр][Ии][ИиЙй]|\.)|\.)` + ws + `?[№N]?([0-9]+)?` + ws + `?[\.:]`)
	reAuthor   = regexp.MustCompile(`А[Вв][Тт][Оо][Рр](\(?[Ыы]?\)?|[Кк][АаИи])?` + ws + `?[\.:]`)
	reSource   = regexp.MustCompile(`И[Сс][Тт][Оо][Чч][Нн][Ии][Кк]\(?[Ии]?\)?` + ws + `?[\.:]`)
	reEditor   = regexp.MustCompile(`[Рр][Ее][Дд][Аа][Кк][Тт][Оо][Рр]([Ыы]|[Сс][Кк][Аа][Яя]` + ws + `[Гг][Рр][Уу][Пп][Пп][Аа])?(` + ws + `?[\.:]|` + ws + `[\-–—]+` + ws + `)`)
	reDate     = regexp.MustCompile(`Д[Аа][Тт][Аа]` + ws + `?[\.:]`)
	reDate2    = regexp.MustCompile(`(^|` + ws + `)[Яя][Нн][Вв][Аа][Рр][ЬьЯя]|[Фф][Ее][Вв][Рр][Аа][Лл][ЬьЯя]|[Мм][Аа][Рр][Тт][Аа]?|[Аа][Пп][Рр][Ее][Лл][ЬьЯя]|[Мм][Аа][ЙйЯя]|[Ии][Юю][Нн][ЬьЯя]|[Ии][Юю][Лл][ЬьЯя]|[Аа][Вв][Гг][Уу][Сс][Тт][Аа]?|[Сс][Ее][Нн][Тт][Яя][Бб][Рр][ЬьЯя]|[Оо][Кк][Тт][Яя][Бб][Рр][ЬьЯя]|[Нн][Оо][Яя][Бб][Рр][ЬьЯя]|[Дд][Ее][Кк][Аа][Бб][Рр][ЬьЯя](` + ws + `|$)`)
	reNumber   = regexp.MustCompile(`^[0-9]+[\.\)]` + ws + `*`)

	// The author label anchored whole-line ("Автор:" and nothing else), used to
	// splice the following element into it.
	reAuthorOnly = regexp.MustCompile(`^(?:А[Вв][Тт][Оо][Рр](\(?[Ыы]?\)?|[Кк][АаИи])?` + ws + `?[\.:])$`)
)

// labelled is the set apply_regexes tries, i.e. every regex except the ones
// chgksuite excludes there ("number", "date2", "handout_short", and the si_* keys,
// which belong to the SI parser).
var labelled = []struct {
	name string
	re   *regexp.Regexp
}{
	{"battle", reBattle},
	{"tour", reTour},
	{"tourrev", reTourrev},
	{"question", reQuestion},
	{"handout", reHandout},
	{"answer", reAnswer},
	{"zachet", reZachet},
	{"nezachet", reNezachet},
	{"comment", reComment},
	{"author", reAuthor},
	{"source", reSource},
	{"editor", reEditor},
	{"date", reDate},
}

// byName is the "element[0] in regexes" lookup of the label-stripping pass.
var byName = map[string]*regexp.Regexp{
	"battle": reBattle, "tour": reTour, "tourrev": reTourrev, "question": reQuestion,
	"handout": reHandout, "answer": reAnswer, "zachet": reZachet, "nezachet": reNezachet,
	"comment": reComment, "author": reAuthor, "source": reSource, "editor": reEditor,
	"date": reDate, "date2": reDate2, "number": reNumber,
}
