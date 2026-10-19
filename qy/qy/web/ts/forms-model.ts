// The pure half of forms.ts: what a self-saving form sends and what its status
// line says about the answer.

export function formBody(entries: Iterable<[string, FormDataEntryValue]>): string {
  const params = new URLSearchParams();
  for (const [name, value] of entries) {
    if (typeof value === "string") params.append(name, value);
  }
  return params.toString();
}

// statusText reads the server's answer: the status it names when the save
// went through, or the words of a refusal, which the server wrote for the
// person who caused it.
export function statusText(ok: boolean, body: string, failed: string): string {
  if (!ok) return body.trim() || failed;
  try {
    const parsed = JSON.parse(body) as { status?: unknown };
    return typeof parsed.status === "string" ? parsed.status : failed;
  } catch {
    return failed;
  }
}
