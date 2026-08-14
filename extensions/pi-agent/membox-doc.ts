import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { StringEnum } from "@earendil-works/pi-ai";
import { Key } from "@earendil-works/pi-tui";
import { Type } from "typebox";
import { execFile } from "node:child_process";
import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";

/**
 * membox-doc — wrap the `mm doc` CLI for the agent.
 *
 * - Toggle with ctrl+shift+m (or `/membox-doc`): when ON, every message is
 *   scanned for membox document references (short IDs like `11e8`) and the
 *   resolved file is injected into the message context, so "把 membox 的
 *   11e8 列入今天的任务" knows exactly which file is meant.
 * - Registers document and PDF CRUD tools plus path/index admin tools, all
 *   backed by the `mm` CLI. PDF binaries remain in membox's managed PDF root.
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
  media_type?: string;
  authors?: string;
  year?: number;
  keywords?: string;
  page_count?: number;
  size?: number;
};

type PDFImportOutput = {
  document: DocRecord;
  path: string;
};

type PDFSearchHit = {
  document_id: string;
  title: string;
  path: string;
  snippet: string;
};

async function runMm(args: string[], timeout = 10_000, signal?: AbortSignal): Promise<string> {
  const { stdout } = await execFileAsync(findMm(), args, {
    timeout,
    signal,
  });
  return stdout;
}

async function runMmJson<T>(args: string[], timeout = 10_000, signal?: AbortSignal): Promise<T> {
  const out = await runMm(args, timeout, signal);
  return JSON.parse(out) as T;
}

type BridgeFile = { base_url: string; token: string };

function bridgePath(): string {
  return join(process.env.MEMBOX_HOME || join(homedir(), ".membox"), "bridge.json");
}

function readBridge(): BridgeFile {
  return JSON.parse(readFileSync(bridgePath(), "utf8")) as BridgeFile;
}

async function ensureBridge(): Promise<BridgeFile> {
  try {
    const bridge = readBridge();
    const response = await fetch(`${bridge.base_url.replace(/\/$/, "")}/api/bridge/status`, {
      signal: AbortSignal.timeout(1500),
    });
    if (response.ok) return bridge;
  } catch { /* start/reconnect below */ }
  await runMm(["web", "start"], 15_000);
  return readBridge();
}

async function memboxAPI(path: string, init?: RequestInit): Promise<any> {
  const bridge = await ensureBridge();
  const response = await fetch(`${bridge.base_url.replace(/\/$/, "")}${path}`, {
    ...init,
    headers: {
      Accept: "application/json",
      "X-Membox-Token": bridge.token,
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...(init?.headers as Record<string, string> | undefined),
    },
  });
  const text = await response.text();
  let body: any;
  try { body = text ? JSON.parse(text) : {}; } catch { body = { message: text }; }
  if (!response.ok) throw new Error(body?.error?.message || body?.message || text || `HTTP ${response.status}`);
  return body;
}

type VideoSummaryOutput = {
  document_id: string;
  path: string;
  filename: string;
  created: boolean;
  course_code: string;
  course_id?: string;
  course_title?: string;
  lecture_no: number;
  lecture_no_source?: string;
  playlist_index?: number;
  video_id: string;
  lecture_title: string;
  source_url: string;
};

function parseSummarizeCommandArgs(raw: string): { url: string; course?: string } {
  const tokens = raw.trim().split(/\s+/).filter(Boolean);
  let url = "";
  let course: string | undefined;
  for (let i = 0; i < tokens.length; i++) {
    const token = tokens[i];
    if (token === "--course" || token === "-c") {
      course = tokens[++i];
    } else if (!url) {
      url = token;
    } else if (!course) {
      course = token;
    }
  }
  return { url, course };
}

function inferCourseCodeFromTitle(title: string): string | undefined {
  const compact = title.match(/\bCS\s*[- ]?\s*(\d+[A-Z]?)\b/i);
  if (!compact) return undefined;
  const base = `cs${compact[1].toLowerCase()}`;
  const term = title.match(/\b(Fall|Winter|Spring|Summer)\s+(20\d{2})\b/i);
  return term ? `${base}-${term[1].toLowerCase()}${term[2]}` : base;
}

async function inspectYouTubeTitle(url: string): Promise<string | undefined> {
  const candidates = [
    process.env.MMD_YTDLP_BIN,
    join(process.env.HOME || "", "repo", "echo-bp", ".venv", "bin", "yt-dlp"),
    "yt-dlp",
  ].filter((value): value is string => Boolean(value));
  for (const binary of candidates) {
    try {
      const { stdout } = await execFileAsync(binary, ["--no-playlist", "--skip-download", "--print", "%(title)s", url], { timeout: 60_000 });
      const title = stdout.trim().split("\n").at(-1)?.trim();
      if (title) return title;
    } catch {
      // Try the next installation candidate.
    }
  }
  return undefined;
}

async function summarizeVideo(url: string, course: string, signal?: AbortSignal): Promise<VideoSummaryOutput> {
  const out = await runMm(["video", "summarize", url, "--course", course, "--json"], 15 * 60_000, signal);
  return JSON.parse(out) as VideoSummaryOutput;
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

  // URL resources are a membox concern. This extension only adapts Pi to the
  // companion API; extraction, canonicalization, dedupe, ranking persistence,
  // and scan cursors all stay in the Go backend.
  const inboxFile = process.env.MEMBOX_INBOX_FILE || join(homedir(), "repo", "append-review", "journal", "inbox.md");
  const resourceChunkSize = 40;
  const resourceChain: { active: boolean; wave: number; total: number; rows: any[] } = {
    active: false, wave: 0, total: 0, rows: [],
  };

  async function injectResourceWave(): Promise<void> {
    const start = (resourceChain.wave - 1) * resourceChunkSize;
    const batch = resourceChain.rows.slice(start, start + resourceChunkSize);
    const content = batch.map((resource: any) =>
      `${resource.id}\n  title: ${resource.title || "（无标题）"}\n  url: ${resource.url}\n` +
      `  context: ${String(resource.source_line || "（无上下文）").slice(0, 500)}`,
    ).join("\n\n");
    await pi.sendUserMessage(
      `[membox resource review · wave ${resourceChain.wave}/${resourceChain.total}]\n\n` +
      `请评估这些 URL 资源的长期知识价值。只允许调用 membox_resource_assess；不要创建或修改 task、proposal、project 或文档。\n` +
      `规则：\n` +
      `1. 每条给 priority(H/M/L)、score(0-1)、一句话 reason。\n` +
      `2. 一手资料、稀缺资料、可长期复用的技术内容优先；通用首页、临时会话和弱信息收藏降权。\n` +
      `3. 分类描述资源本身的知识价值，不混入任务 deadline 或项目紧迫度。\n` +
      `4. 必须一次调用 membox_resource_assess，source=${JSON.stringify(inboxFile)}，wave=${resourceChain.wave}，expected_count=${batch.length}，覆盖本批全部资源。\n` +
      `5. 完成后一句话汇报本批 H/M/L 数量。\n\n${content}`,
      { deliverAs: "followUp" },
    );
  }

  pi.registerTool({
    name: "membox_resource_ingest",
    label: "Ingest URL resources",
    description: "Extract HTTP(S) URLs from source lines and persist canonical, deduplicated resources in membox.",
    parameters: Type.Object({
      lines: Type.Array(Type.String()),
      source_document_id: Type.Optional(Type.String()),
      source_file: Type.Optional(Type.String()),
      source_commit: Type.Optional(Type.String()),
    }),
    async execute(_toolCallId, params) {
      const out = await memboxAPI("/api/resources/ingest", {
        method: "POST", body: JSON.stringify(params),
      });
      const text = `Found ${out.found ?? 0} URL(s): ${out.inserted ?? 0} inserted, ${out.existing ?? 0} existing.`;
      return { content: [{ type: "text", text }], details: out };
    },
  });

  pi.registerTool({
    name: "membox_resource_list",
    label: "List URL resources",
    description: "List canonical URL resources from membox, ranked by knowledge-value priority and score.",
    parameters: Type.Object({
      limit: Type.Optional(Type.Integer({ minimum: 1, maximum: 200 })),
    }),
    async execute(_toolCallId, params) {
      const out = await memboxAPI(`/api/resources?limit=${params.limit ?? 50}`);
      const rows = (out.resources || []).map((resource: any) =>
        `${resource.id} ${resource.priority} ${resource.score == null ? "-" : Number(resource.score).toFixed(2)} ` +
        `${resource.title || "（无标题）"} — ${resource.url}${resource.reason ? ` — ${resource.reason}` : ""}`,
      );
      return { content: [{ type: "text", text: rows.join("\n") || "No resources." }], details: out };
    },
  });

  pi.registerTool({
    name: "membox_resource_assess",
    label: "Assess URL resources",
    description: "Batch-assign knowledge-value priority, score, and reason to URL resources stored in membox.",
    parameters: Type.Object({
      assessments: Type.Array(Type.Object({
        id: Type.String(),
        priority: StringEnum(["H", "M", "L"] as const),
        score: Type.Number({ minimum: 0, maximum: 1 }),
        reason: Type.String(),
      })),
      wave: Type.Optional(Type.Integer({ minimum: 0 })),
      source: Type.Optional(Type.String()),
      expected_count: Type.Optional(Type.Integer({ minimum: 0 })),
    }),
    async execute(_toolCallId, params) {
      const out = await memboxAPI("/api/resources/assess", {
        method: "POST", body: JSON.stringify(params),
      });
      return {
        content: [{ type: "text", text: `Assessed ${out.assessed ?? 0} membox resource(s).` }],
        details: out,
      };
    },
  });

  pi.registerTool({
    name: "membox_resource_scan",
    label: "Resource review cursor",
    description: "Read or reset membox's importance-review cursor for a resource source.",
    parameters: Type.Object({
      source: Type.String(),
      reset: Type.Optional(Type.Boolean()),
    }),
    async execute(_toolCallId, params) {
      const out = params.reset
        ? await memboxAPI("/api/resources/scan", { method: "POST", body: JSON.stringify(params) })
        : await memboxAPI(`/api/resources/scan?source=${encodeURIComponent(params.source)}`);
      return { content: [{ type: "text", text: JSON.stringify(out, null, 2) }], details: out };
    },
  });

  pi.registerCommand("resources", {
    description: "membox URL resources: list | scan",
    handler: async (args, ctx) => {
      const action = args.trim() || "list";
      try {
        if (action === "list") {
          const out = await memboxAPI("/api/resources?limit=50");
          const rows = out?.resources || [];
          const markdown = rows.length
            ? `**Membox URL Resources**\n\n` + rows.map((resource: any, index: number) =>
                `${index + 1}. \`${resource.priority}\` **${resource.score == null ? "-" : Number(resource.score).toFixed(2)}** ` +
                `${resource.title ? `**${resource.title}** — ` : ""}${resource.url} \`#${String(resource.id).slice(0, 8)}\`` +
                `${resource.reason ? ` — ${resource.reason}` : ""}`,
              ).join("\n")
            : "没有 URL resources";
          pi.sendMessage({ customType: "membox-resources", content: markdown, display: true });
          return;
        }
        if (action === "scan") {
          const out = await memboxAPI("/api/resources/ingest", {
            method: "POST",
            body: JSON.stringify({ source_file: inboxFile }),
          });
          const listed = await memboxAPI(`/api/resources?limit=5000&source_file=${encodeURIComponent(inboxFile)}`);
          const rows = listed?.resources || [];
          if (!rows.length) {
            ctx.ui.notify("URL 扫描完成：没有发现资源", "info");
            return;
          }
          await memboxAPI("/api/resources/scan", {
            method: "POST", body: JSON.stringify({ source: inboxFile, reset: true }),
          });
          Object.assign(resourceChain, {
            active: true, wave: 1,
            total: Math.ceil(rows.length / resourceChunkSize), rows,
          });
          await injectResourceWave();
          ctx.ui.notify(`Membox URL 入库完成（${out.inserted} 新增 / ${out.existing} 已有）`, "info");
          return;
        }
        ctx.ui.notify("Usage: /resources list | scan", "warning");
      } catch (error: any) {
        resourceChain.active = false;
        ctx.ui.notify(`/resources 失败: ${error?.message ?? error}`, "error");
      }
    },
  });

  pi.on("turn_end", async (_event, ctx) => {
    if (!resourceChain.active) return;
    try {
      const state = await memboxAPI(`/api/resources/scan?source=${encodeURIComponent(inboxFile)}`);
      if ((state?.wave ?? 0) < resourceChain.wave) {
        resourceChain.active = false;
        ctx.ui.notify("resource review 中断（游标未推进）— /resources scan 可重启", "warning");
        return;
      }
      if (resourceChain.wave >= resourceChain.total) {
        resourceChain.active = false;
        ctx.ui.notify("resource importance review 完成 🎉 — /resources list 查看排名", "info");
        return;
      }
      resourceChain.wave += 1;
      await injectResourceWave();
    } catch {
      resourceChain.active = false;
    }
  });

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

  const summarizeCommand = {
    description: "Download YouTube subtitles, summarize through echo-bp, and save to membox. Usage: /summarize <url> [course] or --course <code>",
    handler: async (rawArgs: string, ctx: ExtensionContext) => {
      const parsed = parseSummarizeCommandArgs(rawArgs);
      if (!parsed.url || !/^https?:\/\/(?:www\.)?(?:youtube\.com|youtu\.be)\//i.test(parsed.url)) {
        ctx.ui.notify("Usage: /summarize <youtube-url> [course]", "error");
        return;
      }
      let course = parsed.course;
      let title: string | undefined;
      if (!course) {
        ctx.ui.setStatus("membox-summary", ctx.ui.theme.fg("dim", "Inspecting video…"));
        title = await inspectYouTubeTitle(parsed.url);
        course = title ? inferCourseCodeFromTitle(title) : undefined;
      }
      if (!course && ctx.hasUI) {
        course = await ctx.ui.input("Course code", "e.g. cs106l-fall2019");
      }
      course = course?.trim().toLowerCase();
      if (!course || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(course)) {
        ctx.ui.setStatus("membox-summary", undefined);
        ctx.ui.notify("A stable course code is required, e.g. /summarize <url> cs106l-fall2019", "error");
        return;
      }
      ctx.ui.setStatus("membox-summary", ctx.ui.theme.fg("accent", `Summarizing ${course}…`));
      try {
        const result = await summarizeVideo(parsed.url, course);
        const source = result.lecture_no_source || "unknown";
        ctx.ui.notify(
          `${result.created ? "Created" : "Updated"} ${result.filename}\n${result.lecture_title}\n${result.document_id}\nlecture=${result.lecture_no} (${source}), playlist_index=${result.playlist_index ?? "?"}`,
          "info",
        );
      } catch (error: any) {
        const detail = error?.stderr?.trim() || error?.message || String(error);
        ctx.ui.notify(`Video summary failed: ${detail}`, "error");
      } finally {
        ctx.ui.setStatus("membox-summary", undefined);
      }
    },
  };
  pi.registerCommand("summarize", summarizeCommand);
  // Keep the user's original spelling as an alias.
  pi.registerCommand("sumamrise", summarizeCommand);

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
    description: "Print the Markdown content of a membox document (selector: short ID, UUID, or path). For PDFs use membox_pdf_show/search/open.",
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

  // ── PDF management ─────────────────────────────────────────────────────
  // These are intentionally thin adapters over `mm pdf ...`. The CLI remains
  // the authority for validation, metadata persistence, trash, and opening.

  pi.registerTool({
    name: "membox_pdf_import",
    label: "Import PDF into membox",
    description:
      "Copy one local PDF into membox's managed PDF directory and index its metadata and extractable text. Wraps `mm pdf import`. Requires confirmation.",
    parameters: Type.Object({
      source: Type.String({ description: "Path to the source .pdf file" }),
      destination: Type.Optional(Type.String({ description: "Optional managed PDF directory; defaults to ~/Documents/membox-pdfs" })),
      title: Type.Optional(Type.String({ description: "Optional searchable title override" })),
      authors: Type.Optional(Type.String({ description: "Optional searchable authors" })),
      year: Type.Optional(Type.Integer({ description: "Optional four-digit publication year", minimum: 1000, maximum: 9999 })),
      keywords: Type.Optional(Type.String({ description: "Optional searchable keywords" })),
    }),
    async execute(_toolCallId, params, signal, _onUpdate, ctx) {
      const target = params.destination || "~/Documents/membox-pdfs";
      const ok = await ctx.ui.confirm(
        "Import PDF into membox?",
        `${params.source}\n→ ${target}\n\nThe source is copied; SQLite stores metadata/index data, not the PDF binary.`,
      );
      if (!ok) {
        return { content: [{ type: "text", text: "PDF import cancelled." }], details: { cancelled: true } };
      }
      const args = ["pdf", "import", params.source, "--json"];
      if (params.destination) args.push("--to", params.destination);
      if (params.title) args.push("--title", params.title);
      if (params.authors) args.push("--authors", params.authors);
      if (params.year !== undefined) args.push("--year", String(params.year));
      if (params.keywords) args.push("--keywords", params.keywords);
      const result = await runMmJson<PDFImportOutput>(args, 120_000, signal);
      return {
        content: [{ type: "text", text: `Imported ${result.document.title || result.document.relative_path} → ${result.document.id}\n${result.path}` }],
        details: result,
      };
    },
  });

  pi.registerTool({
    name: "membox_pdf_list",
    label: "List membox PDFs",
    description: "List indexed PDF documents and searchable metadata. Wraps `mm pdf list --json`.",
    parameters: Type.Object({
      limit: Type.Optional(Type.Integer({ description: "Maximum PDFs (default 50)", minimum: 1, maximum: 200 })),
      include_unavailable: Type.Optional(Type.Boolean({ description: "Include missing and untracked PDFs" })),
    }),
    async execute(_toolCallId, params, signal) {
      const args = ["pdf", "list", "--json", "--limit", String(params.limit ?? 50)];
      if (params.include_unavailable) args.push("--all");
      const documents = await runMmJson<DocRecord[]>(args, 30_000, signal);
      const rows = documents.map((doc) => {
        const metadata = [doc.authors, doc.year, doc.page_count ? `${doc.page_count} pages` : ""].filter(Boolean).join(" · ");
        return `- ${doc.id.slice(-4)} ${doc.title || doc.relative_path}${metadata ? ` — ${metadata}` : ""}\n  ${doc.path}`;
      });
      return {
        content: [{ type: "text", text: rows.length ? rows.join("\n") : "No PDFs." }],
        details: { count: documents.length, documents },
      };
    },
  });

  pi.registerTool({
    name: "membox_pdf_search",
    label: "Search membox PDFs",
    description: "Search PDF title, authors, year, keywords, path, and extracted text. Wraps `mm pdf search --json`.",
    parameters: Type.Object({
      query: Type.String({ description: "Search query" }),
      limit: Type.Optional(Type.Integer({ description: "Maximum results (default 20)", minimum: 1, maximum: 50 })),
    }),
    async execute(_toolCallId, params, signal) {
      const hits = await runMmJson<PDFSearchHit[]>([
        "pdf", "search", params.query, "--json", "--limit", String(params.limit ?? 20),
      ], 30_000, signal);
      const rows = hits.map((hit) =>
        `- ${hit.document_id.slice(-4)} ${hit.title}\n  ${hit.path}${hit.snippet ? `\n  ${hit.snippet.replace(/\s+/g, " ").slice(0, 300)}` : ""}`,
      );
      return {
        content: [{ type: "text", text: rows.length ? rows.join("\n") : "No PDF hits." }],
        details: { count: hits.length, hits },
      };
    },
  });

  pi.registerTool({
    name: "membox_pdf_show",
    label: "Show membox PDF",
    description: "Show one PDF's stable UUID, path, status, metadata, size, and SHA-256. Wraps `mm pdf show --json`.",
    parameters: Type.Object({
      selector: Type.String({ description: "PDF selector: short ID or full UUID" }),
    }),
    async execute(_toolCallId, params, signal) {
      const doc = await runMmJson<DocRecord>(["pdf", "show", params.selector, "--json"], 10_000, signal);
      return {
        content: [{ type: "text", text: JSON.stringify(doc, null, 2) }],
        details: doc,
      };
    },
  });

  pi.registerTool({
    name: "membox_pdf_update",
    label: "Update membox PDF metadata",
    description:
      "Update searchable PDF catalog metadata without rewriting binary PDF bytes. Wraps `mm pdf update`. Requires confirmation.",
    parameters: Type.Object({
      selector: Type.String({ description: "PDF selector: short ID or full UUID" }),
      title: Type.Optional(Type.String({ description: "New title; empty resets to filename" })),
      authors: Type.Optional(Type.String({ description: "New authors; empty clears" })),
      year: Type.Optional(Type.Integer({ description: "Four-digit publication year, or 0 to clear", minimum: 0, maximum: 9999 })),
      keywords: Type.Optional(Type.String({ description: "New keywords; empty clears" })),
    }),
    async execute(_toolCallId, params, signal, _onUpdate, ctx) {
      const changes = [
        params.title !== undefined ? `title=${JSON.stringify(params.title)}` : "",
        params.authors !== undefined ? `authors=${JSON.stringify(params.authors)}` : "",
        params.year !== undefined ? `year=${params.year}` : "",
        params.keywords !== undefined ? `keywords=${JSON.stringify(params.keywords)}` : "",
      ].filter(Boolean);
      if (changes.length === 0) throw new Error("At least one PDF metadata field is required");
      const ok = await ctx.ui.confirm("Update PDF metadata?", `${params.selector}\n${changes.join("\n")}`);
      if (!ok) {
        return { content: [{ type: "text", text: "PDF metadata update cancelled." }], details: { cancelled: true } };
      }
      const args = ["pdf", "update", params.selector, "--json"];
      if (params.title !== undefined) args.push("--title", params.title);
      if (params.authors !== undefined) args.push("--authors", params.authors);
      if (params.year !== undefined) args.push("--year", String(params.year));
      if (params.keywords !== undefined) args.push("--keywords", params.keywords);
      const doc = await runMmJson<DocRecord>(args, 30_000, signal);
      return {
        content: [{ type: "text", text: `Updated PDF metadata for ${doc.title || doc.id} (${doc.id})` }],
        details: doc,
      };
    },
  });

  pi.registerTool({
    name: "membox_pdf_open",
    label: "Open membox PDF",
    description: "Open an indexed PDF with the operating system viewer (`open` on macOS). Wraps `mm pdf open`.",
    parameters: Type.Object({
      selector: Type.String({ description: "PDF selector: short ID or full UUID" }),
    }),
    async execute(_toolCallId, params, signal) {
      await runMm(["pdf", "open", params.selector], 30_000, signal);
      return { content: [{ type: "text", text: `Opened PDF ${params.selector}.` }], details: { selector: params.selector } };
    },
  });

  pi.registerTool({
    name: "membox_pdf_rename",
    label: "Rename membox PDF",
    description: "Rename a PDF while preserving its UUID; optionally update its searchable title. Wraps `mm pdf rename`. Requires confirmation.",
    parameters: Type.Object({
      selector: Type.String({ description: "PDF selector: short ID or full UUID" }),
      new_filename: Type.String({ description: "New filename ending in .pdf" }),
      title: Type.Optional(Type.String({ description: "Optional new searchable title" })),
    }),
    async execute(_toolCallId, params, signal, _onUpdate, ctx) {
      const ok = await ctx.ui.confirm("Rename PDF?", `${params.selector}\n→ ${params.new_filename}${params.title ? `\ntitle: ${params.title}` : ""}`);
      if (!ok) {
        return { content: [{ type: "text", text: "PDF rename cancelled." }], details: { cancelled: true } };
      }
      const args = ["pdf", "rename", params.selector, params.new_filename, "--json"];
      if (params.title !== undefined) args.push("--title", params.title);
      const result = await runMmJson<{ document_id: string; path: string; title?: string }>(args, 30_000, signal);
      return {
        content: [{ type: "text", text: `Renamed PDF ${result.document_id}: ${result.path}` }],
        details: result,
      };
    },
  });

  pi.registerTool({
    name: "membox_pdf_delete",
    label: "Delete membox PDF (soft)",
    description: "Move an indexed PDF to membox trash. It remains restorable. Wraps `mm pdf delete`. Requires confirmation.",
    parameters: Type.Object({
      selector: Type.String({ description: "PDF selector: short ID or full UUID" }),
    }),
    async execute(_toolCallId, params, signal, _onUpdate, ctx) {
      const doc = await runMmJson<DocRecord>(["pdf", "show", params.selector, "--json"], 10_000, signal);
      const ok = await ctx.ui.confirm("Delete PDF?", `Move to trash?\n${doc.title || doc.relative_path}\n${doc.path}`);
      if (!ok) {
        return { content: [{ type: "text", text: "PDF deletion cancelled." }], details: { cancelled: true } };
      }
      const result = await runMmJson<{ document_id: string; path: string; trashed: boolean }>(
        ["pdf", "delete", params.selector, "--json"], 30_000, signal,
      );
      return {
        content: [{ type: "text", text: `Trashed PDF ${result.document_id}: ${result.path}` }],
        details: result,
      };
    },
  });

  pi.registerTool({
    name: "membox_pdf_restore",
    label: "Restore membox PDF",
    description: "Restore a PDF from membox trash to its original location. Wraps `mm pdf restore`. Requires confirmation.",
    parameters: Type.Object({
      selector: Type.String({ description: "PDF selector: short ID or full UUID" }),
    }),
    async execute(_toolCallId, params, signal, _onUpdate, ctx) {
      const doc = await runMmJson<DocRecord>(["pdf", "show", params.selector, "--json"], 10_000, signal);
      const ok = await ctx.ui.confirm("Restore PDF?", `${doc.title || doc.relative_path}\n${doc.path}`);
      if (!ok) {
        return { content: [{ type: "text", text: "PDF restore cancelled." }], details: { cancelled: true } };
      }
      const result = await runMmJson<{ document_id: string; path: string }>(
        ["pdf", "restore", params.selector, "--json"], 30_000, signal,
      );
      return {
        content: [{ type: "text", text: `Restored PDF ${result.document_id}: ${result.path}` }],
        details: result,
      };
    },
  });

  // ── path & index management ────────────────────────────────────────────
  // Wrap `mm path add/remove/list/scan` and `mm index status`. The document
  // and PDF CRUD tools above intentionally leave catalog admin out.

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
    description: "Add a directory to membox's scan paths and scan it immediately. Use when the user wants membox to index a new folder of Markdown or PDF files. Relative paths resolve against the current working directory.",
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
    name: "membox_video_summarize",
    label: "Summarize a YouTube lecture into membox",
    description:
      "Ask the running mmd service to process one YouTube video through echo-bp, then save/update its summary in membox's default path as <course>-lec<N>.md. The URL must identify one video. By default N is inferred from a readable title such as 'Lecture 7', with playlist position only as fallback; lecture explicitly overrides inference.",
    parameters: Type.Object({
      url: Type.String({ description: "YouTube watch/video URL (may include a playlist)" }),
      course: Type.String({ description: "Stable readable course code, e.g. cs336 or stanford-cs336-2025" }),
      lecture: Type.Optional(Type.Integer({ description: "Explicit semantic lecture number. Set only when the user explicitly provides it; never copy the URL's playlist index. Omit to infer from the readable video title.", minimum: 1 })),
      force: Type.Optional(Type.Boolean({ description: "Regenerate existing echo-bp transcript/summary artifacts" })),
    }),
    async execute(_toolCallId, params, signal, onUpdate, ctx) {
      const label = params.lecture === undefined
        ? `${params.course} (lecture inferred from title)`
        : `${params.course}-lec${params.lecture}`;
      const ok = await ctx.ui.confirm(
        "Generate video summary?",
        `echo-bp will download subtitles, call pi for a summary, and publish ${label} into membox.\n\n${params.url}`,
      );
      if (!ok) {
        return { content: [{ type: "text", text: "Video summary cancelled." }], details: { cancelled: true } };
      }
      onUpdate?.({ content: [{ type: "text", text: `Processing ${params.url} through mmd…` }], details: { state: "running" } });
      const args = ["video", "summarize", params.url, "--course", params.course, "--json"];
      if (params.lecture !== undefined) args.push("--lecture", String(params.lecture));
      if (params.force) args.push("--force");
      try {
        const out = await runMm(args, 15 * 60_000, signal);
        const result = JSON.parse(out) as VideoSummaryOutput;
        const action = result.created ? "Created" : "Updated";
        return {
          content: [{ type: "text", text: `${action} ${result.filename} → ${result.document_id}\n${result.lecture_title}\n${result.path}` }],
          details: result,
        };
      } catch (error: any) {
        const detail = error?.stderr?.trim() || error?.message || String(error);
        return { content: [{ type: "text", text: `Video summary failed: ${detail}` }], details: { error: detail }, isError: true };
      }
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
