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
    el("div", { class: cls }, box("один"), box("два"), box("три")));
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
    section("Раскладка — .u-*", "Композиция вместо нового класса: display:flex + gap + align-items уже написан.", utilities()),

    section("Текст", ".hint несёт margin:0 — отступы даёт контейнер, не ребёнок.",
      el("div", { class: "u-col u-gap-sm" },
        el("h2", { class: "gal-h2", text: "Заголовок раздела" }),
        el("p", { class: "hint", text: "Пояснение под контролом: приглушённое, того же кегля." }),
        el("p", { class: "hint hint-danger", text: "Предупреждение, которое нельзя прочесть как подсказку." }),
        el("p", { class: "label-empty", text: "меток нет — пустое состояние внутри панели" }))),

    section("Кнопки", "Одна кнопка на действие; btn-ghost — второстепенное, btn-danger — необратимое.",
      el("div", { class: "u-row u-gap-sm u-wrap u-align-center" },
        el("button", { class: "btn", type: "button", text: "Сохранить" }),
        el("button", { class: "btn btn-ghost", type: "button", text: "Отмена" }),
        el("button", { class: "btn btn-danger", type: "button", text: "Удалить" }),
        el("button", { class: "btn btn-ghost btn-small", type: "button", text: "Мелкое действие" }),
        el("button", { class: "kadd", type: "button", title: "плюс" }, icon("plus")))),

    section("Поля", "", el("div", { class: "u-col u-gap-sm" },
      el("input", { class: "input", type: "text", placeholder: "Обычное поле" }),
      el("select", { class: "input" }, el("option", { value: "", text: "Выбор" })))),

    section("Сегмент — .seg", "Выбор одного из немногих: режим, вид, версия.", seg("все", "любая", "ни одной")),

    section("Метки", "Один чип на всё: карточка, «Массовое действие», «Фильтр по меткам».",
      el("div", { class: "label-picker" }, chip("взяли", "green", true), chip("надо переписать", "purple"), chip("не взяли", "red"), chip("длинная метка на две строки", "blue"))),

    section("Строки списков", "Одна форма на строку сущности: участник, вложение, ссылка.",
      el("div", { class: "u-col u-gap-xs" },
        row("member-row", el("span", { class: "member-name", text: "аня" }), el("span", { class: "member-role", text: "редактор" }), el("button", { class: "attach-del member-del", type: "button", text: "×" })),
        row("attach-row", el("button", { class: "attach-name", type: "button" }, icon("paperclip")), el("span", { class: "attach-size", text: "1.2 MB" })))),

    section("Полосы над доской", "Мода доски объявляет себя строкой .board-main, а не наложением поверх карточек.",
      el("div", { class: "u-col u-gap-sm" },
        el("div", { class: "mass-bar" },
          el("span", { class: "mass-count", text: "Выбрано: 3 карточки" }),
          el("div", { class: "mass-acts" },
            el("button", { class: "input mass-act", type: "button", text: "Переместить" }),
            el("button", { class: "input mass-act mass-act-danger", type: "button", text: "Удалить" })),
          el("button", { class: "input", type: "button", text: "Готово" })),
        el("div", { class: "filter-bar" },
          el("span", { class: "filter-bar-what", text: "Со всеми метками: взяли" }),
          el("span", { class: "hint", text: "перетаскивание внутри списка выключено" }),
          el("button", { class: "btn btn-ghost btn-small", type: "button", text: "Сбросить" })))),

    section("Лента", "Событие и его скелет: у скелета та же высота, чтобы панель не прыгала.",
      el("div", { class: "u-col u-gap-sm" },
        el("div", { class: "tl-event" },
          el("div", { class: "tl-meta", text: "аня · вчера" }),
          el("div", { class: "tl-comment", text: "Комментарий в ленте." })),
        el("div", { class: "tl-event tl-skeleton" },
          el("div", { class: "tl-skeleton-bar tl-skeleton-meta" }),
          el("div", { class: "tl-skeleton-bar" }),
          el("div", { class: "tl-skeleton-bar tl-skeleton-short" })))),

    section("Карточка", "Вопрос и заголовок: модификатор есть только у тех видов, у которых он что-то меняет.",
      el("div", { class: "gal-list" },
        el("div", { class: "kcard" },
          el("div", { class: "kcard-labels" }, dot("green"), dot("red"), el("span", { class: "kcard-handout", title: "Раздаточный материал" }, icon("file-text"))),
          el("div", { class: "kcard-title" }, el("span", { class: "kcard-num", text: "7. " }), document.createTextNode("Текст вопроса, обрезанный по числу строк из «Изменить размеры»."))),
        el("div", { class: "kcard kcard-heading" }, el("div", { class: "kcard-title", text: "Заголовок тура" })))),
  );
}

function dot(color: string): HTMLElement {
  const d = el("span", { class: "kcard-label" });
  d.style.background = FILLS[color] || "var(--structure)";
  return d;
}

build();
