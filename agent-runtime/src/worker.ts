import { controls, Evidence, RuntimeFailure, validateControl } from "./protocol.js";
import { SessionAttempt } from "./session.js";

// SDK/MCP diagnostics must never corrupt the stdout protocol.
console.log = console.info = (...args: unknown[]) => console.error(...args);
const iterator = controls(process.stdin)[Symbol.asyncIterator]();
let evidence: Evidence | undefined;
let attempt: SessionAttempt | undefined;
let finished = false;
try {
  const first = await iterator.next();
  const request = validateControl(first.value);
  evidence = new Evidence(request.output_dir, {
    request_id: request.request_id, run_id: request.run_id,
    stage_id: request.stage_id, attempt_id: request.attempt_id,
  });
  attempt = new SessionAttempt(request, evidence);
  let lastSeq = request.seq;
  const consume = (async () => {
    while (!finished) {
      const next = await iterator.next();
      if (finished) break;
      if (next.done) { await attempt!.cancel("parent input closed"); break; }
      const value = next.value as Record<string, unknown>;
      if (value.protocol_version !== 1 || value.type !== "cancel" ||
          value.seq !== ++lastSeq || Object.entries(evidence!.identity).some(([k, v]) => value[k] !== v))
        throw new RuntimeFailure("protocol", "invalid cancel control");
      await attempt!.cancel(String(value.reason ?? "parent cancelled"));
    }
  })().catch(async error => {
    if (!finished) {
      console.error(String(error));
      await attempt?.cancel(`control protocol failure: ${String(error)}`);
    }
  });
  await attempt.run();
  finished = true;
  process.stdin.destroy();
  await consume;
} catch (error) {
  console.error(error instanceof Error ? error.stack : String(error));
  process.exitCode = 1;
  // A missing terminal is deliberately a protocol failure on the parent side.
} finally {
  finished = true;
  process.stdin.destroy();
}
