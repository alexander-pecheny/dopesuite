"""What the four emitters share of the transcript format: a Block's table."""


def table(out, at, rows):
    """`[таблица at]`, one `место | участник` line per (place, name)."""
    out += ["", f"[таблица {at}]"]
    for place, name in rows:
        out.append(f"{place:>2g} | {name}")
