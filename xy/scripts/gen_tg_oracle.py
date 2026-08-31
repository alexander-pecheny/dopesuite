"""Records what chgksuite's Telegram export would send, without a bot.

Builds a TelegramExporter without its __init__ (which wants a token, the
network and a sidecar bot), stubs every call that reaches Telegram, and runs
export() over each fixture. The transcript it prints is the oracle
internal/chgk/tg is checked against; regenerate with

    uv run --project ~/chgksuite/chgksuite python xy/scripts/gen_tg_oracle.py \
        > xy/internal/chgk/tg/testdata/transcript.json
"""

import json
import os
import sys

sys.path.insert(0, os.path.expanduser("~/chgksuite/chgksuite"))

from chgksuite.common import DefaultArgs, get_source_dirs  # noqa: E402
from chgksuite.composer import telegram as tgmod  # noqa: E402
from chgksuite.composer.chgksuite_parser import parse_4s  # noqa: E402
from chgksuite.composer.telegram import TelegramExporter  # noqa: E402

tgmod.time.sleep = lambda *a, **k: None
tgmod.random.randint = lambda a, b: a

RESOURCES = get_source_dirs()[1]
FIXTURES = os.path.join(
    os.path.dirname(os.path.abspath(__file__)), "..", "internal", "chgk", "tg", "testdata"
)


def make_args(**over):
    args = DefaultArgs()
    args.labels_file = os.path.join(RESOURCES, "labels_ru.toml")
    args.regexes_file = os.path.join(RESOURCES, "regexes_ru.json")
    args.game = "chgk"
    args.tgchannel = "-1001111111111"
    args.tgchat = "-1002222222222"
    args.dry_run = False
    args.nospoilers = False
    args.disable_asterisks_processing = False
    args.resize_images = False
    args.skip_until = None
    args.add_polls = False
    args.poll_config = os.path.join(RESOURCES, "poll_config.toml")
    args.only_question_number = False
    for k, v in over.items():
        setattr(args, k, v)
    return args


def run(source, args, targetdir):
    structure = parse_4s(source, game=args.game)
    exporter = TelegramExporter.__new__(TelegramExporter)
    from chgksuite.composer.composer_common import BaseExporter

    BaseExporter.__init__(exporter, structure, args, {"targetdir": targetdir, "tmp_dir": targetdir})
    exporter.qcount = 1
    exporter.number = 1
    exporter.tg_heading = None
    exporter.rich_mode = True
    exporter.si_mode = args.game in ("si", "troika")
    exporter.channel_id = None
    exporter.chat_id = None

    calls = []
    msg_id = [1000]

    def fake_post(chat_id, text, photo, reply_to_message_id=None):
        msg_id[0] += 1
        call = {"call": "post", "chat": str(chat_id), "reply_to": reply_to_message_id}
        if isinstance(text, dict):
            call["html"] = text["html"]
            call["media"] = [mid for mid, _ in text.get("media_files", [])]
        else:
            call["text"] = text
            call["photo"] = bool(photo)
        calls.append(call)
        return {"message_id": msg_id[0], "chat": {"id": chat_id}}

    def fake_discussion(channel_id, message_id):
        msg_id[0] += 1
        calls.append({"call": "discussion_of", "message_id": message_id})
        return msg_id[0]

    def fake_api(method, data=None, files=None):
        calls.append({"call": method, "data": data})
        return {"message_id": 1}

    exporter._post = fake_post
    exporter.get_discussion_message = fake_discussion
    exporter.send_api_request = fake_api
    exporter.verify_access = lambda *a, **k: True
    exporter.resolve_username_to_id = lambda *a, **k: None
    exporter.export()
    return calls


def main():
    out = []
    for name in sorted(os.listdir(FIXTURES)):
        if not name.endswith(".4s"):
            continue
        path = os.path.join(FIXTURES, name)
        with open(path, encoding="utf8") as f:
            source = f.read()
        for tag, over in (
            ("plain", {}),
            ("nospoilers", {"nospoilers": True}),
            ("polls", {"add_polls": True}),
            ("skip", {"skip_until": 2}),
            ("asterisks", {"disable_asterisks_processing": True}),
            ("english", {
                "language": "en",
                "labels_file": os.path.join(RESOURCES, "labels_en.toml"),
                "regexes_file": os.path.join(RESOURCES, "regexes_en.json"),
            }),
        ):
            out.append(
                {
                    "fixture": name[: -len(".4s")],
                    "variant": tag,
                    "calls": run(source, make_args(**over), FIXTURES),
                }
            )
    print(json.dumps(out, ensure_ascii=False, indent=1))


if __name__ == "__main__":
    main()
