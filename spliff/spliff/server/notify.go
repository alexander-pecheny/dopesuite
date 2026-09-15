package spliffserver

import (
	"context"
	"strconv"
	"time"

	"spliff/spliff/storage/store"

	spliffstrings "spliff/i18nstrings"
)

// Spliff sends exactly three telegram DMs, and there is no settings page for
// them (spliff/docs/spec-v1.md):
//
//  1. a Join Request knocks on the Owner's door;
//  2. a Transaction created with something Unclaimed tells every other Member,
//     because an Unclaimed part is an invitation to claim a Share;
//  3. a Transaction where you hold a Payment or a Share, created or edited by
//     somebody else, tells you — once per Transaction per person per change.
//
// All of it is best-effort and none of it blocks a write: the Group page is the
// durable signal, a DM is a knock on the door. Somebody who logged in with a
// password has no telegram and simply is not knocked on.

// groupLink is where a DM points. It is built from SPLIFF_PUBLIC_URL, which is
// required outside development precisely so that this cannot be a guess.
func groupLink(groupID int64) string {
	return publicURL() + "/group/" + strconv.FormatInt(groupID, 10)
}

func transactionLink(txID int64) string {
	return publicURL() + "/transaction/" + strconv.FormatInt(txID, 10)
}

// notifyJoinRequest is rule 1. Nothing waits for it: the Owner's Invite Links
// panel lists the people waiting whether or not this arrives.
func (s *server) notifyJoinRequest(groupID, requesterID int64) {
	if s.bot == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		group, err := store.GroupByID(ctx, s.db, groupID)
		if err != nil {
			logDropped("notify: group", err)
			return
		}
		names, err := store.UserNames(ctx, s.db, []int64{requesterID})
		if err != nil {
			logDropped("notify: requester", err)
			return
		}
		tg, err := store.TelegramOf(ctx, s.db, []int64{group.OwnerID})
		if err != nil {
			logDropped("notify: owner telegram", err)
			return
		}
		if owner, ok := tg[group.OwnerID]; ok {
			s.notifyDM(ctx, owner, spliffstrings.Default.Notify.Join.Text(
				names[requesterID], group.Name, groupLink(groupID)))
		}
	}()
}

// transactionNotice is what one write has to tell people about.
type transactionNotice struct {
	GroupID int64
	TxID    int64
	ActorID int64
	Created bool // a creation, as against an edit — rule 2 is about creations
}

// notifyTransaction is rules 2 and 3, resolved into ONE message per person: a
// Member who both holds a Share and would hear about the Unclaimed part is
// knocked on once, because two DMs about one bill is what makes people mute a
// bot.
func (s *server) notifyTransaction(n transactionNotice) {
	if s.bot == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		tx, err := store.TransactionByID(ctx, s.db, n.TxID)
		if err != nil {
			logDropped("notify: transaction", err)
			return
		}
		group, err := store.GroupByID(ctx, s.db, n.GroupID)
		if err != nil {
			logDropped("notify: group", err)
			return
		}
		members, err := store.Members(ctx, s.db, n.GroupID)
		if err != nil {
			logDropped("notify: members", err)
			return
		}

		str := spliffstrings.Default
		// involved first, so that somebody who is both involved and merely
		// nearby gets the message that actually concerns them.
		text := map[int64]string{}
		if n.Created && tx.Unclaimed() > 0 {
			line := str.Notify.Unclaimed.Text(group.Name, tx.Description, transactionLink(n.TxID))
			for _, m := range members {
				text[m.UserID] = line
			}
		}
		involved := map[int64]bool{}
		for _, e := range tx.Payments {
			involved[e.MemberID] = true
		}
		for _, e := range tx.Shares {
			involved[e.MemberID] = true
		}
		line := str.Notify.Involved.Edited(group.Name, tx.Description, transactionLink(n.TxID))
		if n.Created {
			line = str.Notify.Involved.Created(group.Name, tx.Description, transactionLink(n.TxID))
		}
		for member := range involved {
			text[member] = line
		}
		// Nobody is told about their own act.
		delete(text, n.ActorID)
		if len(text) == 0 {
			return
		}

		ids := make([]int64, 0, len(text))
		for id := range text {
			ids = append(ids, id)
		}
		tg, err := store.TelegramOf(ctx, s.db, ids)
		if err != nil {
			logDropped("notify: telegram ids", err)
			return
		}
		for id, chat := range tg {
			s.notifyDM(ctx, chat, text[id])
		}
	}()
}
