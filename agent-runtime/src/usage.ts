export type UsageStatus = "reported" | "missing" | "partial";
type ObjectValue = Record<string, any>;
const count = (v: unknown): number | null =>
  typeof v === "number" && Number.isSafeInteger(v) && v >= 0 ? v : null;

/** Supported OpenAI adapters: cached input/reasoning are subsets, not extra tokens. */
export function normalizeUsage(raw?: ObjectValue): ObjectValue {
  const input = count(raw?.input_tokens ?? raw?.prompt_tokens);
  const output = count(raw?.output_tokens ?? raw?.completion_tokens);
  const providerTotal = count(raw?.total_tokens);
  const localTotal = input === null || output === null ? null : input + output;
  const status: UsageStatus = !raw ? "missing"
    : input === null || output === null ? "partial" : "reported";
  return {
    status, source: raw ? "provider_stream_event" : "unavailable",
    input_tokens: input, output_tokens: output,
    total_tokens: providerTotal ?? localTotal,
    provider_total_tokens: providerTotal, computed_total_tokens: localTotal,
    total_difference: providerTotal === null || localTotal === null ? null : providerTotal - localTotal,
    cached_input_tokens: count(raw?.input_tokens_details?.cached_tokens ?? raw?.prompt_tokens_details?.cached_tokens),
    cache_creation_input_tokens: count(raw?.cache_creation_input_tokens),
    reasoning_tokens: count(raw?.output_tokens_details?.reasoning_tokens ?? raw?.completion_tokens_details?.reasoning_tokens),
    estimated_cost: null,
    raw: raw ?? null,
  };
}

export class UsageLedger {
  calls = 0;
  consumed = 0;
  incomplete = false;
  pending?: { id: string; started: number; raw?: ObjectValue };
  constructor(private attemptId: string) {}
  start(): string {
    if (this.pending) throw new Error("previous model request has not been accounted");
    const id = `${this.attemptId}:model:${++this.calls}`;
    this.pending = { id, started: performance.now() };
    return id;
  }
  observe(data: unknown): void {
    if (!this.pending || !data || typeof data !== "object") return;
    const event = data as ObjectValue;
    // Only protocol locations, never arbitrary tool content that mentions "usage".
    const usage = event.response?.usage ?? event.usage;
    if (usage && typeof usage === "object") this.pending.raw = usage;
  }
  finish(normalized?: unknown, stopReason?: string): ObjectValue | undefined {
    if (!this.pending) return;
    const usage = normalizeUsage(this.pending.raw);
    const record = {
      model_call_id: this.pending.id, ...usage, pi_usage: normalized ?? null,
      latency_ms: performance.now() - this.pending.started,
      stop_reason: stopReason ?? "interrupted",
    };
    if (usage.status !== "reported") this.incomplete = true;
    if (usage.total_tokens !== null) this.consumed += usage.total_tokens;
    this.pending = undefined;
    return record;
  }
}
