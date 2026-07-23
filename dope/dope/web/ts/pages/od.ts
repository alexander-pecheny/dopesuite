// The ОД (ЧГК) page bundle: the shell, the init-payload boot, then the page
// module — which imports its own library dependencies (root ADR-0001).
import '../shell/shell';
import '../init.js';
import '../od.js';
