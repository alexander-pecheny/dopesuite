# The Telegram export record

Every Telegram export, by the Python chgksuite or by this Go port, writes down
what it posted in `~/.chgksuite/telegram.db`. The file is kept across runs, so a
post can be found and corrected after the export finished: a typo in question
14's answer, a missing source, a wrong picture. Both tools write the same file
with the same schema; this page is the contract between them.

A dry run records nothing: its message ids are invented.

## The file

SQLite in WAL mode, `PRAGMA user_version = 1`. Open it with a busy timeout: an
export may be writing while you read.

### `exports`: one row per run

| column | what it holds |
|---|---|
| `id` | the run |
| `tool` | `chgksuite` (Python) or `chgksuite-go` |
| `tool_version` | the build that ran |
| `source_path` | the absolute path of the exported file |
| `source_sha256` | the hex SHA-256 of that file's bytes, to tell whether it has changed since |
| `tgaccount` | the key of the bot token in `~/.chgksuite/telegram.toml`; `''` for the default, and always `''` from the Go port, which takes its token from `--token` or `$CHGKSUITE_TG_TOKEN` |
| `bot_id` | the posting bot's Telegram id (`getMe`) |
| `channel_id` | the channel, `-100…` |
| `chat_id` | the linked discussion group, `-100…`; NULL if none |
| `started_at` | ISO 8601 with a UTC offset |
| `finished_at` | when the run completed; NULL while it runs, or for good if it crashed |

### `posts`: one row per Telegram message

A row is written right after Telegram accepts the message, so a run that died
halfway still lists everything that reached the channel.

| column | what it holds |
|---|---|
| `export_id` | the run, `exports.id` |
| `chat_id` | where the message is: the channel, or the discussion group for a poll posted under a comment |
| `message_id` | its id in that chat |
| `link` | `https://t.me/c/<id>/<message>` |
| `question_number` | the number as the packet prints it; NULL for a post that is not a question's |
| `role` | `heading`, `navigation`, `question`, `answer`, `comment`, `handout`, `poll` or `other` |
| `content_type` | `text`, `photo` or `poll` |
| `reply_to_message_id` | the message it replies to, if any |
| `text` | the exact text or caption sent |
| `parse_mode` | as sent (`HTML`); NULL if none |
| `entities` | the entities sent, as a JSON array; NULL if none |
| `sent_at` | ISO 8601 with a UTC offset |

A message that bundles several parts takes the role of its main part, and
`text` holds all of it. The rich export posts each question as one message with
its answer, comment and pictures folded in, so that message is a `question` row
(`photo` when it carries pictures), and a question's poll is a `poll` row with
the same `question_number`. The packet's title and each tour's heading are
`heading` rows; the pinned index at the end is `navigation`.

The Go port writes rich messages (`sendRichMessage`): their `text` is the
`rich_message.html` that was sent.

### `messages` and `bot_status`

The Python tool's bot inbox: every update its bot received, and whether the bot
came up. Rows older than seven days are pruned when an export starts. The Go
port keeps its inbox in memory and leaves these tables empty. Nothing here is
needed to edit a post.

## Editing a post

1. Find the run: the latest `exports` row for the file, by `source_path`, with
   `finished_at` set.

   ```sql
   SELECT * FROM exports
   WHERE source_path = '/abs/path/pack.4s' AND finished_at IS NOT NULL
   ORDER BY id DESC LIMIT 1;
   ```

2. Find the message:

   ```sql
   SELECT chat_id, message_id, content_type, text, parse_mode FROM posts
   WHERE export_id = ? AND question_number = '14' AND role = 'question';
   ```

3. Edit it as the bot that posted it: Telegram lets only that bot edit a
   message. Its token is the one under `tgaccount` in
   `~/.chgksuite/telegram.toml` (for the Go port, the token that run was given),
   and `bot_id` says which bot that must be. Start from the recorded `text`,
   change what needs changing, and send it with the same `parse_mode`:
   `editMessageText` for a `text` row, `editMessageCaption` for a `photo` row.
   A rich message is edited with the Bot API's matching method for rich
   messages. A poll cannot be edited.

The record is not updated by an edit made this way.
