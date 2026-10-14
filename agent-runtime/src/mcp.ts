import { StdioTransport, StreamableHttpTransport } from "@earendil-works/pi-mcp";
import type { McpTransportFactory } from "@earendil-works/pi-coding-agent";
import { Evidence, RuntimeFailure } from "./protocol.js";

function expand(value: string): string {
  if (value.startsWith("!")) throw new RuntimeFailure("config", "MCP shell expansion is disabled");
  return value.replace(/\$\{([A-Za-z_][A-Za-z0-9_]*)\}/g, (_, name: string) => {
    const value = process.env[name];
    if (value === undefined) throw new RuntimeFailure("config", `missing MCP variable ${name}`);
    return value;
  });
}

export function transportFactory(evidence: Evidence, fail: (error: Error) => void): McpTransportFactory {
  return (entry, cwd, authProvider) => {
    const { config, name } = entry;
    if ("url" in config) {
      return new StreamableHttpTransport({
        url: config.url,
        headers: Object.fromEntries(Object.entries(config.headers ?? {}).map(([k, v]) => [k, expand(v)])),
        authProvider, reconnect: { maxRetries: 0 },
      });
    }
    const runtimeEnv = Object.fromEntries(Object.entries(process.env).filter(([key, value]) =>
      value !== undefined && /^(PATH|HOME|SHELL|TMPDIR|TMP|TEMP|LANG|LC_.*|XDG_.*|UV_CACHE_DIR|SSL_CERT_FILE|SSL_CERT_DIR)$/.test(key)
    )) as Record<string, string>;
    class ObservedStdio extends StdioTransport {
      override async start(): Promise<void> {
        await super.start();
        try {
          const event = { type: "mcp_process_started", server: name, pid: this.pid,
            timestamp: new Date().toISOString() };
          evidence.append("mcp-processes.jsonl", event);
          evidence.emit("event", event);
        } catch (error) { fail(error as Error); throw error; }
      }
    }
    return new ObservedStdio({
      command: config.command, args: config.args, cwd: config.cwd ?? cwd,
      inheritEnv: false,
      env: { ...runtimeEnv, ...Object.fromEntries(
        Object.entries(config.env ?? {}).map(([k, v]) => [k, expand(v)])) },
      stderr: "pipe", maxMessageBytes: 16 * 1024 * 1024,
      onStderr: chunk => {
        try {
          evidence.append("mcp.log", { server: name, type: "stderr",
            timestamp: new Date().toISOString(), text: chunk });
        } catch (error) { fail(error as Error); }
      },
      closeTimeoutMs: 1000,
    });
  };
}
