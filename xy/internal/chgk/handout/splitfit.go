package handout

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"sync"

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
// The image-shrink refinement pass (fitBlock below) needs a Typesetter that can
// also measure; with one that cannot, image blocks simply keep their given size.
const splitFitMaxRows = 256

// newSFRun loads the run's images into the typesetter.
func newSFRun(ctx context.Context, images map[string][]byte, a Args, ts Typesetter) (*sfRun, error) {
	if err := ts.SetImages(ctx, images); err != nil {
		return nil, err
	}
	return &sfRun{a: a, ts: ts}, nil
}

// FitRows returns the fitted row count per block (in order) — exported for parity
// tests against chgksuite's "final rows=N".
func FitRows(ctx context.Context, hndt string, images map[string][]byte, a Args, ts Typesetter) ([]int, error) {
	hndt, images, err := ApplyRotation(hndt, images)
	if err != nil {
		return nil, err
	}
	hndt, images = flattenImagePaths(hndt, images)
	r, err := newSFRun(ctx, images, a, ts)
	if err != nil {
		return nil, err
	}
	var rows []int
	for _, b := range parseSFBlocks(hndt) {
		best, _, err := r.fitBlock(ctx, b)
		if err != nil {
			return nil, err
		}
		rows = append(rows, best)
	}
	return rows, nil
}

func SplitFit(ctx context.Context, hndt string, images map[string][]byte, a Args, ts Typesetter) ([]byte, error) {
	hndt, images, err := ApplyRotation(hndt, images)
	if err != nil {
		return nil, err
	}
	hndt, images = flattenImagePaths(hndt, images)
	r, err := newSFRun(ctx, images, a, ts)
	if err != nil {
		return nil, err
	}

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
			best, resize, err := r.fitBlock(ctx, b)
			if err != nil {
				fail(fmt.Errorf("q%s: %w", b.qnum(), err))
				return
			}
			pdf, err := r.renderPDF(ctx, b.with(withRows(resize, best)))
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
	a  Args
	ts Typesetter
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
// largest k where k*step rows still fit on one page. extra is the metadata the
// probe carries besides the row count — the image's resize, during the shrink
// pass.
func (r *sfRun) fitRows(ctx context.Context, b sfBlock, extra map[string]*string) (int, error) {
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
		pages, err := r.pageCount(ctx, b.with(withRows(extra, k*step)))
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

// pageCount asks typst itself how many pages the handout paginates to (no PDF
// produced) — faithful to chgksuite's render+page-count, but without writing or
// parsing a PDF. The trailing metadata makes the page number queryable; the wasm
// typesetter reads it straight off the laid-out document instead.
func (r *sfRun) pageCount(ctx context.Context, hndt string) (int, error) {
	typ := GenerateTyp(hndt, r.a) + "\n#context [#metadata(here().page()) <xypages>]\n"
	_, pages, err := r.ts.Compile(ctx, typ, false)
	return pages, err
}

// renderPDF compiles the handout to a PDF and compresses it with pdfcpu.
func (r *sfRun) renderPDF(ctx context.Context, hndt string) ([]byte, error) {
	pdf, _, err := r.ts.Compile(ctx, GenerateTyp(hndt, r.a), true)
	if err != nil {
		return nil, err
	}
	return compressPDF(pdf), nil
}

func compressPDF(raw []byte) []byte {
	var out bytes.Buffer
	if err := api.Optimize(bytes.NewReader(raw), &out, pdfConf()); err != nil || out.Len() == 0 {
		return raw
	}
	return out.Bytes()
}

// ── the image-shrink refinement (fit_rows_and_resize) ──

// withRows is the metadata a probe carries: the row count, plus whatever else
// (the image's resize) the caller wants set.
func withRows(extra map[string]*string, rows int) map[string]*string {
	m := map[string]*string{"rows": ptr(strconv.Itoa(rows))}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// fitBlock fits one block, returning its row count and the metadata its render
// should carry. Without an image, or a typesetter that can measure, it is just
// the row search; with both, it shrinks the image until another row fits and
// then grows it back as far as that row count allows — chgksuite's
// fit_rows_and_resize.
func (r *sfRun) fitBlock(ctx context.Context, b sfBlock) (int, map[string]*string, error) {
	resize := b.resizeImage()
	rows, err := r.fitRows(ctx, b, b.resizeUpdate(resize))
	if err != nil {
		return 0, nil, err
	}
	cfg := r.a.Resize
	ms, canMeasure := r.ts.(Measurer)
	if !canMeasure || b.meta["image"] == "" {
		return rows, b.resizeUpdate(resize), nil
	}

	shrink := 1 - cfg.ShrinkPercent/100
	for {
		bottom, onePage, err := r.bottomSpace(ctx, ms, b, rows, resize)
		if err != nil {
			return 0, nil, err
		}
		if !onePage || resize <= cfg.MinResizeImage {
			break
		}
		if bottom <= r.bottomSpaceThreshold(bottom, rows, cfg.BottomSpaceRowRatio) {
			break
		}

		// Shrink in steps until one more row fits (or the floor is reached).
		trial, improvedRows := resize, 0
		for trial > cfg.MinResizeImage {
			trial = math.Max(cfg.MinResizeImage, trial*shrink)
			n, err := r.fitRows(ctx, b, b.resizeUpdate(trial))
			if err != nil {
				return 0, nil, err
			}
			if n > rows {
				improvedRows = n
				break
			}
			if math.Abs(trial-cfg.MinResizeImage) < 1e-4 {
				break
			}
		}
		if improvedRows == 0 {
			break
		}
		// Then give the image back as much size as that row count still allows.
		grown, err := r.maxResizeForRows(ctx, b, improvedRows, trial, resize)
		if err != nil {
			return 0, nil, err
		}
		resize, rows = grown, improvedRows
	}
	return rows, b.resizeUpdate(resize), nil
}

// bottomSpace measures the blank space (mm) below the content of a one-page
// handout; onePage is false when it doesn't fit on one page at all.
func (r *sfRun) bottomSpace(ctx context.Context, ms Measurer, b sfBlock, rows int, resize float64) (float64, bool, error) {
	typ := GenerateTyp(b.with(withRows(b.resizeUpdate(resize), rows)), r.a) + MeasureSnippet
	pages, y, err := ms.Measure(ctx, typ)
	if err != nil {
		return 0, false, err
	}
	if pages > 1 {
		return 0, false, nil
	}
	return math.Max(0, float64(r.a.PaperHeight)-y), true, nil
}

// bottomSpaceThreshold is the blank space worth shrinking for: a fraction of one
// row's height, itself derived from the space the rows do occupy.
func (r *sfRun) bottomSpaceThreshold(bottom float64, rows int, ratio float64) float64 {
	available := float64(r.a.PaperHeight - r.a.MarginTop - r.a.MarginBottom)
	return ratio * math.Max(0, available-bottom) / float64(rows)
}

// maxResizeForRows binary-searches the largest resize that still fits `rows`.
func (r *sfRun) maxResizeForRows(ctx context.Context, b sfBlock, rows int, low, high float64) (float64, error) {
	best := low
	for i := 0; i < r.a.Resize.RefineIterations; i++ {
		mid := (low + high) / 2
		fitted, err := r.fitRows(ctx, b, b.resizeUpdate(mid))
		if err != nil {
			return 0, err
		}
		if fitted >= rows {
			best, low = mid, mid
		} else {
			high = mid
		}
	}
	return best, nil
}

// resizeImage is the block's own resize_image, 1.0 by default.
func (b sfBlock) resizeImage() float64 {
	if f, err := strconv.ParseFloat(b.meta["resize_image"], 64); err == nil && f > 0 {
		return f
	}
	return 1
}

// resizeUpdate is resize_update_for_block: the resize_image line a probe or a
// render should carry — none for a block without an image, and none for an
// untouched 1.0 the source never spelled out.
func (b sfBlock) resizeUpdate(resize float64) map[string]*string {
	if b.meta["image"] == "" {
		return nil
	}
	_, explicit := b.meta["resize_image"]
	if !explicit && math.Abs(resize-1) < 1e-4 {
		return nil
	}
	return map[string]*string{"resize_image": ptr(formatFloat(resize))}
}

// formatFloat ports format_float: two decimals, floored, trailing zeros dropped.
func formatFloat(v float64) string {
	s := strconv.FormatFloat(math.Floor(v*100+1e-9)/100, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}
