# Spliff — shared expenses

Expense sharing for a circle of friends, shaped like Splitwise. People in a Group record who paid for whom, in any currency, and the Group always knows who owes whom. These terms were decided on 2026-09-15.

## Language

**Group**:
A circle of people who share expenses with each other. Every Transaction belongs to exactly one Group, and debts never cross Groups.

**Base currency**:
The single currency a Group shows its balances and debts in. It is chosen when the Group is created and can be changed at any time. Changing it only restates the balances; nothing stored is rewritten.

**Transaction**:
One dated movement of money recorded in a Group, in the currency it actually took place in. It keeps that currency and that amount permanently, and is converted to the Base currency only when someone reads it.

**Rate table**:
The rate of every currency on one calendar day, as fetched from the rate source on that day. A table exists only for a day on which one was actually fetched.

**Rate date**:
The day whose Rate table is used to convert a Transaction. That is the Transaction's own date if a table exists for it, and otherwise the nearest day that has one; if two days are equally near, the earlier one is used. Someone who backdates a Transaction therefore gets whichever table is nearest to that date.

**Pinned rate**:
A rate that someone writes onto one particular Transaction, because it is the rate their bank actually charged. It consists of a target currency and a number. For that Transaction it wins over the Rate table. If the Group's Base currency is not the currency the Pinned rate names, the amount is converted through the Pinned rate first and then through the Rate table for the remaining step. The design allows for this from the start, but v1 does not offer it.

**Payment**:
One Member's part in handing over the money for a Transaction: who paid, and how much. A Transaction has one or more Payments, and they add up to its total. A restaurant bill that two people paid is one Transaction with two Payments.
_Avoid_: a `payer` field. A Transaction has Payments, and "a payer" is simply whoever holds one.

**Share**:
The part of a Transaction's total that one member is responsible for, stored as an amount in the Transaction's own currency. An even split is only a quick way of entering amounts; what gets stored is always the amounts themselves. When a split leaves odd minor units over, they are given out one each, first to the payers in order of descending Payment, and then to everyone else in split order.

**Unclaimed**:
The part of a Transaction's total that no Share accounts for. It belongs to nobody and nobody owes it. The payers absorb it in proportion to their Payments, until Members claim it by setting Shares of their own. Shares may never add up to more than the total.
_Avoid_: remainder, residue. Both suggest that something has gone wrong, and being Unclaimed is a perfectly normal state.

**Settlement**:
A Transaction with exactly one Payment and one Share, each for the whole amount. B hands A the cash, so B's Payment is the total and A's Share is the total. It is not a separate kind of record.

**Claim**:
A member setting their own Share on a Transaction that somebody else paid for. Every member is allowed to edit every field of every Transaction in their Group, and a Claim is simply the most common case of that.

**History**:
A record of every change made to a Transaction: who made it, when, and what changed. Every member of the Group can see it. The app lets everyone edit everything because the group trusts each other, and History is what makes that checkable afterwards.

**Net balance**:
For one Member, their Payments minus their Shares minus the Unclaimed they have absorbed, added up across the whole Group and expressed in the Base currency. Each amount is converted using its own Transaction's Rate date. The Net balances of a Group always add up to zero.

**Debt graph**:
The Group's Net balances turned into a small number of transfers, by greedy pairing: take the largest creditor and the largest debtor, settle as much as possible between them, and repeat; ties are broken by the order people joined. It is computed whenever it is asked for, it is a pure function of the ledger, and it is never edited by hand.
_Avoid_: "simplified debts", since there is no unsimplified view to contrast it with.

**Member**:
A person who belongs to a Group. Every Share and every Payment names a Member who is currently in the Group. Nobody may leave or be removed while their Net balance is non-zero, which is what stops the Debt graph from naming somebody who is no longer there.

**Phantom**:
A Member that has a name but no account: someone at the table who has not joined Spliff yet, or a stand-in used for testing. The Owner creates one, and it can pay and hold Shares like anybody else. Somebody who joins through an Invite Link may claim a Phantom, and its Payments, Shares and History then become theirs. A Phantom whose balance is zero can be removed, like any other Member.
_Avoid_: placeholder, fake user, and ghost. A ghost is exactly what the zero-balance rule exists to prevent.

**Owner**:
The Member who created the Group, or who has been given it since. Only the Owner can remove Members, create and revoke Invite Links, decide Join Requests, delete the Group and hand ownership to somebody else. The Owner cannot leave without handing it over first. Everything else — every Transaction, and the Group's name and Base currency — is equally open to every Member.
_Avoid_: admin, and creator, since the person who created the Group may not be the Owner any more.

**Invite Link**:
The same idea as in xy: a URL the Owner creates, which lets whoever holds it join the Group as a Member. It can have a limit on how many times it is used, it can expire, and it can hold the joiner for approval as a Join Request. It can also be revoked. It grants Membership and never a role.

**Photo**:
A picture that a Member attaches to a Transaction so the others can see it, usually the receipt or the bill. It is stored as the app re-encodes it, never as the phone sent it, so that nothing but the picture itself is kept. A Transaction may have several, and any Member may add or remove one.
_Avoid_: attachment, which is xy's word and covers any file, whereas a Photo is always an image; and receipt, since the picture may be of anything.
