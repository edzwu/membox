import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Key } from "@earendil-works/pi-tui";
import { Type } from "typebox";
import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";

/**
 * membox-doc — wrap the `mm doc` CLI for the agent.
 *
 * - Toggle with ctrl+shift+m (or `/membox-doc`): when ON, every message is
 *   scanned for membox document references (short IDs like `11e8`) and the
 *   resolved file is injected into the message context, so "把 membox 的
 *   11e8 列入今天的任务" knows exactly which file is meant.
 * - Registers doc CRUD tools (list / search / resolve / cat / create /
 *   rename / delete) and path/index admin tools (path add/remove/list/scan,
 *   index status), all backed by the `mm` CLI.
 */

const execFileAsync = promisify(execFile);

const SHORT_ID_RE = /\b[0-9a-fA-F]{4,10}\b/g;

function findMm(): string {
  if (process.env.MEMBOX_MM) return process.env.MEMBOX_MM;
  const home = process.env.HOME || "";
  const candidates = [join(home, "repo", "membox", "bin", "mm"), "mm"];
  return candidates[0];
}

type DocRecord = {
  id: string;
  path: string;
  relative_path: string;
  status: string;
  title: string;
  size?: number;
};

async function runMm(args: string[], timeout = 10_000): Promise<string> {
  const { stdout } = await execFileAsync(findMm(), args, {
    timeout,
  });
  return stdout;
}

async function runMmJson<T>(args: string[], timeout = 10_000): Promise<T> {
  const out = await runMm(args, timeout);
  return JSON.parse(out) as T;
}

/** Resolve a selector (full ID, short ID tail, path) to one document. */
async function resolveDoc(selector: string): Promise<DocRecord | null> {
  try {
    return await runMmJson<DocRecord>(["doc", "show", selector, "--json"]);
  } catch {
    return null;
  }
}

export default function (pi: ExtensionAPI) {
  let enabled = false;

  function toggle(ctx: ExtensionContext) {
    enabled = !enabled;
    ctx.ui.notify(`membox-doc ${enabled ? "enabled" : "disabled"}`, "info");
    updateStatus(ctx);
  }

  function updateStatus(ctx: ExtensionContext) {
    if (!enabled) {
      ctx.ui.setStatus("membox-doc", undefined);
      return;
    }
    const theme = ctx.ui.theme;
    const icon = theme.fg("accent", "◈");
    const text = theme.fg("dim", " membox");
    ctx.ui.setStatus("membox-doc", icon + text);
  }

  pi.registerCommand("membox-doc", {
    description: "Toggle membox doc reference resolution",
    handler: async (_args, ctx) => toggle(ctx),
  });

  pi.registerShortcut(Key.ctrlShift("m"), {
    description: "Toggle membox doc reference resolution",
    handler: async (ctx) => toggle(ctx),
  });

  pi.on("session_start", async (_event, ctx) => {
    updateStatus(ctx);
  });

  // When enabled, resolve short IDs mentioned in the user message and inject
  // the file context so the model knows exactly which document is meant.
  pi.on("input", async (event, ctx) => {
    if (event.source === "extension") return { action: "continue" };
    if (!enabled || !event.text) return { action: "continue" };

    const candidates = [...new Set(event.text.match(SHORT_ID_RE) || [])].slice(0, 5);
    if (candidates.length === 0) return { action: "continue" };

    const resolved: string[] = [];
    for (const token of candidates) {
      const doc = await resolveDoc(token);
      if (doc) {
        resolved.push(`"${token}" → ${doc.title || doc.relative_path} (${doc.id.slice(0, 8)}…, ${doc.status}, ${doc.path})`);
      }
    }
    if (resolved.length === 0) return { action: "continue" };

    const note = `\n\n[membox doc context (auto-resolved)]\n${resolved.join("\n")}\n`;
    return { action: "transform", text: event.text + note };
  });

  pi.registerTool({
    name: "membox_doc_resolve",
    label: "Resolve membox doc reference",
    description:
      "Resolve a membox document selector (short ID tail like '11e8', full UUID, or path) into the concrete file. Use this whenever the user refers to 'membox 的 <id>' or a bare short ID.",
    parameters: Type.Object({
      selector: Type.String({ description: "Document selector: short ID (e.g. 11e8), full UUID, or path" }),
    }),
    async execute(_toolCallId, params) {
      const doc = await resolveDoc(params.selector);
      if (!doc) {
        return {
          content: [{ type: "text", text: `No document matched ${JSON.stringify(params.selector)}` }],
          details: {},
        };
      }
      return {
        content: [
          {
            type: "text",
            text: [
              `id: ${doc.id}`,
              `title: ${doc.title}`,
              `path: ${doc.path}`,
              `status: ${doc.status}`,
            ].join("\n"),
          },
        ],
        details: { document_id: doc.id, path: doc.path },
      };
    },
  });

  pi.registerTool({
    name: "membox_doc_search",
    label: "Search membox documents",
    description: "Full-text search over membox documents (titles and bodies). Returns id, title, path and a snippet per hit.",
    parameters: Type.Object({
      query: Type.String({ description: "Search query" }),
      limit: Type.Optional(Type.Integer({ description: "Max results (default 10)", minimum: 1, maximum: 50 })),
    }),
    async execute(_toolCallId, params) {
      const hits = await runMmJson<Array<{ document_id: string; title: string; path: string; snippet: string }>>([
        "doc", "search", params.query, "--json",
      ]);
      const rows = (hits || []).slice(0, params.limit ?? 10).map((h) => {
        const id = h.document_id.slice(0, 8);
        return `- ${id} ${h.title} @ ${h.path}\n  ${(h.snippet || "").replace(/\s+/g, " ").slice(0, 160)}`;
      });
      return {
        content: [{ type: "text", text: rows.length ? rows.join("\n") : "No hits." }],
        details: { count: rows.length },
      };
    },
  });

  pi.registerTool({
    name: "membox_doc_cat",
    label: "Read a membox document",
    description: "Print the full content of a membox document (selector: short ID, UUID, or path).",
    parameters: Type.Object({
      selector: Type.String({ description: "Document selector" }),
      maxChars: Type.Optional(Type.Integer({ description: "Truncate output (default 6000)", minimum: 500 })),
    }),
    async execute(_toolCallId, params) {
      const out = await runMm(["doc", "cat", params.selector]);
      const max = params.maxChars ?? 6000;
      const text = out.length > max ? `${out.slice(0, max)}\n… [truncated]` : out;
      return { content: [{ type: "text", text }], details: { chars: out.length } };
    },
  });

  pi.registerTool({
    name: "membox_doc_list",
    label: "List membox documents",
    description: "List membox documents (optionally filtered by a substring of title/path).",
    parameters: Type.Object({
      filter: Type.Optional(Type.String({ description: "Substring filter on title/path" })),
      limit: Type.Optional(Type.Integer({ description: "Max rows (default 50)", minimum: 1, maximum: 200 })),
    }),
    async execute(_toolCallId, params) {
      const docs = await runMmJson<DocRecord[]>(["doc", "list", "--json"]);
      const filtered = (params.filter ? docs.filter((d) => (d.title + " " + d.path).toLowerCase().includes(params.filter!.toLowerCase())) : docs)
        .slice(0, params.limit ?? 50);
      const rows = filtered.map((d) => `- ${d.id.slice(0, 8)} ${d.title || d.relative_path} (${d.status})`);
      return {
        content: [{ type: "text", text: rows.length ? rows.join("\n") : "No documents." }],
        details: { count: rows.length },
      };
    },
  });

  pi.registerTool({
    name: "membox_doc_create",
    label: "Create a membox document",
    description:
      "Create a new text document in the membox notes directory (writes the file and rescans so it gets a UUID). Use for tasks, scratchpads, or notes the user asks to save.",
    parameters: Type.Object({
      filename: Type.String({ description: "Filename, e.g. 'task-2026-08-08.md'. Non-markdown extensions are allowed." }),
      content: Type.String({ description: "Full file content" }),
    }),
    async execute(_toolCallId, params) {
      const paths = await runMmJson<Array<{ id: number; path: string; status: string }>>(["path", "list", "--json"]).catch(() => []);
      const ready = paths.find((p) => p.status === "ready") || paths[0];
      if (!ready) {
        return { content: [{ type: "text", text: "No membox scan path configured (mm path add <dir>)" }], details: {} };
      }
      const file = join(ready.path, params.filename);
      const fs = await import("node:fs/promises");
      if (await fs.stat(file).then(() => true, () => false)) {
        return { content: [{ type: "text", text: `File already exists: ${file}` }], details: {} };
      }
      await fs.writeFile(file, params.content, "utf8");
      await runMm(["path", "scan"]).catch(() => {});
      const doc = await resolveDoc(params.filename);
      return {
        content: [{ type: "text", text: doc ? `Created ${file} → ${doc.id}` : `Created ${file} (indexed by next scan)` }],
        details: { path: file, document_id: doc?.id },
      };
    },
  });

  pi.registerTool({
    name: "membox_doc_rename",
    label: "Rename a membox document",
    description: "Rename a membox document's file without changing its UUID.",
    parameters: Type.Object({
      selector: Type.String({ description: "Document selector" }),
      newFilename: Type.String({ description: "New filename (must keep the same extension)" }),
    }),
    async execute(_toolCallId, params) {
      const out = await runMm(["doc", "rename", params.selector, params.newFilename]);
      return { content: [{ type: "text", text: out.trim() || "Renamed." }], details: {} };
    },
  });

  pi.registerTool({
    name: "membox_doc_delete",
    label: "Delete a membox document (soft)",
    description: "Move a membox document to the trash (soft delete; restorable via mm trash restore).",
    parameters: Type.Object({
      selector: Type.String({ description: "Document selector" }),
    }),
    async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
      const doc = await resolveDoc(params.selector);
      if (!doc) {
        return { content: [{ type: "text", text: `No document matched ${JSON.stringify(params.selector)}` }], details: {} };
      }
      const ok = await ctx.ui.confirm("Delete membox doc", `Move to trash?\n${doc.title || doc.relative_path}\n${doc.path}`);
      if (!ok) return { content: [{ type: "text", text: "Deletion cancelled." }], details: {} };
      await runMm(["doc", "delete", params.selector]);
      return { content: [{ type: "text", text: `Trashed ${doc.title || doc.path}` }], details: { document_id: doc.id } };
    },
  });

  // ── path & index management ────────────────────────────────────────────
  // Wrap `mm path add/remove/list/scan` and `mm index status`. The doc CRUD
  // tools above intentionally leave catalog admin out; these cover it.

  function formatScan(s: {
    paths?: number; files?: number; added?: number; updated?: number;
    renamed?: number; unchanged?: number; missing?: number;
    possible_renames?: number; errors?: number; timestamp_source?: string;
  }): string {
    return [
      `Scanned ${s.paths ?? 0} path(s), ${s.files ?? 0} file(s):`,
      `+${s.added ?? 0} added, ${s.updated ?? 0} updated, ${s.renamed ?? 0} renamed, ${s.unchanged ?? 0} unchanged, ${s.missing ?? 0} missing, ${s.errors ?? 0} errors`,
      s.timestamp_source ? `timestamp source: ${s.timestamp_source}` : "",
    ].filter(Boolean).join("\n");
  }

  pi.registerTool({
    name: "membox_path_list",
    label: "List membox scan paths",
    description: "List configured membox scan paths (directories membox indexes). No parameters.",
    parameters: Type.Object({}),
    async execute(_toolCallId, _params) {
      const paths = await runMmJson<Array<{ id: number; path: string; documents: number; status: string; last_scan_at?: string }>>(["path", "list", "--json"]);
      const rows = (paths || []).map((p) => {
        const last = p.last_scan_at ? p.last_scan_at.replace("T", " ").slice(0, 16) : "?";
        return `${p.id}  ${p.path}  [${p.documents} docs, ${p.status}, scanned ${last}]`;
      });
      return {
        content: [{ type: "text", text: rows.length ? rows.join("\n") : "No scan paths configured. Use membox_path_add to add one." }],
        details: { count: rows.length, paths: paths || [] },
      };
    },
  });

  pi.registerTool({
    name: "membox_path_add",
    label: "Add a membox scan path",
    description: "Add a directory to membox's scan paths and scan it immediately. Use when the user wants membox to index a new folder of Markdown files. Relative paths resolve against the current working directory.",
    parameters: Type.Object({
      directory: Type.String({ description: "Directory to add (absolute or relative path)" }),
    }),
    async execute(_toolCallId, params) {
      const result = await runMmJson<{ path: { id: number; path: string; documents: number; status: string }; already_exists?: boolean; scan: any }>(["path", "add", params.directory, "--json"]);
      const line = result?.already_exists
        ? `Path already configured: ${result.path.path} (id ${result.path.id})`
        : `Added path ${result.path.id}: ${result.path.path} (${result.path.status})`;
      const scanLine = result?.scan ? "\n" + formatScan(result.scan) : "";
      return {
        content: [{ type: "text", text: `${line}${scanLine}` }],
        details: { path: result.path, scan: result.scan, already_exists: result.already_exists ?? false },
      };
    },
  });

  pi.registerTool({
    name: "membox_path_scan",
    label: "Scan membox paths",
    description: "Re-scan membox paths to pick up filesystem changes (new/edited/renamed/deleted files). Optionally set timestamp='git' to derive document dates from Git history (paths must be inside a Git worktree).",
    parameters: Type.Object({
      selector: Type.Optional(Type.String({ description: "Optional path ID or directory to scan (default: all configured paths)" })),
      timestamp: Type.Optional(Type.String({ description: "Document timestamp source: 'filesystem' (default) or 'git'" })),
    }),
    async execute(_toolCallId, params) {
      const args = ["path", "scan", "--json"];
      if (params.selector) args.push(params.selector);
      if (params.timestamp === "git") args.push("--timestamp=git");
      const result = await runMmJson<{
        paths: number; files: number; added: number; updated: number; renamed: number;
        unchanged: number; missing: number; possible_renames: number; errors: number;
        timestamp_source?: string;
      }>(args, 120_000);
      return {
        content: [{ type: "text", text: formatScan(result) }],
        details: result,
      };
    },
  });

  pi.registerTool({
    name: "membox_path_remove",
    label: "Remove a membox scan path",
    description: "Remove a directory from membox's scan paths. Does not delete files on disk; documents under this path become untracked and disappear from listings/search until re-added. Requires confirmation.",
    parameters: Type.Object({
      selector: Type.String({ description: "Path ID or directory to remove" }),
    }),
    async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
      const paths = await runMmJson<Array<{ id: number; path: string; documents: number; status: string }>>(["path", "list", "--json"]).catch(() => []);
      const match = (paths || []).find((p) => String(p.id) === params.selector || p.path === params.selector);
      const preview = match
        ? `Remove path ${match.id}: ${match.path}\n${match.documents} document(s) will become untracked.`
        : `Remove path ${params.selector}`;
      const ok = await ctx.ui.confirm("Remove membox path?", preview);
      if (!ok) return { content: [{ type: "text", text: "Removal cancelled." }], details: { cancelled: true } };
      await runMm(["path", "remove", params.selector]);
      return {
        content: [{ type: "text", text: match ? `Removed path ${match.id}: ${match.path}` : `Removed ${params.selector}` }],
        details: { removed: true, path: match?.path },
      };
    },
  });

  pi.registerTool({
    name: "membox_index_status",
    label: "Show membox index status",
    description: "Show membox index status: number of paths, active/missing/untracked documents, database location, and last scan time.",
    parameters: Type.Object({}),
    async execute(_toolCallId, _params) {
      const result = await runMmJson<{ paths: number; active: number; missing: number; untracked: number; last_scan_at: string; database_path: string }>(["index", "status", "--json"]);
      const last = result.last_scan_at ? result.last_scan_at.replace("T", " ").replace(/\.\d+Z$/, "Z") : "?";
      const text = [
        `Paths: ${result.paths}   Active: ${result.active}   Missing: ${result.missing}   Untracked: ${result.untracked}`,
        `Database:  ${result.database_path}`,
        `Last scan: ${last}`,
      ].join("\n");
      return { content: [{ type: "text", text }], details: result };
    },
  });
}
