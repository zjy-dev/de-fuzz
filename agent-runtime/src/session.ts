import fs from "node:fs";
import path from "node:path";
import {
  createAgentSession, createMcpExtension, DefaultResourceLoader, ModelRuntime,
  SessionManager, SettingsManager, type AgentSession, type ExtensionFactory, type McpServerConfig,
} from "@earendil-works/pi-coding-agent";
import { Evidence, RuntimeFailure, sha256, type RunControl } from "./protocol.js";
import { ToolPolicy, contains, realPath } from "./policy.js";
import { UsageLedger } from "./usage.js";
import { transportFactory } from "./mcp.js";

async function bounded<T>(promise: Promise<T>, ms: number, category: string): Promise<T> {
  let timer: NodeJS.Timeout | undefined;
  try {
    return await Promise.race([promise, new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new RuntimeFailure(category, `${category} deadline exceeded`)), ms);
    })]);
  } finally { clearTimeout(timer); }
}

export class SessionAttempt {
  session?: AgentSession;
  private cancelled = false;
  private failure?: RuntimeFailure;
  private settled = false;
  private ledger: UsageLedger;
  private policy: ToolPolicy;
  private toolStarts = new Map<string, number>();
  private toolTimers = new Map<string, NodeJS.Timeout>();
  private graphCalls = 0;
  private graphDataCalls = 0;
  constructor(private request: RunControl, private evidence: Evidence) {
    this.ledger = new UsageLedger(request.attempt_id);
    this.policy = new ToolPolicy(request);
  }
  async cancel(reason = "cancelled"): Promise<void> {
    this.cancelled = true;
    this.failure ??= new RuntimeFailure("cancelled", reason);
    await this.session?.abort();
  }
  private safe(action: () => void): void {
    try { action(); }
    catch (error) {
      this.failure ??= new RuntimeFailure("evidence", String(error));
      void this.session?.abort();
    }
  }
  private recordUsage(normalized?: unknown, reason?: string): void {
    const usage = this.ledger.finish(normalized, reason);
    if (!usage) return;
    const record = {
      ...this.evidence.identity, session_id: this.evidence.sessionId,
      model_provider: this.request.config.model_provider,
      model: this.request.config.model_id, ...usage,
    };
    this.evidence.append("usage.jsonl", record);
    this.evidence.emit("usage", record);
  }
  private observer: ExtensionFactory = (pi) => {
    pi.on("agent_settled", () => { this.settled = true; });
    pi.on("before_agent_start", event => this.safe(() => {
      this.evidence.write("system-prompt.txt", event.systemPrompt);
      this.evidence.json("effective-session.json", {
        system_prompt_sha256: sha256(event.systemPrompt),
        tools: this.session?.agent.state.tools.map(t => ({
          name: t.name, description: t.description, parameters: t.parameters,
        })),
      });
    }));
    pi.on("before_provider_request", event => this.safe(() => {
      this.evidence.append("provider-events.jsonl", {
        ...this.evidence.identity, session_id: this.evidence.sessionId,
        model_call_id: this.ledger.pending?.id, timestamp: new Date().toISOString(),
        type: event.type, payload: event.payload,
      });
    }));
    pi.on("after_provider_response", event => this.safe(() => {
      this.evidence.append("provider-events.jsonl", {
        ...this.evidence.identity, session_id: this.evidence.sessionId,
        model_call_id: this.ledger.pending?.id, timestamp: new Date().toISOString(),
        type: event.type, status: event.status,
        headers: Object.fromEntries(Object.entries(event.headers).filter(([key]) =>
          /^(x-request-id|request-id|retry-after|content-type|date)$/i.test(key))),
      });
    }));
    pi.on("provider_stream_event", event => this.safe(() => {
      this.ledger.observe(event.data);
      this.evidence.append("provider-events.jsonl", {
        ...this.evidence.identity, session_id: this.evidence.sessionId,
        model_call_id: this.ledger.pending?.id, timestamp: new Date().toISOString(), ...event,
      });
    }));
    pi.on("tool_call", event => {
      try {
        const reason = this.failure?.message ?? this.policy.denyReason(event.toolName, event.input);
        if (reason) {
          this.evidence.emit("event", { type: "tool_denied", tool_call_id: event.toolCallId,
            tool: event.toolName, reason });
          return { block: true, reason };
        }
      } catch (error) {
        this.failure ??= new RuntimeFailure("evidence", String(error));
        return { block: true, reason: this.failure.message };
      }
    });
  };
  async run(): Promise<void> {
    const { request: r, evidence: e } = this;
    const started = performance.now();
    const runtimeDir = path.join(r.output_dir, ".runtime");
    fs.mkdirSync(runtimeDir, { recursive: true });
    for (const name of ["events.jsonl", "provider-events.jsonl", "usage.jsonl", "mcp.log"])
      e.write(name, "");
    for (const name of ["request.json", "resolved-agent.json", "resolved-config.json", "output-schema.json",
      "tool-binaries.jsonl", "callgraph-binding.json"])
      e.include(name);
    let finalText = "";
    let heartbeat: NodeJS.Timeout | undefined;
    try {
      e.emit("ready", { backend_identity: "pi-sdk", sdk_version: "1.1.0", node: process.version });
      const modelRuntime = await ModelRuntime.create({
        authPath: path.join(runtimeDir, "auth.json"),
        modelsPath: r.config.models_path, modelsStorePath: path.join(runtimeDir, "models-store"),
        allowModelNetwork: false, refreshOnCreate: false,
      });
      const model = modelRuntime.getModel(r.config.model_provider, r.config.model_id);
      if (!model) throw new RuntimeFailure("config", "pinned model is unavailable");
      if (model.reasoning && r.config.temperature_policy === "apply" && r.agent.temperature !== undefined)
        throw new RuntimeFailure("config", "reasoning model requires an explicit temperature omission policy");
      if (!["openai-responses", "openai-completions"].includes(model.api))
        throw new RuntimeFailure("config", `usage adapter not verified for ${model.api}`);
      const settings = SettingsManager.inMemory({
        compaction: { enabled: false }, cacheWarming: "off",
        retry: { enabled: false, maxRetries: 0,
          provider: { maxRetries: 0, timeoutMs: r.config.request_timeout_ms } },
        enableAnalytics: false, enableInstallTelemetry: false,
      });
      const extensions: ExtensionFactory[] = [this.observer];
      if (Object.keys(r.config.mcp).length) {
        const mcp = createMcpExtension({
          createTransport: transportFactory(e, error => {
            this.failure ??= new RuntimeFailure("evidence", String(error));
            void this.session?.abort();
          }),
          logPath: path.join(r.output_dir, "mcp.log"),
          startupWaitMs: r.config.mcp_startup_timeout_ms,
          loadConfig: () => ({
            autoEnableCodemode: false, errors: [],
            servers: Object.entries(r.config.mcp).map(([name, config]) => ({
              name, source: path.join(r.output_dir, "resolved-config.json"), scope: "extension",
              // Python strictly validates transport/config; SDK checks server schemas.
              config: { ...Object.fromEntries(Object.entries(config).filter(([key]) =>
                  !["database_path", "provenance_path"].includes(key))), exposure: "hidden",
                timeout: r.config.mcp_call_timeout_ms / 1000,
                toolExposure: Object.fromEntries([...this.policy.allowed]
                  .filter(t => t.startsWith(`mcp__${name}__`))
                  .map(t => [t.slice(`mcp__${name}__`.length), "direct"])),
              } as McpServerConfig,
            })),
          }),
          openUrl: async () => { throw new RuntimeFailure("auth", "interactive MCP auth is disabled"); },
        });
        extensions.push(mcp);
      }
      // Gate the actual stream entry. Extension-hook exceptions alone do not block Pi.
      const stream = modelRuntime.streamSimple.bind(modelRuntime);
      modelRuntime.streamSimple = (selected, context, options) => {
        try {
        if (this.failure) throw this.failure;
        if (e.failure) throw new RuntimeFailure("evidence", e.failure.message);
        if (this.cancelled) throw new RuntimeFailure("cancelled", "attempt cancelled");
        if (selected.provider !== model.provider || selected.id !== model.id)
          throw new RuntimeFailure("config", "model changed after resolution");
        if (this.ledger.incomplete && r.config.strict_usage)
          throw new RuntimeFailure("evidence", "provider usage is incomplete; next call blocked");
        if (this.ledger.calls >= r.config.max_model_calls)
          throw new RuntimeFailure("budget", "maximum model requests reached");
        // Conservative reservation, recorded separately from measured provider usage.
        const estimatedInput = Math.ceil(Buffer.byteLength(JSON.stringify(context)) / 3);
        const remaining = r.config.token_budget === null ? Infinity
          : r.config.token_budget - this.ledger.consumed;
        const maxTokens = Math.min(r.config.max_output_tokens, model.maxTokens,
          Math.floor(remaining - estimatedInput));
        if (maxTokens < 1) throw new RuntimeFailure("budget", "insufficient remaining token budget");
        const id = this.ledger.start();
        e.emit("event", { type: "model_request", model_call_id: id,
          estimated_input_tokens: estimatedInput, max_output_tokens: maxTokens });
        try {
          return stream(selected, context, { ...options, maxTokens,
            temperature: r.config.temperature_policy === "apply" ? r.agent.temperature : undefined,
            maxRetries: 0, timeoutMs: r.config.request_timeout_ms, transport: "sse",
            onPayload: async (payload, selectedModel) => {
              const effective = await options?.onPayload?.(payload, selectedModel);
              if (this.failure) throw this.failure;
              if (e.failure) throw new RuntimeFailure("evidence", e.failure.message);
              return effective;
            },
          });
        } catch (error) {
          this.safe(() => this.recordUsage(undefined, "error"));
          throw error;
        }
        } catch (error) {
          this.failure ??= error instanceof RuntimeFailure ? error : new RuntimeFailure("provider", String(error));
          throw error;
        }
      };
      const loader = new DefaultResourceLoader({
        cwd: r.cwd, agentDir: runtimeDir, settingsManager: settings,
        noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true,
        noContextFiles: true, extensionFactories: extensions, systemPrompt: r.agent.system_prompt,
      });
      await loader.reload();
      const created = await createAgentSession({
        cwd: r.cwd, agentDir: runtimeDir, modelRuntime, model,
        thinkingLevel: r.config.thinking_level, settingsManager: settings,
        sessionManager: SessionManager.create(r.cwd, path.join(r.output_dir, "sessions")),
        resourceLoader: loader, tools: [...this.policy.allowed],
      });
      this.session = created.session;
      e.sessionId = this.session.sessionId;
      e.emit("session_started", { agent_id: r.agent.agent_id, model_provider: model.provider,
        model_id: model.id, api: model.api, thinking_level: this.session.thinkingLevel,
        temperature: r.config.temperature_policy === "apply" ? r.agent.temperature ?? null : null,
        temperature_policy: r.config.temperature_policy,
      });
      this.session.subscribe(event => this.safe(() => {
        e.append("events.jsonl", { ...e.identity, session_id: e.sessionId,
          model_call_id: this.ledger.pending?.id, timestamp: new Date().toISOString(), ...event });
        // Deltas are persisted once; Python receives bounded references for large messages.
        if (event.type !== "message_update") e.emit("event", event);
        if (event.type === "message_end" && event.message.role === "assistant") {
          if (event.message.stopReason === "error" || event.message.stopReason === "aborted")
            e.append("provider-events.jsonl", {
              ...e.identity, session_id: e.sessionId, model_call_id: this.ledger.pending?.id,
              timestamp: new Date().toISOString(), type: "provider_error",
              source: "pi-assistant-message", error: event.message.errorMessage,
              stop_reason: event.message.stopReason,
            });
          this.recordUsage(event.message.usage, event.message.stopReason);
          if (event.message.stopReason === "error" || event.message.stopReason === "aborted")
            this.failure ??= new RuntimeFailure(this.cancelled ? "cancelled" : "provider",
              event.message.errorMessage ?? event.message.stopReason);
        }
        if (event.type === "tool_execution_start") {
          this.toolStarts.set(event.toolCallId, performance.now());
          this.toolTimers.set(event.toolCallId, setTimeout(() => {
            this.failure ??= new RuntimeFailure("timeout", `tool deadline exceeded: ${event.toolCallId}`);
            void this.session?.abort();
          }, r.config.mcp_call_timeout_ms));
        }
        if (event.type === "tool_execution_end") {
          clearTimeout(this.toolTimers.get(event.toolCallId));
          this.toolTimers.delete(event.toolCallId);
          const result = event.result as { details?: { fullOutputPath?: string } };
          const full = result?.details?.fullOutputPath;
          let original;
          if (full) {
            // SDK originals must be below this attempt's isolated TMPDIR.
            const file = realPath(full);
            if (!contains(realPath(path.join(runtimeDir, "tmp")), file))
              throw new RuntimeFailure("artifact", "MCP original outside attempt temporary directory");
            original = e.artifact(fs.readFileSync(file), path.extname(file));
            this.policy.allowOriginal(file);
          }
          e.emit("event", { type: "tool_evidence", tool_call_id: event.toolCallId,
            tool: event.toolName, success: !event.isError, original,
            latency_ms: performance.now() - (this.toolStarts.get(event.toolCallId) ?? performance.now()),
          });
          if (!event.isError && event.toolName.startsWith("mcp__defuzz_callgraph__")) {
            this.graphCalls++;
            if (!event.toolName.endsWith("__list_targets")) this.graphDataCalls++;
          }
        }
      }));
      await this.session.bindExtensions({});
      const readinessDeadline = performance.now() + r.config.mcp_startup_timeout_ms;
      while (r.agent.required_tools.some(t => !this.session!.getActiveToolNames().includes(t))) {
        if (this.cancelled) throw new RuntimeFailure("cancelled", "cancelled during MCP startup");
        if (performance.now() >= readinessDeadline)
          throw new RuntimeFailure("mcp", `required tools unavailable: ${r.agent.required_tools
            .filter(t => !this.session!.getActiveToolNames().includes(t)).join(", ")}`);
        await new Promise(resolve => setTimeout(resolve, 25));
      }
      const active = this.session.getActiveToolNames();
      if (active.some(t => !this.policy.allowed.has(t)))
        throw new RuntimeFailure("config", "SDK exposed an unapproved tool");
      e.json("tools.json", this.session.getAllTools().filter(t => active.includes(t.name)));
      heartbeat = setInterval(() => this.safe(() => e.emit("heartbeat", {
        model_calls: this.ledger.calls, elapsed_ms: performance.now() - started,
      })), 2000);
      if (this.cancelled) throw new RuntimeFailure("cancelled", "cancelled before prompt");
      await this.session.prompt(r.prompt);
      if (!this.settled || !this.session.isIdle)
        throw new RuntimeFailure("provider", "Pi prompt returned without agent_settled");
      if (this.failure) throw this.failure;
      const messages = this.session.agent.state.messages;
      const last = [...messages].reverse().find(m => m.role === "assistant");
      if (!last || last.role !== "assistant" || !["stop"].includes(last.stopReason))
        throw new RuntimeFailure("provider", `incomplete final message: ${last?.role === "assistant" ? last.stopReason : "missing"}`);
      finalText = last.content.filter(c => c.type === "text").map(c => c.text).join("\n");
      if (!finalText.trim()) throw new RuntimeFailure("schema", "empty final message");
      if (this.ledger.incomplete && r.config.strict_usage)
        throw new RuntimeFailure("evidence", "provider usage is incomplete");
    } catch (error) {
      this.failure ??= error instanceof RuntimeFailure ? error : new RuntimeFailure("provider", String(error));
    } finally {
      clearInterval(heartbeat);
      for (const timer of this.toolTimers.values()) clearTimeout(timer);
      this.safe(() => this.recordUsage(undefined, this.cancelled ? "aborted" : "error"));
      if (this.session) {
        try {
          await bounded(this.session.extensionRunner.emit({ type: "session_shutdown", reason: "quit" }),
            r.config.cleanup_timeout_ms, "mcp");
        } catch (error) { this.failure ??= new RuntimeFailure("mcp", String(error)); }
        this.session.dispose();
        this.safe(() => {
          if (this.session?.sessionFile)
            e.write("session.jsonl", fs.readFileSync(this.session.sessionFile));
        });
      }
    }
    this.safe(() => e.write("final.raw.txt", finalText));
    const outcome = {
      runtime_ok: !this.failure, evidence_ok: !e.failure && !this.ledger.incomplete && this.failure?.category !== "evidence",
      artifact_ok: this.failure?.category !== "artifact", schema_ok: null,
      error_category: this.failure?.category, error: this.failure?.message,
      final_text: finalText, model_calls: this.ledger.calls, total_tokens: this.ledger.consumed,
      usage_complete: !this.ledger.incomplete,
      callgraph_queries: this.graphCalls, callgraph_data_queries: this.graphDataCalls,
      latency_ms: performance.now() - started,
    };
    e.json("outcome.json", outcome);
    e.include("mcp.log");
    const manifest = e.manifest();
    await e.flush();
    e.emit(this.failure ? "error" : "result", { ...outcome, manifest });
    await e.flush();
  }
}
