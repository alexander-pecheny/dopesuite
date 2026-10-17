# qy — glossary

**Contest.** One year's «Вопрос года». It is named by its year and runs from
November to January. Its stages are calendar dates in Moscow time, each
inclusive at both ends: nomination, the first stage (prelim), the factcheck,
the final, and the results day. A new Contest starts from the previous one's
dates moved 52 weeks on, so each stage falls on the same weekday.

**Nomination.** A logged-in person's pick, in one of their slots. The first
five slots are main picks and the next three are reserves. A slot holds a
gotquestions link or a question's full text.

**Question.** A nominated question. `orig` is what the nominator gave, parsed,
with its credits. `clean` is the anonymised copy the jury reads. Two
nominations of one gotquestions link share a Question; an admin merges other
duplicates.

**Suggestion.** A proposed redaction of `clean`: replace a quote in a field. The
rule pass and the language model make them, and an admin accepts or rejects
each one.

**Counted.** The Questions that enter the contest under the 5+3 rule. Each
nominator, in the order they first nominated, keeps their first five slots that
are neither a duplicate of someone earlier nor rejected. A reserve therefore
counts only in place of a main pick that did not.

**QY number.** A counted Question's number, dealt in random order once
nomination is over.

**Portion.** The QY numbers the first stage opens together, `portion_size` of
them every `portion_days` days.

**Juror.** A person on the expert or the people's jury of one year, named by
telegram handle. The people's jury reads only published Questions.

**Like.** A juror's mark on a Question in the first stage. During the factcheck
a juror may withdraw a like but not add one.

**Shortlist.** The expert jury's top `expert_cutoff` by likes, ties included,
then the people's jury's top `people_cutoff` from the rest, then any Question an
admin pinned. A declined author or an admin's removal takes a Question out
before the count. An admin freezes the Shortlist, which opens the final.

**Ballot.** An expert juror's 1–10 marks for the frozen Shortlist. It counts
only when every Question is marked. The jury's order is the sum of ranks (Borda
count), with the plain sum of marks as the tie-break.

**Popular vote.** Any logged-in person's three picks, worth 5, 4 and 3 points.
One per account, with the voter's rating id.
