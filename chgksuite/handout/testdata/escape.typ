#set page(
  width: 210mm,
  height: 297mm,
  margin: (
    top: 5mm,
    bottom: 5mm,
    left: 5mm,
    right: 5mm,
  ),
)
#set text(font: "Noto Sans", size: 14pt)
#set par(justify: false, leading: 0.55em)
#set block(spacing: 0pt)

#let _solid = 0.8pt + black
#let _dashed = (paint: black, thickness: 0.5pt, dash: "dashed")

// One team: a box of tcols x trows identical cells separated inside by dashed
// lines, each cell's content aligned and vertically centred. Cells are flush so
// each dash sits on a shared edge, centred between the two cells' content (the
// cell inset `pad` is the only gap); this keeps the text optically centred.
// `border` is the outer stroke (solid for a real team, dashed when uncut).
#let _team(border, tcols, trows, cellw, rowh, pad, centered, cells) = box(
  stroke: border,
  inset: 0pt,
  table(
    columns: (cellw,) * tcols,
    rows: (rowh,) * trows,
    inset: pad,
    align: (if centered { center } else { left }) + horizon,
    stroke: (x, y) => (
      left: if x > 0 { _dashed },
      top: if y > 0 { _dashed },
    ),
    ..cells,
  ),
)

// Text is laid out between the font's ascender and descender lines, but its ink
// is not: a capital falls short of the ascender while a descender nearly reaches
// the descender line, so a metrically centred cell reads bottom-heavy. Measuring
// the body again with ink-tight edges gives the slack on each side (summed over
// its lines); half their difference is what centres the ink the eye sees.
#let _ink_shift(body, w) = {
  let h(top, bottom, lead) = measure(box(width: w, {
    set text(top-edge: top, bottom-edge: bottom)
    set par(leading: lead)
    body
  })).height
  let lead = par.leading.to-absolute()
  let lines = if lead == 0pt { 1 } else {
    calc.round((h("ascender", "descender", lead) - h("ascender", "descender", 0pt)) / lead) + 1
  }
  (h("ascender", "bounds", lead) - h("bounds", "descender", lead)) / (2 * lines)
}

// A question block: ncols x nrows cells grouped into (tcols x trows) teams,
// tiled with gaps. Every cell holds the same `cellbody`; a single measurement
// fixes the shared row height to max(content height, strut) + padding. `pad`
// (cell inset) and `strut` (single-line floor) scale per block. `teamed` is
// false when the sheet can't be cut into teams, giving an all-dashed block.
//
// `label` is the grey «Вопрос N» of `question_label: inside`, so a handout
// carries its question number after it is cut out. It is small and pinned into
// the cell's top-left corner, over the cell's own padding, whatever the cell
// does with its content, so it sits at the same place in every block and costs
// little room. A centred body that leaves that corner free shares the top line
// with it; any other body starts below it.
#let handout(ncols, nrows, tcols, trows, gap, cellw, pad, strut, teamed, centered, cellbody, label: none) = context {
  let ntc = int(ncols / tcols)
  let ntr = int(nrows / trows)
  let border = if teamed { _solid } else { _dashed }
  let w = cellw - 2 * pad
  let lbl = if label != none { text(fill: gray, size: 7pt, label) }
  let body = if lbl == none { cellbody } else {
    let b = measure(cellbody, width: w)
    let l = measure(lbl)
    let beside = centered and b.width + 2 * l.width + 2mm <= w
    if beside { cellbody } else {
      block(width: 100%, inset: (top: calc.max(0pt, l.height + 1.2mm - pad)), cellbody)
    }
  }
  let rowh = calc.max(measure(box(width: w, body)).height, strut) + 2 * pad
  let cell = {
    if lbl != none { place(top + left, dx: 0.5mm - pad, dy: 0.3mm - pad, lbl) }
    move(dy: -_ink_shift(body, w), body)
  }
  let one = _team(border, tcols, trows, cellw, rowh, pad, centered, (cell,) * (tcols * trows))
  // Left-aligned so the block's left edge lines up with the grey label above it;
  // gaps separate the teams (cells within a team stay flush).
  align(left, grid(
    columns: ntc,
    column-gutter: gap,
    row-gutter: gap,
    ..(one,) * (ntc * ntr),
  ))
}

// Small grey caption sitting just above (and left-aligned with) its block; it is
// sticky so a page break never orphans it from the handout beneath it.
#let qlabel(body) = block(above: 2.0mm, below: 0.9mm,
  sticky: true, text(fill: gray, size: 7pt, body))

// A block with its number inside prints no caption above it, so nothing would
// keep it off the block before it; this leaves the caption's air instead, and
// drops away at the top of a page like the caption's own spacing.
#let qgap() = v(2.0mm, weak: true)

#qlabel[Раздаточный материал к вопросу 5]

#handout(3, 1, 3, 1, 1.5mm, 66.0mm, 2mm, 5.927mm, true, true, text(size: 14pt)[Текст со \#спец \[символами\] \*звёздочками\* и \_подчёркиванием\_\
вторая строка])