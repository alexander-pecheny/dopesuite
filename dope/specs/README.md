# Спецификация чемпионата ЭК

Эта папка фиксирует целевую модель до реализации. Документы короткие и читаются сверху вниз.

## Карта документов

- [01-existing.md](01-existing.md) — что уже реализовано в проекте.
- [02-domain.md](02-domain.md) — сущности чемпионата и правила жизненного цикла.
- [03-sqlite.md](03-sqlite.md) — предлагаемая структура SQLite.
  - [03a-sqlite-core.md](03a-sqlite-core.md) — справочники и сетка.
  - [03b-sqlite-match.md](03b-sqlite-match.md) — состояние боя, результаты, события.
  - [03c-sqlite-field-rules.md](03c-sqlite-field-rules.md) — соглашения для ID, code, index, position.
  - [03d-sqlite-match-fields.md](03d-sqlite-match-fields.md) — подробности `match_slots`, тем, ответов, результатов.
- [04-scheme-json.md](04-scheme-json.md) — формат JSON-схемы турнира.
  - [04a-scheme-sources.md](04a-scheme-sources.md) — источники слотов и пересевы.
- [05-studchr-ek.md](05-studchr-ek.md) — схема СтудЧР-2026 ЭК как частный случай.
- [06-ui-api.md](06-ui-api.md) — страницы, API и поведение интерфейса.
- [07-implementation.md](07-implementation.md) — порядок внедрения.
- [08-auth.md](08-auth.md) — учетные записи, инвайты и логин через Telegram-бот.
- [09-tournaments-and-games.md](09-tournaments-and-games.md) — турниры, игры и наследование состава.
- [../static/schemes/studchr-ek-2026.json](../static/schemes/studchr-ek-2026.json) — сгенерированная JSON-сетка СтудЧР для импорта.

## Цель

Перейти от одного боя в `match_state.json` к полноценному чемпионату в SQLite: импорт JSON-сетки, команды, игроки, площадки, сетка боев, переходы по местам, пересевы, страницы ведущего и зрителя.

## Не цель

Не копировать визуальный дизайн Google Sheets. Таблица используется как источник структуры турнира, а интерфейс должен развивать уже существующий дизайн проекта: компактные таблицы, sticky-колонки, текущие цвета, host/viewer-разделение и SSE-синхронизация.
