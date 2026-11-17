// A streamed answer (text/event-stream), as plain code with no DOM (node runs it in test/sse.test.ts):
// the events of OpenAI's chat completions, put together, and the words of an agent step ("say") read while
// the JSON is still being written. index.ts (chat) and agent.ts (agent steps) use it.

// Splits the text that arrives in pieces into the events it holds; a piece may end in the middle of one.
export class SSEParser {
  private buf = "";

  // The data of each complete event in what has come so far, parsed; "[DONE]" and what is not JSON are left out.
  feed(piece: string): any[] {
    this.buf += piece.replace(/\r\n/g, "\n");
    const out: any[] = [];
    let at = this.buf.indexOf("\n\n");
    while (at >= 0) {
      const block = this.buf.slice(0, at);
      this.buf = this.buf.slice(at + 2);
      const data = block
        .split("\n")
        .filter((l) => l.startsWith("data:"))
        .map((l) => l.slice(5).replace(/^ /, ""))
        .join("\n");
      if (data && data !== "[DONE]") {
        try {
          out.push(JSON.parse(data));
        } catch (e) {
          // a piece of text that is not JSON is not an event
        }
      }
      at = this.buf.indexOf("\n\n");
    }
    return out;
  }

  // what is left when the stream ended: an event that was not closed with an empty line
  rest(): any[] {
    const r = this.buf.trim();
    this.buf = "";
    return r ? this.feed(r + "\n\n") : [];
  }
}

export interface Streamed {
  content: string; // what the model wrote
  toolArgs: string; // the arguments of the function it called, when it called one
  usage: any; // what the answer used, as the host says (the last event that has it)
  finish: string; // why it ended: "stop", "length" ...
  error: string; // an error the host sent in the stream
}

export function newStreamed(): Streamed {
  return { content: "", toolArgs: "", usage: null, finish: "", error: "" };
}

// Adds an event to what was streamed so far. True when the words (content or call) grew.
export function addEvent(s: Streamed, ev: any): boolean {
  let grew = false;
  if (ev && ev.error) s.error = String((ev.error && ev.error.message) || ev.error).slice(0, 300);
  if (ev && ev.usage) s.usage = ev.usage;
  const choice = ev && Array.isArray(ev.choices) ? ev.choices[0] : null;
  if (choice) {
    const d = choice.delta || {};
    if (typeof d.content === "string" && d.content) {
      s.content += d.content;
      grew = true;
    }
    const call = Array.isArray(d.tool_calls) ? d.tool_calls[0] : null;
    if (call && call.function && typeof call.function.arguments === "string" && call.function.arguments) {
      s.toolArgs += call.function.arguments;
      grew = true;
    }
    if (choice.finish_reason) s.finish = String(choice.finish_reason);
  }
  return grew;
}

// The answer as the page reads it: the text, or the arguments of the call.
export function answerOf(s: Streamed): string {
  return s.content !== "" ? s.content : s.toolArgs;
}

// The words of "say" in a JSON object that is still being written, so far: "" when it has not begun.
export function partialSay(text: string): string {
  const m = /"say"\s*:\s*"/.exec(text);
  if (!m) return "";
  let out = "";
  for (let i = m.index + m[0].length; i < text.length; i++) {
    const c = text[i];
    if (c === '"') return out;
    if (c !== "\\") {
      out += c;
      continue;
    }
    const n = text[i + 1];
    if (n === undefined) return out; // the escape is not here yet
    if (n === "u") {
      const hex = text.slice(i + 2, i + 6);
      if (hex.length < 4) return out;
      out += String.fromCharCode(parseInt(hex, 16) || 63);
      i += 5;
    } else {
      out += n === "n" ? "\n" : n === "t" ? "\t" : n;
      i += 1;
    }
  }
  return out;
}
