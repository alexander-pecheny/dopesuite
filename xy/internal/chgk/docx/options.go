package docx

// Options are chgksuite's `compose docx` switches. The zero value is its
// defaults: the host's copy of every question, answers shown, no spoilers.
type Options struct {
	Spoilers   Spoilers
	ScreenMode ScreenMode
	// NoAnswers prints questions only — not even spoilered answers.
	NoAnswers bool
	// NoParagraph drops the line break after "Вопрос N.".
	NoParagraph bool
	// OnlyQuestionNumber labels a question "N." instead of "Вопрос N.".
	OnlyQuestionNumber bool
}

// Spoilers says how the answers are hidden from a reader of the printout.
type Spoilers string

const (
	SpoilersOff Spoilers = "off"
	// SpoilersWhiten paints the answer white (the template's "Whitened" style).
	SpoilersWhiten Spoilers = "whiten"
	// SpoilersPagebreak starts the answers on a new page.
	SpoilersPagebreak Spoilers = "pagebreak"
	// SpoilersDots pushes the answer down behind 30 lines of dots.
	SpoilersDots Spoilers = "dots"
)

// ScreenMode says whose copy of a question is printed: the host's (with stress
// accents and bracketed reading notes) or the screen's (without).
type ScreenMode string

const (
	ScreenOff ScreenMode = "off"
	// ScreenReplaceAll prints the screen's copy only.
	ScreenReplaceAll ScreenMode = "replace_all"
	// ScreenAddVersions prints both copies, one after the other.
	ScreenAddVersions ScreenMode = "add_versions"
	// ScreenAddVersionsColumns prints both copies side by side in a table.
	ScreenAddVersionsColumns ScreenMode = "add_versions_columns"
)

// whitenField is chgksuite's WHITEN: which fields a spoiler hides. The answer
// is whitened by its own call site; the handout and the author never are.
var whitenField = map[string]bool{
	"zachet": true, "nezachet": true, "comment": true, "source": true,
}

// textOpts are the per-call switches format_docx_element takes: whether to glue
// non-breaking spaces, whether this field is hidden behind a spoiler, and which
// copy of the text — the host's or the screen's — is being printed.
type textOpts struct {
	nbsp           bool
	whiten         bool
	removeAccents  bool
	removeBrackets bool
}

// screen reports whether this is the screen's copy, which is what a (screen …)
// directive switches on.
func (o textOpts) screen() bool { return o.removeAccents || o.removeBrackets }
