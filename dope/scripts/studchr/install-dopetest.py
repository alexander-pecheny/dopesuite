"""Installs the built СтудЧР фест as dopetest's database, keeping its accounts.

The фест is built by TestStudchrWholeFest into a database of its own, which has
no real users — it was made by a test server. Dropping it in as-is would lock
everyone out, so the accounts, their invites and their live sessions are carried
over from the database being replaced, and every user who organises anything
today is made an organiser of the championship.

Run on the box, next to the two files:

    python3 install-dopetest.py /var/lib/dopetest/fest.db studchr.db
"""
import sqlite3
import sys

# Carried across verbatim: who may log in, and what they are holding. Everything
# else in the old database is the фест data the new one replaces.
KEEP = ["users", "invites", "telegram_login_codes", "sessions"]


def main(live, incoming):
    db = sqlite3.connect(incoming)
    db.execute("pragma foreign_keys = off")
    db.execute("attach database ? as old", (live,))

    # The фест the new database carries, before its own accounts are cleared —
    # its creator is about to stop existing.
    fest = db.execute("select id from fests order by id limit 1").fetchone()
    if fest is None:
        sys.exit("во входящей базе нет феста")
    festID = fest[0]

    db.execute("delete from fest_organizers")
    db.execute("delete from sessions")
    db.execute("delete from users")
    for table in KEEP:
        columns = [row[1] for row in db.execute(f"pragma table_info({table})")]
        shared = [c for c in columns if c in
                  {row[1] for row in db.execute(f"pragma old.table_info({table})")}]
        names = ", ".join(shared)
        db.execute(f"insert into {table}({names}) select {names} from old.{table}")
    db.execute("update fests set created_by = (select id from users where is_system = 1) where id = ?", (festID,))

    # Whoever organises anything on the live instance keeps organising here.
    organisers = [row[0] for row in db.execute(
        "select distinct user_id from old.fest_organizers where user_id in (select id from users)")]
    now = db.execute("select max(created_at) from users").fetchone()[0]
    for user in organisers:
        db.execute("""
insert or ignore into fest_organizers(fest_id, user_id, role, added_at) values(?, ?, 'admin', ?)""",
                   (festID, user, now))
    db.commit()
    print(f"фест {festID}: организаторов {len(organisers)}, "
          f"пользователей {db.execute('select count(*) from users').fetchone()[0]}")
    db.close()


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
