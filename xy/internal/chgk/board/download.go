package board

import (
	"fmt"
	"regexp"
	"strings"

	"xy/internal/chgk/docx"
)

// gui_board_download's half that has no network in it: a fetched board into the
// files it becomes.

// DownloadOptions are the switches `board download` takes.
type DownloadOptions struct {
	// Lists narrows the download to these list names.
	Lists []string
	// SI writes a .docx per list with the card captions as headings, which is
	// how a Своя игра package is edited.
	SI bool
	// QB pairs two lists into a quizbowl .docx: --qb takes the two list names.
	QB []string
	// OnlyAnswers / NoAnswers filter a card's lines, for the СИ .docx.
	OnlyAnswers, NoAnswers bool
	// SingleFile writes one .4s for the whole board instead of one per list.
	SingleFile bool
	// Labels also writes a file per label.
	Labels bool
	// ReplaceDoubleLineBreaks and FixTrelloNewEditor undo what Trello's editor
	// did to a description on the way in.
	ReplaceDoubleLineBreaks bool
	FixTrelloNewEditor      bool
	// Docx are the switches the two .docx outputs pass on (--font, template).
	Docx docx.Options
}

// File is one file the download wants written, named relative to the folder.
type File struct {
	Name string
	Data []byte
}

// Download turns a fetched board into its files.
func Download(j *JSON, o DownloadOptions) ([]File, error) {
	names := map[string]string{}
	var openLists []List
	for _, l := range j.Lists {
		if l.Closed {
			continue
		}
		openLists = append(openLists, l)
		names[l.ID] = strings.ReplaceAll(l.Name, "/", "_")
	}
	wanted := map[string]bool{}
	for _, name := range o.Lists {
		wanted[strings.TrimSpace(name)] = true
	}

	lists := map[string][]string{}
	var listOrder []string
	counters := map[string]int{}
	// The СИ .docx is built per list, in the order the lists' cards arrive.
	siDocs := map[string][]docx.Block{}
	siThemes := map[string][]string{}
	var siOrder []string

	for _, card := range j.Cards {
		desc := card.Desc
		if o.ReplaceDoubleLineBreaks || o.FixTrelloNewEditor {
			desc = undoTrelloEditor(desc)
		}
		if o.FixTrelloNewEditor {
			desc = fixTrelloLinks(desc)
		}
		listName, known := names[card.IDList]
		if card.Closed || !known || (len(wanted) > 0 && !wanted[listName]) {
			continue
		}
		counters[card.IDList]++

		cardTitle, clearTitle := "", ""
		switch {
		case !o.SI:
		case strings.HasPrefix(card.Name, "#"):
			cardTitle = card.Name
			counters[card.IDList] = 0
		default:
			cardTitle = fmt.Sprintf("Тема %d. %s", counters[card.IDList], card.Name)
			clearTitle = card.Name
		}

		if o.SI {
			if _, seen := siDocs[listName]; !seen {
				siOrder = append(siOrder, listName)
				siDocs[listName] = []docx.Block{docx.Heading(1, listName)}
			}
			blocks, themes := siDocs[listName], siThemes[listName]
			if cardTitle != "" {
				if m := reHeadingCard.FindStringSubmatch(cardTitle); m != nil {
					blocks = append(blocks, docx.Heading(len(m[1]), m[2]))
					blocks = append(blocks, themesList(themes)...)
					themes = nil
					blocks = append(blocks, docx.Paragraph(""))
				} else {
					themes = append(themes, clearTitle)
					blocks = append(blocks, docx.Heading(2, cardTitle), docx.Paragraph(""), docx.Paragraph(""))
				}
			}
			if desc != "" {
				blocks = append(blocks, docx.Paragraph(processDesc(desc, o.OnlyAnswers, o.NoAnswers)))
			}
			siDocs[listName], siThemes[listName] = blocks, themes
		}

		if _, seen := lists[listName]; !seen {
			listOrder = append(listOrder, listName)
		}
		sep := "\n\n"
		if strings.HasPrefix(cardTitle, "#") {
			sep = ""
		}
		lists[listName] = append(lists[listName], cardTitle+sep+processDesc(desc, false, false))

		if o.Labels {
			for _, label := range labelSet(card) {
				if _, seen := lists[label]; !seen {
					listOrder = append(listOrder, label)
				}
				head := ""
				if o.SI {
					head = card.Name
				}
				lists[label] = append(lists[label], head+processDesc(desc, false, false))
			}
		}
	}

	var out []File
	if o.SI {
		for _, name := range siOrder {
			blocks := append(siDocs[name], themesList(siThemes[name])...)
			data, err := docx.Simple(blocks, o.Docx)
			if err != nil {
				return nil, err
			}
			out = append(out, File{name + ".docx", data})
		}
	}
	if len(o.QB) == 2 {
		data, err := quizbowl(lists[o.QB[0]], lists[o.QB[1]], o.Docx)
		if err != nil {
			return nil, err
		}
		out = append(out, File{"quizbowl.docx", data})
	}

	if o.SingleFile {
		var body strings.Builder
		for _, l := range openLists {
			for _, item := range lists[strings.ReplaceAll(l.Name, "/", "_")] {
				body.WriteString("\n" + item + "\n")
			}
		}
		return append(out, File{"singlefile.4s", []byte(body.String())}), nil
	}
	for _, name := range listOrder {
		var body strings.Builder
		for _, item := range lists[name] {
			body.WriteString("\n" + item + "\n")
		}
		out = append(out, File{name + ".4s", []byte(body.String())})
	}
	return out, nil
}

var reHeadingCard = regexp.MustCompile(`(#+)\s*(.*)`)

// themesList is add_themes_list: the «Темы:» paragraph a СИ document closes a
// section with, or nothing when the section had none.
func themesList(themes []string) []docx.Block {
	if len(themes) == 0 {
		return nil
	}
	lines := make([]string, len(themes))
	for i, t := range themes {
		lines[i] = fmt.Sprintf("%d. %s", i+1, t)
	}
	return []docx.Block{docx.Paragraph("Темы:\n" + strings.Join(lines, "\n"))}
}

// quizbowl pairs two lists into the tossup/bonus document.
func quizbowl(first, second []string, opts docx.Options) ([]byte, error) {
	var blocks []docx.Block
	for i := 0; i < len(first) && i < len(second); i++ {
		blocks = append(blocks,
			docx.Block{Text: fmt.Sprintf("Тоссап %d.", i+1), Bold: true},
			docx.Paragraph(""), docx.Paragraph(first[i]), docx.Paragraph(""),
			docx.Block{Text: fmt.Sprintf("Бонус %d.", i+1), Bold: true},
			docx.Paragraph(""), docx.Paragraph(second[i]), docx.Paragraph(""),
		)
	}
	return docx.Simple(blocks, opts)
}

func labelSet(card Card) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range card.Labels {
		if l.Name == "" || seen[l.Name] {
			continue
		}
		seen[l.Name] = true
		out = append(out, l.Name)
	}
	return out
}

// processDesc is process_desc: the escapes Trello's editor added, and the line
// filters the two answer switches ask for.
func processDesc(s string, onlyAnswers, noAnswers bool) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\\`", "`")
	s = strings.ReplaceAll(s, `\*`, "*")
	switch {
	case onlyAnswers:
		s = filterLines(s, isAnswerLine)
	case noAnswers:
		s = filterLines(s, func(line string) bool { return !isFieldLine(line) })
	}
	return s
}

func filterLines(s string, keep func(string) bool) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if keep(line) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func isAnswerLine(line string) bool {
	return hasAnyPrefix(line, "Ответ", "Зачёт", "Зачет", "1", "2", "3", "4", "5", "6", "8")
}

func isFieldLine(line string) bool {
	return hasAnyPrefix(line, "Ответ", "Коммента", "Источник", "Автор",
		"Зачёт", "Зачет", "Незачёт", "Незачет")
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

var reIndent = regexp.MustCompile(`\n +`)

// undoTrelloEditor is the block of replacements gui_board_download runs when
// either --replace_double_line_breaks or --fix_trello_new_editor is on.
func undoTrelloEditor(desc string) string {
	desc = strings.ReplaceAll(desc, "\n\n", "\n")
	desc = strings.ReplaceAll(desc, `\@`, "@")
	desc = reIndent.ReplaceAllString(desc, "\n")
	desc = strings.ReplaceAll(desc, "\n\\-", "\n-")
	desc = strings.ReplaceAll(desc, `\#`, "#")
	return strings.ReplaceAll(desc, "```", "")
}

// fixTrelloLinks is fix_trello_new_editor_links: the editor turned a bare URL
// into "[url](url)", and this puts it back.
func fixTrelloLinks(desc string) string {
	var result []string
	for {
		i := strings.Index(desc, "](")
		if i < 0 {
			break
		}
		first, second, link, ok := parseLink(desc, i)
		if ok && link != "" {
			together := first + second
			end := strings.Index(desc, together) + len(together) + 1
			if end > len(desc) {
				end = len(desc)
			}
			result = append(result, strings.Replace(desc[:end], together, link, 1))
			desc = desc[end:]
			continue
		}
		result = append(result, desc[:i+2])
		desc = desc[i+2:]
	}
	if len(result) == 0 {
		return desc
	}
	return strings.Join(result, "") + desc
}

// parseLink is find_and_parse_link: from the "](" at index, the bracketed text
// before it and the parenthesised target after, and the URL when both are one.
func parseLink(s string, index int) (first, second, link string, ok bool) {
	if index >= len(s) || s[index] != ']' || index+1 >= len(s) || s[index+1] != '(' {
		return "", "", "", false
	}
	mvr, level := index, 0
	for mvr > 0 {
		mvr--
		if s[mvr] == ']' {
			level++
		} else if s[mvr] == '[' {
			if level > 0 {
				level--
			} else {
				break
			}
		}
	}
	if mvr < 0 || s[mvr] != '[' {
		return "", "", "", false
	}
	first = s[mvr : index+1]
	mvr, level = index+1, 0
	for mvr < len(s)-1 {
		mvr++
		if s[mvr] == '(' {
			level++
		} else if s[mvr] == ')' {
			if level > 0 {
				level--
			} else {
				break
			}
		}
	}
	if mvr >= len(s) || s[mvr] != ')' {
		return "", "", "", false
	}
	second = s[index+1 : mvr+1]
	if strings.HasPrefix(first[1:], "http") && strings.HasPrefix(second[1:], "http") {
		link = first[1 : len(first)-1]
	}
	return first, second, link, true
}
