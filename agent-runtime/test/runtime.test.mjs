import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { controls } from "../dist/protocol.js";
import { ToolPolicy } from "../dist/policy.js";
import { normalizeUsage, UsageLedger } from "../dist/usage.js";

test("byte framing retains large Unicode frames split inside code points", async () => {
  const expected = { text: "界".repeat(500000) };
  const data = Buffer.from(JSON.stringify(expected) + "\n");
  const stream = (async function* () {
    for (let i = 0; i < data.length; i += 65537) yield data.subarray(i, i + 65537);
  })();
  const decoded = [];
  for await (const value of controls(stream)) decoded.push(value);
  assert.deepEqual(decoded, [expected]);
});

test("invalid UTF-8 and incomplete frames are rejected", async () => {
  for (const frame of [Buffer.from('{"partial":true}'), Buffer.from([0xff, 10])]) {
    await assert.rejects(async () => {
      for await (const _value of controls((async function* () { yield frame; })())) { /* consume */ }
    });
  }
});

test("usage does not double count reasoning/cache and absent provider usage stays unknown", () => {
  assert.equal(normalizeUsage().status, "missing");
  assert.equal(normalizeUsage().total_tokens, null);
  assert.equal(normalizeUsage({ prompt_tokens: 10 }).status, "partial");
  const ledger = new UsageLedger("attempt");
  ledger.start();
  ledger.observe({ usage: {
    prompt_tokens: 40, completion_tokens: 20, total_tokens: 60,
    prompt_tokens_details: { cached_tokens: 10 },
    completion_tokens_details: { reasoning_tokens: 5 },
  } });
  const record = ledger.finish({ total: 60 }, "stop");
  assert.equal(record.total_tokens, 60);
  assert.equal(record.reasoning_tokens, 5);
  assert.equal(ledger.consumed, 60);
  assert.equal(ledger.finish(), undefined);
  ledger.start();
  assert.equal(ledger.finish({ total: 0 }, "aborted").status, "missing");
  assert.equal(ledger.incomplete, true);
});

test("policy denies symlink escapes, forbidden tools and readonly edits", () => {
  const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "defuzz-policy-")));
  try {
    fs.mkdirSync(path.join(root, "source"));
    fs.mkdirSync(path.join(root, "outside"));
    fs.mkdirSync(path.join(root, "evidence"));
    fs.symlinkSync(path.join(root, "outside"), path.join(root, "source", "escape"));
    const request = {
      cwd: path.join(root, "source"), output_dir: path.join(root, "evidence"),
      deny_read_paths: [path.join(root, "source", "private")], writable: true,
      agent: { agent_id: "checker-author", tools: ["read", "grep", "write"], deny_tools: [] },
    };
    const policy = new ToolPolicy(request);
    assert.match(policy.denyReason("write", { path: "escape/new.txt" }), /outside/);
    assert.match(policy.denyReason("read", { path: "private/secret" }), /denied/);
    assert.match(policy.denyReason("grep", {}), /narrower/);
    assert.match(policy.denyReason("bash", { command: "echo no" }), /not allowed/);
    assert.equal(policy.denyReason("write", { path: "checker.c" }), undefined);
    assert.match(new ToolPolicy({ ...request, writable: false })
      .denyReason("write", { path: "checker.c" }), /read-only/);
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});
