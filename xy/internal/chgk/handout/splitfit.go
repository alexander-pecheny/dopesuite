package handout

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// pdfConf returns a pdfcpu config with relaxed validation (typst PDFs are valid
// but don't always pass pdfcpu's strict checks).
func pdfConf() *model.Configuration {
	c := model.NewDefaultConfiguration()
	c.ValidationMode = model.ValidationRelaxed
	return c
}

// SplitFit is a Go port of chgksuite's `handouts split_fit`: for each handout
// block it finds the largest row count that still fits one page (binary search,
// using typst's own pagination via a page-count query rather than rendering +
// parsing PDFs), renders a fitted per-question PDF, builds an all-questions
// (one-team) PDF, and returns them all as a zip. Per-question + all-q PDFs are
// compressed with pdfcpu. typst measurement replaces chgksuite's pypdf path.
//
// Not yet ported: the image-shrink refinement pass (chgksuite shrinks an image
// handout to pack more rows when blank space remains) — image blocks fit at
// their given/native size. The fitted layout is otherwise identical.
const splitFitMaxRows = 256

// newSFRun sets up a scratch dir with the images written, returning the run and
// a cleanup func.
func newSFRun(images map[string][]byte, a Args, typstPath string) (*sfRun, func(), error) {
	if typstPath == "" {
		typstPath = "typst"
	}
	fonts, err := bundledFontDir()
	if err != nil {
		return nil, nil, err
	}
	dir, err := scratchTemp("xy-splitfit-*")
	if err != nil {
		return nil, nil, err
	}
	for name, data := range images {
		base := filepath.Base(name)
		if base == "" || base == "." || base == ".." || strings.ContainsAny(name, `/\`) {
			continue
		}
		if err := writeScratch(dir, base, data); err != nil {
			os.RemoveAll(dir)
			return nil, nil, err
		}
	}
	return &sfRun{a: a, dir: dir, typst: typstPath, fonts: fonts}, func() { os.RemoveAll(dir) }, nil
}

// FitRows returns the fitted row count per block (in order) — exported for parity
// tests against chgksuite's "final rows=N".
func FitRows(ctx context.Context, hndt string, images map[string][]byte, a Args, typstPath string) ([]int, error) {
	r, cleanup, err := newSFRun(images, a, typstPath)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	var rows []int
	for _, b := range parseSFBlocks(hndt) {
		best, err := r.fitRows(ctx, b)
		if err != nil {
			return nil, err
		}
		rows = append(rows, best)
	}
	return rows, nil
}

func SplitFit(ctx context.Context, hndt string, images map[string][]byte, a Args, typstPath string) ([]byte, error) {
	r, cleanup, err := newSFRun(images, a, typstPath)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	blocks := parseSFBlocks(hndt)
	if len(blocks) == 0 {
		return nil, errors.New("no handout blocks")
	}

	type output struct {
		name string
		pdf  []byte
	}
	// Fit + render each block concurrently (bounded by CPU count), then the
	// all-questions PDF. Outputs kept in block order.
	outputs := make([]output, len(blocks)+1)
	workers := runtime.NumCPU()
	if workers > len(blocks) {
		workers = len(blocks)
	}
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}
	for i, b := range blocks {
		wg.Add(1)
		go func(i int, b sfBlock) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			best, err := r.fitRows(ctx, b)
			if err != nil {
				fail(fmt.Errorf("q%s: %w", b.qnum(), err))
				return
			}
			pdf, err := r.renderPDF(ctx, b.with(map[string]*string{"rows": ptr(strconv.Itoa(best))}))
			if err != nil {
				fail(fmt.Errorf("q%s render: %w", b.qnum(), err))
				return
			}
			outputs[i] = output{fmt.Sprintf("q%s.pdf", b.qnum()), pdf}
		}(i, b)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	// all-questions, one team each
	var allParts []string
	for _, b := range blocks {
		step := b.rowStep()
		allParts = append(allParts, strings.TrimRight(b.with(map[string]*string{"rows": ptr(strconv.Itoa(step))}), "\n"))
	}
	allPDF, err := r.renderPDF(ctx, strings.Join(allParts, "\n---\n")+"\n")
	if err != nil {
		return nil, fmt.Errorf("all-q render: %w", err)
	}
	outputs[len(blocks)] = output{"all_q.pdf", allPDF}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, o := range outputs {
		w, err := zw.Create(o.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(o.pdf); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// sfRun carries the per-invocation render context.
type sfRun struct {
	a     Args
	dir   string
	typst string
	fonts string
	seq   atomic.Int64
}

// ── block model ──

type sfBlock struct {
	raw  string
	meta map[string]string
}

func parseSFBlocks(contents string) []sfBlock {
	var out []sfBlock
	for _, raw := range splitBlocks(contents) {
		b := sfBlock{raw: raw, meta: map[string]string{}}
		for _, line := range strings.Split(raw, "\n") {
			k, v, ok := strings.Cut(line, ":")
			k = strings.TrimSpace(k)
			if ok && reservedWords[k] {
				b.meta[k] = strings.TrimSpace(v)
			}
		}
		out = append(out, b)
	}
	return out
}

func (b sfBlock) qnum() string {
	if v, ok := b.meta["for_question"]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return fmt.Sprintf("%02d", n)
		}
		return v
	}
	return "00"
}

func (b sfBlock) columns() int {
	n, _ := strconv.Atoi(b.meta["columns"])
	if n <= 0 {
		n = 1
	}
	return n
}

func (b sfBlock) handoutsPerTeam() int {
	if v, ok := b.meta["handouts_per_team"]; ok {
		if n, _ := strconv.Atoi(v); n > 0 {
			return n
		}
	}
	return 3
}

func (b sfBlock) maxWidthMultiplier() int {
	mw := 1.0
	if v, ok := b.meta["max_width"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			mw = f
		}
	}
	m := int(1.0/mw + 1e-9)
	if m < 1 {
		m = 1
	}
	return m
}

// sfColumns mirrors split_fit_columns (columns × the max_width multiplier).
func (b sfBlock) sfColumns() int { return b.columns() * b.maxWidthMultiplier() }

// rowStep mirrors valid_row_step: handouts_per_team / gcd(columns, hpt).
func (b sfBlock) rowStep() int {
	c, n := b.sfColumns(), b.handoutsPerTeam()
	return n / gcd(c, n)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}

// with returns the block's .hndt text with metadata updated/removed (a nil value
// removes the key), applying the max_width→columns expansion like write_handout.
func (b sfBlock) with(updates map[string]*string) string {
	if b.maxWidthMultiplier() > 1 {
		updates["columns"] = ptr(strconv.Itoa(b.sfColumns()))
		updates["max_width"] = nil
	}
	return upsertMeta(b.raw, updates)
}

// upsertMeta ports utils/split_fit upsert_metadata: replace/remove the given
// reserved-word metadata lines in place, appending any missing ones after the
// last metadata line.
func upsertMeta(raw string, updates map[string]*string) string {
	var out []string
	updated := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		k, _, ok := strings.Cut(line, ":")
		k = strings.TrimSpace(k)
		if ok && reservedWords[k] {
			if v, isUpdate := updates[k]; isUpdate {
				if !updated[k] {
					if v != nil {
						out = append(out, k+": "+*v)
					}
					updated[k] = true
				}
				continue
			}
		}
		out = append(out, line)
	}
	// stable insertion order for any missing keys
	for _, k := range []string{"rows", "columns", "max_width", "resize_image", "image"} {
		v, isUpdate := updates[k]
		if !isUpdate || updated[k] || v == nil {
			continue
		}
		insertAt := 0
		for i, line := range out {
			kk, _, ok := strings.Cut(line, ":")
			if ok && reservedWords[strings.TrimSpace(kk)] {
				insertAt = i + 1
			}
		}
		out = append(out[:insertAt], append([]string{k + ": " + *v}, out[insertAt:]...)...)
		updated[k] = true
	}
	return strings.TrimRight(strings.Join(out, "\n"), " \n") + "\n"
}

func ptr(s string) *string { return &s }

// ── typst harness ──

// fitRows ports best_rows_for_block: exponential-up then binary search for the
// largest k where k*step rows still fit on one page.
func (r *sfRun) fitRows(ctx context.Context, b sfBlock) (int, error) {
	step := b.rowStep()
	maxK := splitFitMaxRows / step
	if maxK < 1 {
		return 0, fmt.Errorf("no valid row count <= %d", splitFitMaxRows)
	}
	cache := map[int]bool{}
	fits := func(k int) (bool, error) {
		if v, ok := cache[k]; ok {
			return v, nil
		}
		hndt := b.with(map[string]*string{"rows": ptr(strconv.Itoa(k * step))})
		pages, err := r.pageCount(ctx, hndt)
		if err != nil {
			return false, err
		}
		cache[k] = pages <= 1
		return cache[k], nil
	}

	low, high := 0, 1
	firstFailure := 0
	for high <= maxK {
		ok, err := fits(high)
		if err != nil {
			return 0, err
		}
		if ok {
			low = high
			high *= 2
		} else {
			firstFailure = high
			break
		}
	}
	if low == 0 {
		return 0, fmt.Errorf("minimum %d rows do not fit one page", step)
	}
	upper := firstFailure
	if upper == 0 {
		upper = maxK + 1
	}
	for low+1 < upper {
		mid := (low + upper) / 2
		ok, err := fits(mid)
		if err != nil {
			return 0, err
		}
		if ok {
			low = mid
		} else {
			upper = mid
		}
	}
	return low * step, nil
}

// pageCount renders the handout to typst and asks typst itself how many pages it
// paginates to (no PDF produced) — faithful to chgksuite's render+page-count but
// without writing/parsing a PDF.
func (r *sfRun) pageCount(ctx context.Context, hndt string) (int, error) {
	typ := GenerateTyp(hndt, r.a) + "\n#context [#metadata(here().page()) <xypages>]\n"
	name := r.tempName(".typ")
	if err := writeScratch(r.dir, name, []byte(typ)); err != nil {
		return 0, err
	}
	cmd := exec.CommandContext(ctx, r.typst, "query", "--root", "/", "--font-path", r.fonts, "--ignore-system-fonts", name, "<xypages>", "--field", "value", "--one")
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	os.Remove(filepath.Join(r.dir, name))
	if err != nil {
		return 0, fmt.Errorf("typst query: %s", strings.TrimSpace(string(out)))
	}
	// output is the page number, possibly with a trailing deprecation warning line
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if n, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
			return n, nil
		}
	}
	return 0, fmt.Errorf("typst query: unparseable page count %q", string(out))
}

// renderPDF compiles the handout to a PDF and compresses it with pdfcpu.
func (r *sfRun) renderPDF(ctx context.Context, hndt string) ([]byte, error) {
	typName := r.tempName(".typ")
	pdfName := strings.TrimSuffix(typName, ".typ") + ".pdf"
	if err := writeScratch(r.dir, typName, []byte(GenerateTyp(hndt, r.a))); err != nil {
		return nil, err
	}
	defer os.Remove(filepath.Join(r.dir, typName))
	cmd := exec.CommandContext(ctx, r.typst, "compile", "--root", "/", "--font-path", r.fonts, "--ignore-system-fonts", typName, pdfName)
	cmd.Dir = r.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("typst compile: %s", strings.TrimSpace(string(out)))
	}
	pdfPath := filepath.Join(r.dir, pdfName)
	defer os.Remove(pdfPath)
	raw, err := os.ReadFile(pdfPath)
	if err != nil {
		return nil, err
	}
	return compressPDF(raw), nil
}

// compressPDF optimizes a PDF in memory (pdfcpu), returning the original bytes if
// optimization fails (it's best-effort, like chgksuite's compress_pdf).
func compressPDF(raw []byte) []byte {
	var out bytes.Buffer
	if err := api.Optimize(bytes.NewReader(raw), &out, pdfConf()); err != nil || out.Len() == 0 {
		return raw
	}
	return out.Bytes()
}

func (r *sfRun) tempName(ext string) string {
	return fmt.Sprintf("sf_%d%s", r.seq.Add(1), ext)
}
