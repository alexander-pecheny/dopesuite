# TODO

## Rate-limiting / lockout on auth endpoints

No throttling exists on authentication today; bcrypt/scrypt cost is the only brake
on online guessing. Add per-IP + per-account rate limiting and lockout.

Endpoints to cover:
- **dope** (`dope/dope/server/auth.go`): `POST /api/auth/login` (telegram code),
  `POST /api/auth/login-password`, `POST /api/auth/login/start`,
  `POST /api/auth/register/start`.
- **xy** (`xy/internal/server/auth.go`): `POST /api/auth/login`,
  `POST /api/auth/login-password`, `POST /api/auth/login/start`.

Notes:
- Both apps are single-binary + SQLite; a small in-memory sliding-window limiter
  (per-IP and per-username) is enough — no external store needed.
- Lock an account for a cooldown after N consecutive password failures; count
  telegram-code attempts too (the code is short-lived but brute-forceable within
  its 60s TTL).
- Return a generic error + 429 on limit; don't leak whether the account exists.
- Shared limiter probably belongs in `dopecore` so both apps use one implementation.
