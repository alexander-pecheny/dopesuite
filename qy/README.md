# qy

«Вопрос года» at qy.pecheny.me: nomination through the site with telegram
login, an anonymisation pass by rules and by a model, the admins' review, the
two jury stages, the popular vote, the results, and a CMS holding every year's
pages from 2021 on.

## Running a contest

1. On `/admin`, create the contest. Its dates are last year's moved 52 weeks
   on; check them on the contest page. Add the jurors by telegram handle.
2. Nomination runs on `/nominate`. Each nomination is parsed on the spot, and
   the background pass then fetches the tournament's dates and asks the model
   for redactions.
3. On the question list, review each question: accept or reject the
   redactions, edit the jury's copy, merge duplicates, mark it published, and
   approve it.
4. After nomination, deal the QY numbers on the contest page. Portions open on
   `/vote` on their own dates.
5. During the factcheck, write each shortlisted question's factcheck, and mark
   the authors who decline. Jurors may withdraw likes on `/vote`. Freeze the
   shortlist on the first-stage page when it is final.
6. The final runs on `/final` (expert jury) and `/popular` (everyone). Publish
   the results on the final page; `/<year>/results/` then shows them. Write
   `<year>/results-intro`, `<year>/shortlist-intro` and `<year>/stories` in the
   CMS.

## Setting up an instance

Everything below is done by hand once; `deploy.py` only ships the binary.

- `/etc/qy.env` from `.env.example`, a `qy` user, `/var/lib/qy`, and the unit
  from `deploy/qy.service.example`.
- A bot from @BotFather for telegram login (`QY_BOT_TOKEN`, `QY_BOT_USERNAME`),
  and an OpenRouter key for the model (`QY_OPENROUTER_KEY`).
- A copy of gotquestions for nominations by link, while its API is down:
  `just import-gq <dump.sql.gz> /var/lib/qy/gq.db`, then `QY_GQ_DB` names it.
- Caddy:

  ```
  qy.pecheny.me {
      reverse_proxy localhost:9686
  }
  ```

  and, in the pecheny.me site block, a redirect that keeps the old addresses
  working (`handle_path` drops the `/qy` prefix, so `pecheny.me/qy/2025/results/`
  lands on `qy.pecheny.me/2025/results/`):

  ```
  handle_path /qy/* {
      redir https://qy.pecheny.me{uri} permanent
  }
  ```
- `qy-server import-archive <vopros_goda checkout>` once, to load 2021–2025.
