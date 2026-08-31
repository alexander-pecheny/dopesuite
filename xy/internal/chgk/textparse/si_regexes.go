package textparse

import "regexp"

// The SI and troika markers: regexes_ru.json's si_* keys, plus the ones
// parser.py keeps to itself because they are the same in every language (a
// question's number in СИ is its point value, and a троика's is 1, 2 or 3).

var (
	reSiTheme          = regexp.MustCompile(`(?i)^Тема` + ws + `+(\d+)\.` + ws + `*(.*)`)
	reSiThemeComment   = regexp.MustCompile(`(?i)^Комментарий к теме[\.:]`)
	reSiBattle         = regexp.MustCompile(`(?i)^(?:БОЙ|Бой)` + ws + `+([IVXLCDM\d]+)`)
	reSiBattleNumbered = regexp.MustCompile(`^(?:\d+\.` + ws + `+[А-ЯЁA-Z` + ws + `\-–—]+|(?i:\d+/\d+` + ws + `+финала(?:\.` + ws + `+\d+` + ws + `+бой)?))$`)
	reSiYourThemes     = regexp.MustCompile(`(?i)^Ваши` + ws + `+темы` + ws + `*:`)
	reSiRoundName      = regexp.MustCompile(`(?i)(открытый|полуоткрытый|закрытый)` + ws + `+раунд`)

	// reStyleHeading is the marker docxread writes for a heading paragraph.
	reStyleHeading = regexp.MustCompile(`^\$\$H(\d)\$\$` + ws + `*(.*)`)

	reSiQuestionNum     = regexp.MustCompile(`^(\d+)\.` + ws + `+`)
	reSiQuestionNumOnly = regexp.MustCompile(`^(\d+)\.?$`)
	// reSiThemeAuthored is an inline theme header, "2. Ноль (Давид Эджибия)":
	// a small index and an author in brackets at the end, for a document whose
	// styling did not mark the header as a heading.
	reSiThemeAuthored = regexp.MustCompile(`^(\d+)\.` + ws + `+(.+\([^)]+\))` + ws + `*$`)

	reLeadingNum          = regexp.MustCompile(`^\d+[\.\)]` + ws + `*`)
	reAuthorGratitudeMeta = regexp.MustCompile(`(?i)^Автор(?:ы|ка)?` + ws + `+благодар`)
	reThemesHeader        = regexp.MustCompile(`(?i)^.*тем[ыа]` + ws + `*:?$`)
	// RE2's \b is ASCII-only, and these words end in Cyrillic or are followed
	// by it, so the boundaries are spelled out as "not a letter or digit".
	reURLLike = regexp.MustCompile(`https?://|www\.|/|\.(?:ru|com|net|org|io|info|edu|su|by|ua|kz)(?:[^\p{L}\p{N}_]|$)`)
)

// siQuestionNumbers are the point values a СИ question can be worth; a line
// numbered anything else is not a question.
var siQuestionNumbers = map[int]bool{
	10: true, 20: true, 30: true, 40: true, 50: true,
	60: true, 70: true, 80: true, 90: true, 100: true,
}

var (
	reTroikaRedundantThemePrefix    = regexp.MustCompile(`(?i)^ТЕМА[\.:]` + ws + `*`)
	reTroikaThemeNumBeforeRedundant = regexp.MustCompile(`(?i)^ТЕМА` + ws + `+\d+(?:` + ws + `*\([^)]+\))?[\.:]` + ws + `*(ТЕМА[\.:])`)
	reTroikaTheme                   = regexp.MustCompile(`(?i)^ТЕМА(?:` + ws + `+\d+(?:` + ws + `*\([^)]+\))?)?[\.:]` + ws + `*.+`)
	reTroikaSection                 = regexp.MustCompile(`(?i)^ГРУППОВОЙ` + ws + `+ЭТАП` + ws + `+\d+` + ws + `*$`)
	reTroikaPointsSection           = regexp.MustCompile(`(?i)^ТЕМЫ` + ws + `+ЗА` + ws + `+\d+` + ws + `+балл\S*` + ws + `*$`)
	reTroikaBattle                  = regexp.MustCompile(`(?i)^БОЙ` + ws + `+(?:\d+|[IVXLCDM]+)` + ws + `*$`)
	reTroikaFinal                   = regexp.MustCompile(`(?i)^\d+/\d+` + ws + `+ФИНАЛА\.?` + ws + `*$`)
	reTroikaQuestionNum             = regexp.MustCompile(`^(\d+)\.?` + ws + `+`)
	reTroikaQuestionNumOnly         = regexp.MustCompile(`^(\d+)\.?$`)
	// reTroikaMultiforaQuestion is the «Мультифора» variant's question marker:
	// "N.M." (theme index, question index), sometimes spelled "Вопрос N.M." or,
	// in the reserve pool, "ЗапN.M.". The dot after M is required, so a bare
	// "N. Name" theme header is not read as a question.
	reTroikaMultiforaQuestion = regexp.MustCompile(`(?i)^(?:ВОПРОС` + ws + `+|ЗАП)?\d+` + ws + `*\.` + ws + `*(\d+)` + ws + `*\.` + ws + `*`)
	reTroikaMultiforaDetect   = regexp.MustCompile(`(?im)^` + ws + `*(?:\$\$H\d\$\$` + ws + `*)?(?:ВОПРОС` + ws + `+|ЗАП)?\d+` + ws + `*\.` + ws + `*\d+` + ws + `*\.`)
	reTroikaNumberedTheme     = regexp.MustCompile(`^\d+\.` + ws + `+\S`)
	reTroikaReserveSection    = regexp.MustCompile(`(?i)^ЗАПАС` + ws + `*$`)
	reTroikaReserveTheme      = regexp.MustCompile(`(?i)^ЗАПАС` + ws + `*-` + ws + `*\d+` + ws + `*\.` + ws + `*.+`)
	reTroikaSourceItem        = regexp.MustCompile(`^(\d+)[\.\)]` + ws + `+`)
	reTroikaHostNote          = regexp.MustCompile(`(?i)^\[?(?:Ведущему|Комментарий` + ws + `+ведущему)(?:[^\p{L}\p{N}_]|$)`)
	reTroikaSourceItemLine    = regexp.MustCompile(`(?m)^` + ws + `*(\d+)[\.\)]` + ws + `+(.+)$`)
)

// troikaQuestionNumbers: a троика theme holds three questions.
var troikaQuestionNumbers = map[int]bool{1: true, 2: true, 3: true}

// structuralTypes are the elements that close whatever question was being read.
var structuralTypes = map[string]bool{
	"battle": true, "round": true, "section": true, "theme": true,
	"heading": true, "editor": true, "date": true, "meta": true,
}
