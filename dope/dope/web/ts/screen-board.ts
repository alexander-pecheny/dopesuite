// The screen (projector board) arithmetic, DOM-free: host settings, the
// city→flag lookup and the column/zoom layout. od.ts measures one probe column
// and applies the plan; everything that can be wrong about the packing lives here.

export interface ScreenSettings {
  bg: string;
  fg: string;
  muted: string;
  fontScale: number;
  columns: number;
  showCity: boolean;
  showCountry: boolean;
}

export const SCREEN_DEFAULTS: ScreenSettings = {
  bg: "#ffffff",
  fg: "#000000",
  muted: "#5f6b7a",
  fontScale: 1, // multiplier on the auto-fit zoom; 1 = fill the screen
  columns: 0, // 0 = auto-pick the column count, >0 = force that many
  showCity: true,
  showCountry: false,
};

export function normalizeScreenSettings(raw: unknown): ScreenSettings {
  const s = {...SCREEN_DEFAULTS};
  if (raw && typeof raw === "object") {
    const r = raw as Partial<Record<keyof ScreenSettings, unknown>>;
    if (typeof r.bg === "string") s.bg = r.bg;
    if (typeof r.fg === "string") s.fg = r.fg;
    if (typeof r.muted === "string") s.muted = r.muted;
    if (typeof r.fontScale === "number" && Number.isFinite(r.fontScale)) s.fontScale = Math.min(Math.max(r.fontScale, 0.4), 2);
    if (typeof r.columns === "number" && Number.isFinite(r.columns)) s.columns = Math.max(0, Math.round(r.columns));
    if (typeof r.showCity === "boolean") s.showCity = r.showCity;
    if (typeof r.showCountry === "boolean") s.showCountry = r.showCountry;
  }
  return s;
}

// City → ISO-3166 alpha-2 lookup for the country-flag option. Russian-domain
// tournaments are mostly RU/CIS with the occasional international team; unknown
// cities simply get no flag. Extend freely as new cities appear.
export const CITY_COUNTRY: Record<string, string> = {
  "москва": "RU", "мск": "RU", "санкт-петербург": "RU", "спб": "RU", "петербург": "RU",
  "питер": "RU", "новосибирск": "RU", "екатеринбург": "RU", "казань": "RU",
  "нижний новгород": "RU", "челябинск": "RU", "самара": "RU", "омск": "RU",
  "ростов-на-дону": "RU", "уфа": "RU", "красноярск": "RU", "пермь": "RU",
  "воронеж": "RU", "волгоград": "RU", "краснодар": "RU", "саратов": "RU",
  "тюмень": "RU", "тольятти": "RU", "ижевск": "RU", "барнаул": "RU",
  "ульяновск": "RU", "иркутск": "RU", "хабаровск": "RU", "ярославль": "RU",
  "владивосток": "RU", "махачкала": "RU", "томск": "RU", "оренбург": "RU",
  "кемерово": "RU", "новокузнецк": "RU", "рязань": "RU", "астрахань": "RU",
  "пенза": "RU", "липецк": "RU", "тула": "RU", "киров": "RU", "чебоксары": "RU",
  "калининград": "RU", "брянск": "RU", "курск": "RU", "иваново": "RU",
  "магнитогорск": "RU", "тверь": "RU", "ставрополь": "RU", "белгород": "RU",
  "сочи": "RU", "сургут": "RU", "владимир": "RU", "чита": "RU", "симферополь": "RU",
  "севастополь": "RU", "калуга": "RU", "смоленск": "RU", "вологда": "RU",
  "мурманск": "RU", "саранск": "RU", "тамбов": "RU", "грозный": "RU",
  "якутск": "RU", "кострома": "RU", "петрозаводск": "RU", "нальчик": "RU",
  "орёл": "RU", "орел": "RU", "новороссийск": "RU", "великий новгород": "RU",
  "псков": "RU", "обнинск": "RU", "дубна": "RU", "зеленоград": "RU",
  "минск": "BY", "гомель": "BY", "могилёв": "BY", "могилев": "BY", "витебск": "BY",
  "гродно": "BY", "брест": "BY", "бобруйск": "BY", "барановичи": "BY",
  "киев": "UA", "харьков": "UA", "одесса": "UA", "днепр": "UA", "днепропетровск": "UA",
  "львов": "UA", "запорожье": "UA", "кривой рог": "UA", "николаев": "UA",
  "винница": "UA", "херсон": "UA", "чернигов": "UA", "полтава": "UA",
  "черкассы": "UA", "житомир": "UA", "сумы": "UA", "хмельницкий": "UA",
  "черновцы": "UA", "ровно": "UA", "ивано-франковск": "UA", "тернополь": "UA",
  "луцк": "UA", "ужгород": "UA",
  "алматы": "KZ", "астана": "KZ", "нур-султан": "KZ", "нурсултан": "KZ",
  "шымкент": "KZ", "караганда": "KZ", "актобе": "KZ", "тараз": "KZ",
  "павлодар": "KZ", "усть-каменогорск": "KZ", "семей": "KZ", "атырау": "KZ",
  "костанай": "KZ", "кызылорда": "KZ", "уральск": "KZ", "петропавловск": "KZ",
  "тель-авив": "IL", "иерусалим": "IL", "хайфа": "IL", "беэр-шева": "IL",
  "нетания": "IL", "ашдод": "IL", "ашкелон": "IL", "ришон-ле-цион": "IL",
  "бат-ям": "IL", "петах-тиква": "IL",
  "ташкент": "UZ", "самарканд": "UZ", "бухара": "UZ",
  "бишкек": "KG", "ош": "KG", "душанбе": "TJ", "ашхабад": "TM",
  "тбилиси": "GE", "батуми": "GE", "ереван": "AM", "баку": "AZ",
  "кишинёв": "MD", "кишинев": "MD",
  "таллин": "EE", "таллинн": "EE", "тарту": "EE", "рига": "LV",
  "вильнюс": "LT", "каунас": "LT", "хельсинки": "FI",
  "берлин": "DE", "мюнхен": "DE", "гамбург": "DE", "франкфурт": "DE",
  "кёльн": "DE", "кельн": "DE", "дюссельдорф": "DE", "штутгарт": "DE",
  "дрезден": "DE", "лейпциг": "DE", "нюрнберг": "DE", "ганновер": "DE",
  "бремен": "DE", "дортмунд": "DE", "эссен": "DE", "бонн": "DE",
  "мёрфельден-вальдорф": "DE", "мёрфельден": "DE",
  "будапешт": "HU", "лондон": "GB", "манчестер": "GB", "прага": "CZ", "брно": "CZ",
  "варшава": "PL", "краков": "PL", "вроцлав": "PL", "гданьск": "PL",
  "париж": "FR", "амстердам": "NL", "мадрид": "ES", "барселона": "ES",
  "рим": "IT", "милан": "IT", "вена": "AT", "цюрих": "CH", "женева": "CH",
  "стокгольм": "SE", "осло": "NO",
  "нью-йорк": "US", "нью йорк": "US", "бостон": "US", "чикаго": "US",
  "сан-франциско": "US", "лос-анджелес": "US", "вашингтон": "US", "сиэтл": "US",
  "торонто": "CA", "монреаль": "CA", "ванкувер": "CA",
  "сидней": "AU", "мельбурн": "AU", "дубай": "AE", "абу-даби": "AE",
};

export function flagEmoji(cc: string): string {
  return cc.toUpperCase().replace(/[A-Z]/g, (c) => String.fromCodePoint(0x1f1e6 + c.charCodeAt(0) - 65));
}

// teamFlag is the leading emoji when "Show country" is on: a globe for a
// national side (rating.chgk gives national/all-star sides that town), otherwise
// the flag of the city's country, or "" when the city is unknown.
export function teamFlag(name: string, city: string): string {
  if (`${name} ${city}`.toLowerCase().includes("сборн")) return "🌍";
  const cc = CITY_COUNTRY[city.trim().toLowerCase()];
  return cc ? flagEmoji(cc) : "";
}

export interface Grouped {
  group: number;
}

// packRows greedily fills columns top-to-bottom (column-major), starting a new
// column once the running body height would exceed maxBodyH. A gap is counted
// before a row whose group differs from the previous row in the same column.
export function packRows<T extends Grouped>(rowItems: T[], rowH: number, gapH: number, maxBodyH: number): T[][] {
  const columns: T[][] = [];
  let current: T[] = [];
  let bodyH = 0;
  for (const item of rowItems) {
    const needGap = current.length > 0 && item.group !== current[current.length - 1].group;
    if (current.length > 0 && bodyH + (needGap ? gapH : 0) + rowH > maxBodyH + 0.5) {
      columns.push(current);
      current = [];
      bodyH = 0;
    }
    bodyH = current.length === 0 ? rowH : bodyH + (needGap ? gapH : 0) + rowH;
    current.push(item);
  }
  if (current.length) columns.push(current);
  return columns;
}

// Metrics come from one probe column rendered at zoom 1, in CSS px.
export interface ScreenMetrics {
  headH: number;
  rowH: number;
  gapH: number;
  colW: number;
  gapPx: number;
  availW: number;
  availH: number;
}

export interface ScreenPlan<T> {
  columns: T[][];
  zoom: number;
  teamCol: number | null; // widened team-name column width, null to keep the base
}

export const SCREEN_BASE_TEAM_COL = 160; // px; matches --screen-team-col default in styles.css
const MAX_ZOOM = 6; // cap so tiny tournaments don't get absurd text
const SAFETY = 0.985; // shrink a touch to absorb sub-pixel rounding

// planScreen packs the rows into columns and picks the zoom that fills the frame.
// Auto mode keeps the column count with the largest zoom (ties → more columns);
// settings.columns forces a count. For a count it binary-searches the smallest
// column body height that still packs into it — a balanced split, hence the
// tallest layout. Leftover width (height-bound layouts) goes into the team
// column; settings.fontScale scales the result.
export function planScreen<T extends Grouped>(rowItems: T[], m: ScreenMetrics, settings: Pick<ScreenSettings, "columns" | "fontScale">): ScreenPlan<T> | null {
  const n = rowItems.length;
  if (!n) return null;
  const groupCount = rowItems[n - 1].group + 1;
  const totalBodyH = n * m.rowH + (groupCount - 1) * m.gapH;
  const columnsNeeded = (maxBodyH: number) => packRows(rowItems, m.rowH, m.gapH, maxBodyH).length;
  const bodyHForColumns = (c: number): number => {
    if (columnsNeeded(m.rowH) <= c) return m.rowH;
    let lo = m.rowH;
    let hi = totalBodyH;
    for (let it = 0; it < 40; it++) {
      const mid = (lo + hi) / 2;
      if (columnsNeeded(mid) <= c) hi = mid;
      else lo = mid;
    }
    return hi;
  };
  const zoomFor = (c: number, bodyH: number) =>
    Math.min(m.availH / (m.headH + bodyH), m.availW / (c * m.colW + (c - 1) * m.gapPx));

  let chosen: {c: number; bodyH: number};
  if (settings.columns > 0) {
    const c = Math.min(Math.max(1, settings.columns), n);
    chosen = {c, bodyH: bodyHForColumns(c)};
  } else {
    let best = {c: 1, bodyH: totalBodyH, zoom: 0};
    for (let c = 1; c <= n; c++) {
      const bodyH = bodyHForColumns(c);
      const zoom = zoomFor(c, bodyH);
      if (zoom >= best.zoom) best = {c, bodyH, zoom};
    }
    chosen = best;
  }

  const {c, bodyH} = chosen;
  const zoom = zoomFor(c, bodyH);
  const totalWidth = c * m.colW + (c - 1) * m.gapPx;
  let teamCol: number | null = null;
  if (totalWidth * zoom > 0 && m.availW / (totalWidth * zoom) > 1.02) {
    const extraPerCol = (m.availW / zoom - totalWidth) / c;
    teamCol = Math.round(Math.min(SCREEN_BASE_TEAM_COL + extraPerCol, SCREEN_BASE_TEAM_COL * 4));
  }
  return {
    columns: packRows(rowItems, m.rowH, m.gapH, bodyH),
    zoom: Math.min(zoom * SAFETY * (settings.fontScale || 1), MAX_ZOOM),
    teamCol,
  };
}
