#set page(
  width: <PAPERWIDTH>mm,
  height: <PAPERHEIGHT>mm,
  margin: (
    top: <MARGIN_TOP>mm,
    bottom: <MARGIN_BOTTOM>mm,
    left: <MARGIN_LEFT>mm,
    right: <MARGIN_RIGHT>mm,
  ),
)
#set text(font: "<FONT>", size: <FONTSIZE>pt)
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

// A question block: ncols x nrows cells grouped into (tcols x trows) teams,
// tiled with gaps. Every cell holds the same `cellbody`; a single measurement
// fixes the shared row height to max(content height, strut) + padding. `pad`
// (cell inset) and `strut` (single-line floor) scale per block. `teamed` is
// false when the sheet can't be cut into teams, giving an all-dashed block.
#let handout(ncols, nrows, tcols, trows, gap, cellw, pad, strut, teamed, centered, cellbody) = context {
  let ntc = int(ncols / tcols)
  let ntr = int(nrows / trows)
  let border = if teamed { _solid } else { _dashed }
  let rowh = calc.max(measure(box(width: cellw - 2 * pad, cellbody)).height, strut) + 2 * pad
  let one = _team(border, tcols, trows, cellw, rowh, pad, centered, (cellbody,) * (tcols * trows))
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
#let qlabel(body) = block(above: <LABEL_ABOVE>mm, below: <LABEL_BELOW>mm,
  sticky: true, text(fill: gray, size: 9pt, body))