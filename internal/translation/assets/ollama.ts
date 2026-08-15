import {
  calculateCost,
  createAssistantMessageEventStream,
  type AssistantMessage,
  type AssistantMessageEventStream,
  type Context,
  type Model,
  type SimpleStreamOptions,
} from "@earendil-works/pi-ai";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const API = "membox-ollama-native" as any;

function messageText(content: unknown): string {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  return content
    .filter((part: any) => part?.type === "text")
    .map((part: any) => String(part.text || ""))
    .join("\n");
}

function nativeMessages(context: Context): Array<{ role: string; content: string }> {
  const messages: Array<{ role: string; content: string }> = [];
  for (const message of context.messages as any[]) {
    if (message.role === "user") {
      const content = messageText(message.content);
      if (content.trim()) messages.push({ role: "user", content });
    } else if (message.role === "assistant") {
      const content = messageText(message.content);
      if (content.trim()) messages.push({ role: "assistant", content });
    }
  }
  return messages;
}

function streamNativeOllama(
  model: Model<any>,
  context: Context,
  options?: SimpleStreamOptions,
): AssistantMessageEventStream {
  const stream = createAssistantMessageEventStream();
  const output: AssistantMessage = {
    role: "assistant",
    content: [],
    api: model.api,
    provider: model.provider,
    model: model.id,
    usage: {
      input: 0,
      output: 0,
      cacheRead: 0,
      cacheWrite: 0,
      totalTokens: 0,
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
    },
    stopReason: "pending",
    timestamp: Date.now(),
  };

  (async () => {
    let textStarted = false;
    // Guard against a stalled Ollama connection: abort after 5 minutes while
    // still honoring the caller's own abort signal (e.g. the user toggling
    // translation off mid-paragraph).
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(new Error("Ollama request timed out after 300s")), 300_000);
    const outer = options?.signal;
    if (outer) {
      if (outer.aborted) controller.abort(outer.reason);
      else outer.addEventListener("abort", () => controller.abort(outer.reason), { once: true });
    }
    try {
      const messages = nativeMessages(context);
      if (!messages.length) throw new Error("translation prompt is empty");
      const response = await fetch(`${model.baseUrl.replace(/\/$/, "")}/api/chat`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          model: model.id,
          messages,
          stream: true,
          think: false,
          options: { temperature: (options as any)?.temperature ?? 0.2, num_ctx: 8192, num_predict: 2048 },
        }),
        signal: controller.signal,
      });
      if (!response.ok || !response.body) {
        throw new Error(`Ollama HTTP ${response.status}: ${await response.text()}`);
      }

      stream.push({ type: "start", partial: output });
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      let terminal: any = null;
      const consume = (line: string) => {
        if (!line.trim()) return;
        const event = JSON.parse(line);
        if (event.error) throw new Error(String(event.error));
        const delta = String(event.message?.content || "");
        if (delta) {
          if (!textStarted) {
            output.content.push({ type: "text", text: "" });
            stream.push({ type: "text_start", contentIndex: 0, partial: output });
            textStarted = true;
          }
          const block = output.content[0];
          if (block.type === "text") block.text += delta;
          stream.push({ type: "text_delta", contentIndex: 0, delta, partial: output });
        }
        if (event.done) terminal = event;
      };

      while (true) {
        const { value, done } = await reader.read();
        buffer += decoder.decode(value || new Uint8Array(), { stream: !done });
        let newline = buffer.indexOf("\n");
        while (newline >= 0) {
          const line = buffer.slice(0, newline).replace(/\r$/, "");
          buffer = buffer.slice(newline + 1);
          consume(line);
          newline = buffer.indexOf("\n");
        }
        if (done) break;
      }
      consume(buffer);
      if (!terminal) throw new Error("Ollama stream ended without a terminal event");
      const textBlock = output.content[0] as { type: string; text: string } | undefined;
      if (!textStarted || !textBlock || textBlock.type !== "text" || !textBlock.text.trim()) {
        throw new Error("Ollama returned an empty translation");
      }

      output.usage.input = Number(terminal.prompt_eval_count) || 0;
      output.usage.output = Number(terminal.eval_count) || 0;
      output.usage.totalTokens = output.usage.input + output.usage.output;
      calculateCost(model, output.usage);
      output.stopReason = terminal.done_reason === "length" ? "length" : "stop";
      stream.push({ type: "text_end", contentIndex: 0, content: textBlock.text, partial: output });
      stream.push({ type: "done", reason: output.stopReason, message: output });
      stream.end();
    } catch (error) {
      output.stopReason = options?.signal?.aborted ? "aborted" : "error";
      output.errorMessage = error instanceof Error ? error.message : String(error);
      stream.push({ type: "error", reason: output.stopReason, error: output });
      stream.end();
    } finally {
      clearTimeout(timer);
    }
  })();

  return stream;
}

export default function (pi: ExtensionAPI) {
  pi.registerProvider("membox-ollama", {
    name: "Membox Ollama Native",
    baseUrl: "http://127.0.0.1:11434",
    apiKey: "ollama",
    api: API,
    models: [{
      id: "qwen3:14b",
      name: "Qwen3 14B (local translation)",
      reasoning: false,
      input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 40960,
      maxTokens: 16384,
    }],
    streamSimple: streamNativeOllama,
  });
}
