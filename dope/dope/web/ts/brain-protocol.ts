// brain-protocol.ts — the brain Protocol's match document as the page reads it:
// two sides of question rows (who buzzed, right or wrong) plus how many
// tiebreak questions were appended, and the adapter from the server's JSON to
// that shape. Pure: parseState takes the row count the scheme gives the match.
export interface BrainRow {
  player: string;
  mark: string; // "right" | "wrong" | ""
}

export interface BrainMatchState {
  tiebreaks?: number;
  teams?: Array<{rows?: Array<BrainRow | null> | null} | null> | null;
}

// parseState is the adapter: exactly two sides, each with questions +
// tiebreaks rows, every row a {player, mark} with a mark the page knows.
export function parseState(raw: unknown, questions: number): BrainMatchState {
  const state = (raw && typeof raw === "object" ? raw : {}) as BrainMatchState;
  if (!Number.isInteger(state.tiebreaks) || (state.tiebreaks as number) < 0) state.tiebreaks = 0;
  if (!Array.isArray(state.teams)) state.teams = [];
  while (state.teams.length < 2) state.teams.push({rows: []});
  const rowCount = questions + (state.tiebreaks as number);
  state.teams = state.teams.map((side) => {
    const rows = Array.isArray(side?.rows) ? side!.rows! : [];
    while (rows.length < rowCount) rows.push({player: "", mark: ""});
    return {
      rows: rows.map((row) => ({
        player: typeof row?.player === "string" ? row.player : "",
        mark: row?.mark === "right" || row?.mark === "wrong" ? row.mark : "",
      })),
    };
  });
  return state;
}
