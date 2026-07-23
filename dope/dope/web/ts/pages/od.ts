// The ОД (ЧГК) page bundle: the shell first, then the legacy page scripts in
// their historical load order — they attach to window and boot themselves.
// Renderer-by-renderer migration replaces these side-effect imports with a
// registered ProtocolRenderer (see shell/contracts.ts).
import '../shell/shell';
import '../../assets/static/init.js';
import '../../assets/static/match-table.js';
import '../../assets/static/entry-model.js';
import '../../assets/static/od.js';
