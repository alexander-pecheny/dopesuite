# Spliff — shared expenses

Splitwise-shaped expense sharing for a circle of friends: people in a Group record who paid for whom, in any currency, and the Group always knows who owes whom. Terms decided 2026-09-15.

## Language

**Group**:
A circle of people who share expenses with each other. Every Transaction belongs to exactly one Group, and debts never cross Groups.

**Base currency**:
The one currency a Group states its balances and debts in. Chosen when the Group is made, changeable at any time; changing it restates every balance, it rewrites nothing.

**Transaction**:
One dated movement of money recorded in a Group, in the currency it actually happened in. It keeps its own currency and amount forever and is converted to the Base currency only when read.

**Rate table**:
Every currency's rate on one calendar day, as fetched from the rate source that day. Tables exist only for days one was fetched.

**Rate date**:
The day whose Rate table a Transaction converts with: the Transaction's own date when a table exists for it, else the nearest day that has one (the earlier one on a tie). A person who backdates a Transaction gets whatever table is nearest.

**Pinned rate**:
A rate a person writes onto one Transaction because it is the one their bank really charged: a target currency and a number. It takes precedence over the Rate table for that Transaction, and when the Group's Base currency is not the one it names, the amount goes through the Pinned rate first and the Rate table for the rest of the way. Designed for from the start; not offered in v1.

**Payment**:
One Member's part of handing over a Transaction's money: who paid and how much. A Transaction has one or more Payments, summing to its total; a restaurant bill two people paid is one Transaction with two Payments.
_Avoid_: payer (as a field — a Transaction has Payments, and "a payer" is whoever holds one)

**Share**:
The part of a Transaction's total one member is answerable for, as an amount in the Transaction's currency. An even split is a way of typing amounts, never what is kept; the odd minor units of a derived split go one each to the payers in descending Payment order, then the others in split order.

**Unclaimed**:
The part of a Transaction's total no Share accounts for. It belongs to nobody and is owed by nobody: the payers absorb it, pro rata to their Payments, until Members claim it by setting their own Shares. Shares may never exceed the total.
_Avoid_: remainder, residue (those suggest an error; Unclaimed is a normal state)

**Settlement**:
A Transaction with one Payment and one Share, the whole amount each: B hands A the cash, so B's Payment and A's Share are both the total. Not a separate kind of record.

**Claim**:
A member setting their own Share on a Transaction someone else paid. Every member may edit every field of every Transaction in their Group; a Claim is just the common case.

**History**:
The record of every change to a Transaction — who, when, what — visible to every member of the Group. Trust is social; History is what makes it auditable.

**Net balance**:
One Member's Payments minus their Shares minus their absorbed Unclaimed, across the Group, in the Base currency, each amount converted at its Transaction's Rate date. A Group's Net balances always sum to zero.

**Debt graph**:
The Group's Net balances resolved into the fewest transfers the greedy pairing yields: largest creditor against largest debtor, repeat, ties by join order. Computed whenever asked, a pure function of the ledger, never edited by hand.
_Avoid_: simplified debts (there is no unsimplified view)

**Member**:
A person with a place in a Group. Every Share and every Payment names a current Member: nobody leaves or is removed while their Net balance is non-zero, so the Debt graph never names a ghost.

**Phantom**:
A Member with a name and no account: someone at the table who is not on Spliff yet, or a stand-in for testing. The Owner makes one; it pays and holds Shares like anyone. A person who joins through an Invite Link may claim a Phantom, and its Payments, Shares and History become theirs; a Phantom at zero balance can be removed like any Member.
_Avoid_: placeholder, ghost (a ghost is what the zero-balance rule prevents), fake user

**Owner**:
The Member who made the Group, or was handed it since. The Owner alone removes Members, mints and revokes Invite Links, decides Join Requests, deletes the Group and hands ownership on; the Owner cannot leave without handing over. Everything else — every Transaction, the Group's name and Base currency — belongs to every Member alike.
_Avoid_: admin, creator (the creator may no longer be the Owner)

**Invite Link**:
As in xy: a URL the Owner mints that admits its holder to the Group as a Member. It may cap uses, expire or hold the joiner for approval as a Join Request, and it is revocable. It grants Membership, never a role.

**Photo**:
A picture a Member attaches to a Transaction for the others' reference — the receipt, the bill. Kept as the app re-encodes it, never as the phone sent it, so nothing but the picture travels. A Transaction may carry several; any Member adds or removes one.
_Avoid_: attachment (xy's word; a Photo is always an image), receipt (it may be anything)
