# qy — glossary

**Contest.** One year's «Вопрос года». It is named by its year and runs from
November to January. Its stages are calendar dates in Moscow time, each
inclusive at both ends: nomination, the first stage (prelim), the factcheck,
the final, and the results day. A new Contest starts from the previous one's
dates moved 52 weeks on, so each stage falls on the same weekday.

**Nomination.** A logged-in person's pick, in one of their slots. The first
five slots are main picks and the next three are reserves. A slot starts as
one box for a link or a whole question: a gotquestions or telegram link is
loaded into the question's fields, a text is parsed into them. A question is
published when it has a publication link; one without needs its tournament's
link. A gotquestions question may be saved as its link alone. A slot may
carry a handout file.

**Rules check.** The automatic check of rule 2 on a nominated question
(`domain/eligibility`): its tournament started in the contest's window and
cannot still be played for rating when the shortlist is published, and no
juror or admin is among its authors or its tournament's editors (by name,
and by rating id for an editor). It only
warns. A nominator who thinks it is wrong saves the question with an
**appeal**, which the admins read on the question's page and decide.

**Question.** A nominated question. `orig` is what the nominator gave, parsed,
with its credits. `clean` is the anonymised copy the jury reads. Two
nominations of one gotquestions link share a Question. The background pass
merges two Questions whose text and answer have nearly the same words (one
pasted, one by link, say), keeping the one with the link; an admin merges other
duplicates by hand, and can undo either.

**Suggestion.** A proposed redaction of `clean`: replace a quote in a field. The
rule pass and the language model make them, and an admin accepts or rejects
each one.

**Counted.** The Questions that enter the contest under the 5+3 rule. Each
nominator, in the order they first nominated, keeps their first five slots that
are neither a duplicate of someone earlier nor rejected. A reserve therefore
counts only in place of a main pick that did not.

**Preliminary sheet.** Nominations collected by a Google Form before the
contest opened, a rating ID and a link a row, loaded by `import-sheet`. They
count for a player only while nobody with that rating ID nominates through the
form. A Question from the sheet that two people nominate (in the sheet or the
form) counts outside everybody's slots.

**QY number.** A counted Question's number, dealt in random order once
nomination is over.

**Portion.** The QY numbers the first stage opens together, `portion_size` of
them every `portion_days` days.

**Juror.** A person on the expert or the people's jury of one year, named by
telegram handle. The people's jury reads only published Questions.

**Like.** A juror's mark on a Question in the first stage. During the factcheck
a juror may withdraw a like but not add one.

**Shortlist.** Exactly the expert jury's top `expert_cutoff` by likes (12 by
the rules), then the people's jury's top `people_cutoff` from the rest (3),
then any Question an admin pinned. A declined author or an admin's removal
takes a Question out before the count. An admin freezes the Shortlist, which
opens the final, and cannot while a Tie is open.

**Tie.** Questions with equal points at a cut of the Shortlist, more of them
than the places left. During the factcheck the expert jury marks them from 1 to
10 in a tie-break round, counted like a Ballot. Only a juror who marked every
tied Question counts, and the Tie is settled once the count has no tie at the
border. While the expert jury's Tie is open, the people's cut is made as if
every Question in it were through.

**Ballot.** An expert juror's 1–10 marks for the frozen Shortlist. It counts
only when every Question is marked. The jury's order is the sum of ranks (Borda
count), with the plain sum of marks as the tie-break.

**Popular vote.** Any logged-in person's three picks, worth 5, 4 and 3 points.
One per account, with the voter's rating id.
