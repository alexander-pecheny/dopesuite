# The Telegram export record: `~/.chgksuite/telegram.db`

This is a contract between the Python and Go chgksuite, for developers and agents; the user documentation is the chgksuite.pecheny.me site.

Every Telegram export by chgksuite, the Python tool or the Go one, writes down
what it posted in `~/.chgksuite/telegram.db`, an SQLite file kept across runs.
It exists so that posts can be found and corrected after the export, by hand or
by an agent. Both tools share the file and its schema; a change to one is a
change to both.

Dry runs are not recorded: their message ids are made up.

## Tables

`exports` has one row per export.

| Column | Meaning |
| --- | --- |
| `id` | The export's number. |
| `tool`, `tool_version` | `chgksuite` (Python) or `chgksuite-go`, and its version. |
| `source_path`, `source_sha256` | The absolute path of the exported file and the sha256 of its bytes. A merged export lists its files one per line and hashes them in that order. |
| `tgaccount` | The account name the Python tool looked the bot token up by in `telegram.toml`; empty for the default token and for the Go tool. |
| `bot_id` | The posting bot's id. A bot token starts with this number and a colon. |
| `channel_id`, `chat_id` | The channel and its discussion group, as `-100…` ids. |
| `started_at`, `finished_at` | When the export started and finished. An empty `finished_at` means it is still running or it stopped halfway; its posts up to that point are still recorded. |

`posts` has one row per message the export sent, written as soon as Telegram
accepted it.

| Column | Meaning |
| --- | --- |
| `export_id` | The export it belongs to. |
| `chat_id`, `message_id` | Where the message is: the channel or the discussion group. |
| `link` | A t.me link to it. |
| `question_number` | The question's number as printed in the pack; empty for posts that are not about one question, including a post that holds several questions of a theme. |
| `role` | What the message is; see below. A message holding several parts is named after its main part. |
| `content_type` | `text`, `photo` or `poll`. |
| `reply_to_message_id` | The message it replies to in the discussion group, if any. |
| `text` | The exact text, caption or poll question sent. |
| `parse_mode` | `HTML` for `sendMessage` and `sendPhoto`; `rich_html` for `sendRichMessage`, whose `text` is the rich HTML as sent; empty for polls. |
| `entities` | A JSON array of entities, if any were sent; the exports send none today. |
| `sent_at` | When it was sent. |

Roles:

- `heading`: the pack, tour or theme heading, with any text around it.
- `navigation`: the pinned post linking to the tours, and its comments.
- `question`: the question's post. In the rich format it also holds the answer, comment, sources and author in a collapsed block.
- `answer`: a reply in the discussion group continuing a question too long for one post, with its answer.
- `comment`: a reply in the discussion group holding what did not fit the question's post: the comment, sources, author or the answer's pictures.
- `handout`: a handout picture posted ahead of its question.
- `poll`: a poll after a question, a tour or the pack.
- `other`: anything else.

The discussion group's automatic copy of each channel post is not recorded:
Telegram updates it when the channel post is edited.

`messages` and `bot_status` are the Python tool's inbox: what its bot
received during a run. Rows older than seven days are deleted at the next run.
The Go tool keeps its inbox in memory and leaves these tables empty.

Timestamps are ISO 8601 with a UTC offset.

## Finding and editing a post

The latest export of a file, and question 14's posts in it:

```sql
SELECT p.chat_id, p.message_id, p.role, p.content_type, p.parse_mode, p.text,
       e.bot_id, e.tgaccount
FROM posts p JOIN exports e ON e.id = p.export_id
WHERE e.id = (SELECT max(id) FROM exports WHERE source_path = '/path/to/pack.4s')
  AND p.question_number = '14'
ORDER BY p.id;
```

Only the bot that posted a message can edit it, so use the token whose number
before the colon is `bot_id`. The Python tool keeps tokens in
`~/.chgksuite/telegram.toml`: `bot_token` for an empty `tgaccount`, otherwise
`bot_tokens.<tgaccount>`. The Go tool takes it from `--token` or
`CHGKSUITE_TG_TOKEN`.

Then call the Bot API with `chat_id` and `message_id`:

- `text` posts with `parse_mode` `HTML`: `editMessageText` with the new text and `parse_mode` `HTML`.
- `photo` posts: `editMessageCaption` with the new caption and `parse_mode` `HTML`.
- `rich_html` posts: the Bot API's edit method for rich messages, with the new rich HTML.
- Polls cannot be edited; delete and resend them.

Update the stored `text` too if the record should match the channel; neither
tool reads it back.
