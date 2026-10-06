# A removed Member keeps their row

Until 6 Oct 2026, removing a Member, or leaving, deleted their
`group_members` row. That row was the only place their name was kept:
Payments, Shares and History snapshots store a member id and nothing else. So
a deleted Transaction that named them, or a History entry, could not say who
they were. The bill form showed «Has left» and History showed «Somebody».

## Decision

- Removing a Member sets `group_members.left_at` (migration 3) and keeps the
  row. CONTEXT.md calls such a person a Former Member.
- Every list of the Group's people reads current Members only: `Members`,
  `MemberByID`, `Phantoms`, `IsMember` and `GroupsOf`. So the two invariants
  in ADR-0002 hold as before: a Share or Payment names a current Member, and
  the balances and the Debt graph never include a Former Member.
- `store.AllMembers` includes Former Members. The server resolves names
  through it, and sends Former Members to the page as `former`. The page shows
  them as «name (left)».
- A Former Member who joins again through an Invite Link gets the same row
  back: `left_at` is cleared and `joined_at` becomes the day they came back.
  `unique(group_id, user_id)` would refuse a second row anyway. Because the id
  is the same, a deleted Transaction that names them can be restored again.
- A Former Member cannot claim a Phantom, because they already have a row in
  the Group. A removed Phantom cannot be claimed.

## Consequences

- Rows deleted before migration 3 are gone, and so are their names. Their
  entries still show «Has left» in the bill form and «Somebody» in History.
- Deleting the Group still deletes every row, Former Members' included.
