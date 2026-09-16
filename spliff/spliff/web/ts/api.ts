// The one way a Spliff page talks to the server. Every refusal the server
// writes was written for the person who caused it (root docs/adr/0006), so the
// body is the message — the page shows it and invents nothing of its own.

export interface EntryDTO {
  member_id: number;
  name: string;
  minor: number;
  amount: string;
}

// One row of /api/currencies: the code a form submits, and the English name
// that says what it is. The set is the newest Rate table's, so it is only ever
// currencies a Transaction could actually be converted out of.
export interface CurrencyDTO {
  code: string;
  name: string;
}

export interface PhotoDTO {
  id: number;
  url: string;
  width: number;
  height: number;
  uploader: string;
}

export interface TransactionDTO {
  id: number;
  group_id: number;
  description: string;
  day: string;
  currency: string;
  total_minor: number;
  total: string;
  unclaimed_minor: number;
  unclaimed: string;
  payments: EntryDTO[];
  shares: EntryDTO[];
  photos: PhotoDTO[];
  rate_date: string;
  in_base: string;
  deleted: boolean;
  created_by: string;
  updated_at: string;
}

/**
 * One place in a Group. `id` is the member ROW — what every Payment and Share
 * names, what the editor keys on and what the kick route addresses. `user_id`
 * is the account behind it and is 0 for a Phantom, who has none.
 */
export interface MemberDTO {
  id: number;
  user_id: number;
  is_phantom: boolean;
  name: string;
  joined_at: string;
  is_owner: boolean;
  balance_minor: number;
  balance: string;
}

export interface TransferDTO {
  from_id: number;
  from_name: string;
  to_id: number;
  to_name: string;
  minor: number;
  amount: string;
}

export interface HistoryDTO {
  id: number;
  transaction_id: number;
  actor: string;
  at: string;
  kind: string;
  before: string;
  after: string;
  description: string;
}

export interface GroupDTO {
  id: number;
  name: string;
  base_currency: string;
  is_owner: boolean;
  /** The caller's own member row. */
  me: number;
  members: MemberDTO[];
  transfers: TransferDTO[];
  live: TransactionDTO[];
  deleted: TransactionDTO[];
  history: HistoryDTO[];
  no_rates: boolean;
}

export interface GroupSummaryDTO {
  id: number;
  name: string;
  base_currency: string;
  is_owner: boolean;
  balance_minor: number;
  balance: string;
}

export interface InvitePersonDTO {
  user_id: number;
  name: string;
  at: string;
}

export interface InviteDTO {
  id: number;
  code: string;
  url: string;
  label: string;
  created_at: string;
  expires_at?: string;
  max_uses: number | null;
  used: number;
  left: number | null;
  requires_approval: boolean;
  state: string;
  joined: InvitePersonDTO[];
  pending: InvitePersonDTO[];
}

export interface PhantomDTO {
  id: number;
  name: string;
}

export interface InvitePeekDTO {
  group_id: number;
  group_name: string;
  state: string;
  requires_approval: boolean;
  phantoms: PhantomDTO[];
}

export interface TransactionViewDTO {
  transaction: TransactionDTO;
  group: { id: number; name: string; base_currency: string; is_owner: boolean; me: number };

  members: MemberDTO[];
  history: HistoryDTO[];
}

// ApiError is a refusal the server WROTE — its own words, for the person who
// caused them. A request that never arrived rejects with whatever fetch threw
// instead, which is how a poll tells "stop, you cannot" from "try again".
export class ApiError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

export async function request<T>(method: string, url: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method };
  if (body !== undefined) {
    init.headers = { "Content-Type": "application/json" };
    init.body = JSON.stringify(body);
  }
  const response = await fetch(url, init);
  if (!response.ok) {
    const text = (await response.text()).trim();
    throw new ApiError(text || `HTTP ${response.status}`, response.status);
  }
  if (response.status === 204) return null as T;
  const text = await response.text();
  return (text ? JSON.parse(text) : null) as T;
}

export function get<T>(url: string): Promise<T> {
  return request<T>("GET", url);
}

export function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
