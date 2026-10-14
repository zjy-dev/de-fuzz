import fs from "node:fs";
import path from "node:path";
import type { RunControl } from "./protocol.js";

/** Resolve existing ancestors too, so a new file below a symlink cannot escape. */
export function realPath(value: string): string {
  const absolute = path.resolve(value);
  try { return fs.realpathSync(absolute); }
  catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    const parent = path.dirname(absolute);
    if (parent === absolute) throw error;
    return path.join(realPath(parent), path.basename(absolute));
  }
}
export const contains = (root: string, target: string): boolean =>
  target === root || target.startsWith(root + path.sep);

export class ToolPolicy {
  readonly allowed: Set<string>;
  private root: string;
  private denied: string[];
  private originals = new Set<string>();
  constructor(private request: RunControl) {
    this.root = realPath(request.cwd);
    // Python already canonicalized deny roots; the OS sandbox may forbid even lstat here.
    this.denied = [...request.deny_read_paths, path.join(request.output_dir, ".runtime")].map(p => path.resolve(p));
    this.allowed = new Set(request.agent.tools.filter(t => !request.agent.deny_tools.includes(t)));
  }
  allowOriginal(file: string): void { this.originals.add(realPath(file)); }
  denyReason(tool: string, input: Record<string, unknown>): string | undefined {
    if (!this.allowed.has(tool)) return `tool is not allowed: ${tool}`;
    if (tool.startsWith("mcp__")) return;
    if (!["read", "grep", "find", "ls", "write", "edit", "bash"].includes(tool))
      return `unsupported tool: ${tool}`;
    const writing = tool === "write" || tool === "edit";
    if (writing && !this.request.writable)
      return "agent is read-only";
    if (tool === "bash") {
      if (!this.request.writable) return "agent is read-only; bash execution denied";
      return;
    }
    if (input.path !== undefined && typeof input.path !== "string") return "invalid path";
    const raw = input.path as string | undefined;
    const lexical = path.resolve(this.root, raw ?? ".");
    if (!writing && tool === "read" && this.originals.has(lexical)) return;
    if (this.denied.some(d => contains(d, lexical))) return `denied path: ${lexical}`;
    const target = realPath(lexical);
    if (this.denied.some(d => contains(d, target))) return `denied path: ${target}`;
    // Recursive queries over an ancestor of a deny path must not expose its contents.
    if (["grep", "find"].includes(tool) && this.denied.some(d => contains(target, d)))
      return "recursive query overlaps a denied path; use a narrower directory";
    if (contains(this.root, target)) return;
    if (!writing && tool === "read" && this.originals.has(target)) return;
    return `path outside approved workspace: ${target}`;
  }
}
