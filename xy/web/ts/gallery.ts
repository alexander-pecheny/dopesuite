// gallery.ts — every primitive a panel is built from, on one page, from
// fixtures: no board, no key, no network. Served at /gallery in dev only.
//
// It exists because xy's vocabulary was only visible by seeding a board and
// driving to each surface, which is slow enough that nobody looked — and a
// feature that never sees its own twin grows its own spacing, its own chip and
// its own idea of muted text. Look here BEFORE minting a class (the
// design-review skill), and here again to judge a change to the skin.
import { xyApp } from "./app.js";
import { icon } from "./icons_gen.js";
import S from "./i18nstrings.js";

const { el, byId } = xyApp;

// A demo label's fill, the same call paintLabels makes on a real board.
const FILLS: Record<string, string> = {
  green: "var(--green)", red: "var(--red)", blue: "var(--blue)",
  yellow: "var(--yellow)", purple: "var(--purple)", grey: "var(--structure)",
};

function section(title: string, note: string, ...kids: Array<HTMLElement | null>): HTMLElement {
  return el("section", { class: "gal-section" },
    el("h2", { class: "gal-title", text: title }),
    note ? el("p", { class: "hint", text: note }) : null,
    el("div", { class: "gal-demo" }, ...kids.filter(Boolean) as HTMLElement[]),
  );
}

function chip(name: string, color: string, on = false): HTMLElement {
  const c = el("span", { class: "label-pick" + (on ? " is-on" : ""), dataset: { c: color } }, el("span", { class: "label-pick-name", text: name }));
  c.style.background = FILLS[color] || "var(--structure)";
  return c;
}

function row(cls: string, ...kids: HTMLElement[]): HTMLElement {
  return el("div", { class: cls }, ...kids);
}

function seg(...words: string[]): HTMLElement {
  return el("div", { class: "seg" }, ...words.map((w, i) =>
    el("button", { class: "seg-btn" + (i === 0 ? " active" : ""), type: "button", text: w })));
}

// The layout utilities are the thing most often re-invented, so they lead — and
// each one is shown with the class that draws it, to be copied.
function utilities(): HTMLElement {
  const box = (t: string): HTMLElement => el("span", { class: "gal-box", text: t });
  const demo = (cls: string): HTMLElement => el("div", { class: "gal-util" },
    el("code", { class: "gal-code", text: cls }),
    el("div", { class: cls }, box(S.gallery.layout.box1()), box(S.gallery.layout.box2()), box(S.gallery.layout.box3())));
  return el("div", { class: "u-col u-gap-md" },
    demo("u-row u-gap-xs"),
    demo("u-row u-gap-sm u-align-center"),
    demo("u-row u-gap-md u-justify-between"),
    demo("u-col u-gap-sm"),
  );
}

function build(): void {
  const g = byId("gallery");
  g.replaceChildren(
    section(S.gallery.layout.title(), S.gallery.layout.note(), utilities()),

    section(S.gallery.text.title(), S.gallery.text.note(),
      el("div", { class: "u-col u-gap-sm" },
        el("h2", { class: "gal-h2", text: S.gallery.text.heading() }),
        el("p", { class: "hint", text: S.gallery.text.hint() }),
        el("p", { class: "hint hint-danger", text: S.gallery.text.danger() }),
        el("p", { class: "label-empty", text: S.gallery.text.empty() }))),

    section(S.gallery.buttons.title(), S.gallery.buttons.note(),
      el("div", { class: "u-row u-gap-sm u-wrap u-align-center" },
        el("button", { class: "btn", type: "button", text: S.gallery.buttons.save() }),
        el("button", { class: "btn btn-ghost", type: "button", text: S.gallery.buttons.cancel() }),
        el("button", { class: "btn btn-danger", type: "button", text: S.gallery.buttons.delete() }),
        el("button", { class: "btn btn-ghost btn-small", type: "button", text: S.gallery.buttons.minor() }),
        el("button", { class: "kadd", type: "button", title: S.gallery.buttons.addTitle() }, icon("plus")))),

    section(S.gallery.fields.title(), "", el("div", { class: "u-col u-gap-sm" },
      el("input", { class: "input", type: "text", placeholder: S.gallery.fields.plainPlaceholder() }),
      el("select", { class: "input" }, el("option", { value: "", text: S.gallery.fields.selectOption() })))),

    section(S.gallery.segment.title(), S.gallery.segment.note(), seg(S.gallery.segment.all(), S.gallery.segment.any(), S.gallery.segment.none())),

    section(S.gallery.labels.title(), S.gallery.labels.note(),
      el("div", { class: "label-picker" }, chip(S.gallery.labels.taken(), "green", true), chip(S.gallery.labels.rewrite(), "purple"), chip(S.gallery.labels.notTaken(), "red"), chip(S.gallery.labels.long(), "blue"))),

    section(S.gallery.listRows.title(), S.gallery.listRows.note(),
      el("div", { class: "u-col u-gap-xs" },
        row("member-row", el("span", { class: "member-name", text: S.gallery.listRows.memberName() }), el("span", { class: "member-role", text: S.gallery.listRows.memberRole() }), el("button", { class: "attach-del member-del", type: "button", text: "×" })),
        row("attach-row", el("button", { class: "attach-name", type: "button" }, icon("paperclip")), el("span", { class: "attach-size", text: "1.2 MB" })))),

    section(S.gallery.bars.title(), S.gallery.bars.note(),
      el("div", { class: "u-col u-gap-sm" },
        el("div", { class: "mass-bar" },
          el("span", { class: "mass-count", text: S.gallery.bars.massSelected(3) }),
          el("div", { class: "mass-acts" },
            el("button", { class: "input mass-act", type: "button", text: S.gallery.bars.massMove() }),
            el("button", { class: "input mass-act mass-act-danger", type: "button", text: S.gallery.bars.massDelete() })),
          el("button", { class: "input", type: "button", text: S.gallery.bars.massDone() })),
        el("div", { class: "filter-bar" },
          el("span", { class: "filter-bar-what", text: S.gallery.bars.filterWhat() }),
          el("span", { class: "hint", text: S.gallery.bars.filterHint() }),
          el("button", { class: "btn btn-ghost btn-small", type: "button", text: S.gallery.bars.filterReset() })))),

    section(S.gallery.feed.title(), S.gallery.feed.note(),
      el("div", { class: "u-col u-gap-sm" },
        el("div", { class: "tl-event" },
          el("div", { class: "tl-meta", text: S.gallery.feed.eventMeta() }),
          el("div", { class: "tl-comment", text: S.gallery.feed.eventComment() })),
        el("div", { class: "tl-event tl-skeleton" },
          el("div", { class: "tl-skeleton-bar tl-skeleton-meta" }),
          el("div", { class: "tl-skeleton-bar" }),
          el("div", { class: "tl-skeleton-bar tl-skeleton-short" })))),

    section(S.gallery.card.title(), S.gallery.card.note(),
      el("div", { class: "gal-list" },
        el("div", { class: "kcard" },
          el("div", { class: "kcard-labels" }, dot("green"), dot("red"), el("span", { class: "kcard-handout", title: S.gallery.card.handoutTitle() }, icon("file-text"))),
          el("div", { class: "kcard-title" }, el("span", { class: "kcard-num", text: "7. " }), document.createTextNode(S.gallery.card.question()))),
        el("div", { class: "kcard kcard-heading" }, el("div", { class: "kcard-title", text: S.gallery.card.heading() })))),
  );
}

function dot(color: string): HTMLElement {
  const d = el("span", { class: "kcard-label" });
  d.style.background = FILLS[color] || "var(--structure)";
  return d;
}

build();
