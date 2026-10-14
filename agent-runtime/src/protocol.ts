import fs from "node:fs";
import path from "node:path";
import { createHash } from "node:crypto";
import { once } from "node:events";

export const PROTOCOL_VERSION = 1;
export const INLINE_LIMIT = 48 * 1024;
export const MAX_CONTROL_BYTES = 16 * 1024 * 1024;
export const sha256 = (data: string | Buffer): string =>
  createHash("sha256").update(data).digest("hex");

export class RuntimeFailure extends Error {
  constructor(public category: string, message: string) { super(message); }
}

export interface Identity {
  request_id: string;
  run_id: string;
  stage_id: string;
  attempt_id: string;
}
export interface RunControl extends Identity {
  protocol_version: 1;
  type: "run";
  seq: number;
  timestamp: string;
  prompt: string;
  cwd: string;
  output_dir: string;
  writable: boolean;
  deny_read_paths: string[];
  agent: {
    agent_id: string;
    system_prompt: string;
    tools: string[];
    deny_tools: string[];
    required_tools: string[];
    temperature?: number;
  };
  config: {
    models_path: string;
    model_provider: string;
    model_id: string;
    thinking_level: "off" | "minimal" | "low" | "medium" | "high" | "xhigh";
    temperature_policy: "apply" | "omit";
    max_model_calls: number;
    max_output_tokens: number;
    token_budget: number | null;
    strict_usage: boolean;
    request_timeout_ms: number;
    mcp_startup_timeout_ms: number;
    mcp_call_timeout_ms: number;
    cleanup_timeout_ms: number;
    mcp: Record<string, Record<string, unknown>>;
  };
}

export function validateControl(value: unknown): RunControl {
  const c = value as RunControl;
  if (!c || c.protocol_version !== PROTOCOL_VERSION || c.type !== "run" || c.seq !== 1)
    throw new RuntimeFailure("protocol", "expected JSONL v1 run control");
  for (const key of ["request_id", "run_id", "stage_id", "attempt_id"] as const)
    if (typeof c[key] !== "string" || !c[key])
      throw new RuntimeFailure("protocol", `missing ${key}`);
  if (typeof c.prompt !== "string" || !c.agent || !c.config ||
      !path.isAbsolute(c.cwd) || !path.isAbsolute(c.output_dir))
    throw new RuntimeFailure("config", "invalid worker request");
  for (const key of ["max_model_calls", "max_output_tokens", "request_timeout_ms",
    "mcp_startup_timeout_ms", "mcp_call_timeout_ms", "cleanup_timeout_ms"] as const)
    if (!Number.isSafeInteger(c.config[key]) || c.config[key] <= 0)
      throw new RuntimeFailure("config", `invalid ${key}`);
  if (c.config.token_budget !== null &&
      (!Number.isSafeInteger(c.config.token_budget) || c.config.token_budget <= 0))
    throw new RuntimeFailure("config", "token budget exhausted or invalid");
  return c;
}

/** Synchronous durable evidence writes; asynchronous, ordered stdout backpressure. */
export class Evidence {
  seq = 0;
  sessionId?: string;
  failure?: Error;
  private queue = Promise.resolve();
  private pendingBytes = 0;
  private terminal = false;
  private paths = new Set<string>();
  constructor(public root: string, public identity: Identity) {
    fs.mkdirSync(path.join(root, "artifacts"), { recursive: true });
  }
  write(name: string, data: string | Buffer): void {
    fs.mkdirSync(path.dirname(path.join(this.root, name)), { recursive: true });
    fs.writeFileSync(path.join(this.root, name), data);
    this.paths.add(name);
  }
  json(name: string, value: unknown): void {
    this.write(name, JSON.stringify(value, null, 2) + "\n");
  }
  append(name: string, value: unknown): void {
    fs.appendFileSync(path.join(this.root, name), JSON.stringify(value) + "\n");
    this.paths.add(name);
  }
  artifact(data: string | Buffer, suffix = ".json"): { path: string; sha256: string; bytes: number } {
    const hash = sha256(data);
    const relative = `artifacts/${hash}${suffix}`;
    this.write(relative, data);
    return { path: relative, sha256: hash, bytes: Buffer.byteLength(data) };
  }
  emit(type: string, data: unknown = {}): void {
    if (this.terminal) throw new RuntimeFailure("protocol", "event after terminal");
    if (type === "result" || type === "error") this.terminal = true;
    const serialized = JSON.stringify(data);
    const body = Buffer.byteLength(serialized) > INLINE_LIMIT
      ? { artifact_ref: this.artifact(serialized) } : { data };
    const frame = JSON.stringify({
      protocol_version: PROTOCOL_VERSION, ...this.identity,
      session_id: this.sessionId, seq: ++this.seq, timestamp: new Date().toISOString(),
      type, ...body,
    }) + "\n";
    this.pendingBytes += Buffer.byteLength(frame);
    if (this.pendingBytes > 16 * 1024 * 1024)
      throw new RuntimeFailure("evidence", "stdout backpressure queue limit exceeded");
    this.queue = this.queue.then(async () => {
      if (!process.stdout.write(frame)) await once(process.stdout, "drain");
      this.pendingBytes -= Buffer.byteLength(frame);
    }).catch((error: Error) => { this.failure = error; });
  }
  /** Register SDK-owned files only after lifecycle shutdown has finished. */
  include(name: string): void {
    if (fs.existsSync(path.join(this.root, name))) this.paths.add(name);
  }
  manifest(): { path: string; sha256: string; bytes: number } {
    const files = [...this.paths].sort().map((name) => {
      const data = fs.readFileSync(path.join(this.root, name));
      return { path: name, sha256: sha256(data), bytes: data.length };
    });
    const data = JSON.stringify({ schema_version: 1, files }, null, 2) + "\n";
    this.write("artifacts/manifest.json", data);
    return { path: "artifacts/manifest.json", sha256: sha256(data), bytes: Buffer.byteLength(data) };
  }
  async flush(): Promise<void> {
    await this.queue;
    if (this.failure) throw new RuntimeFailure("evidence", this.failure.message);
    for (const name of this.paths) {
      const fd = fs.openSync(path.join(this.root, name), "r");
      try { fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
    }
  }
}

/** Byte framing handles split UTF-8, and never relies on readline's line limit. */
export async function* controls(stream: AsyncIterable<Buffer>): AsyncGenerator<unknown> {
  let pending = Buffer.alloc(0);
  for await (const chunk of stream) {
    pending = Buffer.concat([pending, chunk]);
    let newline: number;
    while ((newline = pending.indexOf(10)) !== -1) {
      if (newline > MAX_CONTROL_BYTES) throw new RuntimeFailure("protocol", "control too large");
      const line = pending.subarray(0, newline);
      pending = pending.subarray(newline + 1);
      yield JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(line));
    }
    if (pending.length > MAX_CONTROL_BYTES) throw new RuntimeFailure("protocol", "control too large");
  }
  if (pending.length) throw new RuntimeFailure("protocol", "unterminated control");
}
