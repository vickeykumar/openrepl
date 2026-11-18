// The model list of Genie and the New question dialog (resources/js/model-choice.js): what is offered
// for each provider, the short list ("Your models": pinned, then last used), "Other models" by
// provider, and search. The picker itself is checked in a browser (e2e/tests/model-picker.spec.js).
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const source = fs.readFileSync(path.join(here, "..", "..", "resources", "js", "model-choice.js"), "utf8");

// load the script as a page does: with what config.js and settings.js define, and a localStorage
function load({ openrouter = false, cloudflare = false, site = {}, store = {}, signedIn = false } = {}) {
  const window = {
    openrouter_enabled: openrouter,
    cloudflare_enabled: cloudflare,
    site_settings: site,
    addEventListener() {},
  };
  const localStorage = {
    getItem: (k) => (k in store ? store[k] : null),
    setItem: (k, v) => { store[k] = String(v); },
  };
  const document = { body: { classList: { contains: (c) => signedIn && c === "is-signed-in" } } };
  const ctx = { window, localStorage, document, console, JSON, Array, Object };
  vm.createContext(ctx);
  vm.runInContext(source, ctx);
  return { MC: window.ModelChoice, store };
}
// what the script made lives in another realm (the vm): compared as plain data
const names = (list) => Array.from(list, (m) => m.name);
const ids = (list) => Array.from(list, (m) => m.id);

test("a provider's models are offered only when the server can call them", () => {
  assert.deepEqual(names(load().MC.models), ["GPT-6 Luna", "GPT-4o mini"]);
  assert.deepEqual(names(load({ openrouter: true }).MC.models), ["GPT-6 Luna", "GPT-4o mini", "Gemma 4 31B"]);
  const all = load({ openrouter: true, cloudflare: true }).MC.models;
  assert.deepEqual(names(all), ["GPT-6 Luna", "GPT-4o mini", "Gemma 4 31B", "Gemma 4 26B", "Qwen3 30B", "GLM 4.7 Flash"]);
  // every Cloudflare model keeps its @cf/ prefix: without it Cloudflare bills the AI Gateway credit
  all.filter((m) => m.provider === "cloudflare").forEach((m) => assert.ok(m.id.startsWith("@cf/"), m.id));
});

test("models an admin added are offered for their own provider, and only when its key is set", () => {
  const site = {
    customModels: [
      { id: "gpt-4.1-mini", name: "GPT-4.1 mini", free: false, provider: "openai" },
      { id: "nvidia/x:free", name: "Nemotron", free: true },
      { id: "@cf/zai-org/glm-5.3-flash", name: "GLM 5.3 Flash", free: false, provider: "cloudflare" },
    ],
  };
  assert.deepEqual(ids(load({ site }).MC.models), ["gpt-6-luna", "gpt-4o-mini", "gpt-4.1-mini"]);
  const some = load({ site, openrouter: true, cloudflare: true }).MC.models;
  assert.deepEqual(ids(some).slice(-3), ["gpt-4.1-mini", "nvidia/x:free", "@cf/zai-org/glm-5.3-flash"]);
  assert.deepEqual(Array.from(some.slice(-3), (m) => m.group), ["OpenAI", "OpenRouter", "Cloudflare Workers AI"]);
  assert.equal(some[some.length - 2].free, true);
});

test("an OpenAI model that thinks first takes an effort, the others do not", () => {
  const site = { customModels: [{ id: "gpt-7-sol", name: "Sol", provider: "openai", reasoning: true }, { id: "gpt-4.1", name: "4.1", provider: "openai" }] };
  const { MC } = load({ site });
  MC.set("gpt-7-sol");
  assert.equal(MC.fields().reasoning_effort, "low");
  MC.set("gpt-4.1");
  assert.equal(MC.fields().max_tokens, 800);
});

test("Your models are three: what is pinned, then what was used last, then the short list", () => {
  const { MC } = load({ openrouter: true, cloudflare: true });
  assert.deepEqual(names(MC.your()), ["GPT-6 Luna", "GPT-4o mini", "Gemma 4 31B"]);
  MC.set("@cf/qwen/qwen3-30b-a3b-fp8");
  MC.set("gpt-4o-mini");
  assert.deepEqual(names(MC.your()), ["GPT-4o mini", "Qwen3 30B", "GPT-6 Luna"]);
  MC.togglePin("@cf/google/gemma-4-26b-a4b-it");
  assert.deepEqual(names(MC.your()), ["Gemma 4 26B", "GPT-4o mini", "Qwen3 30B"]);
  MC.togglePin("@cf/zai-org/glm-4.7-flash");
  assert.equal(MC.your().length, 3);
  assert.deepEqual(names(MC.your()).slice(0, 2), ["Gemma 4 26B", "GLM 4.7 Flash"]);
  // a pin is not taken away by what is used later
  MC.set("google/gemma-4-31b-it");
  assert.ok(ids(MC.your()).includes("@cf/google/gemma-4-26b-a4b-it") && ids(MC.your()).includes("@cf/zai-org/glm-4.7-flash"));
  MC.togglePin("@cf/google/gemma-4-26b-a4b-it");
  assert.equal(MC.isPinned("@cf/google/gemma-4-26b-a4b-it"), false);
});

test("the model in use is always among Your models", () => {
  const { MC } = load({ openrouter: true, cloudflare: true });
  MC.togglePin("gpt-6-luna");
  MC.togglePin("gpt-4o-mini");
  MC.togglePin("google/gemma-4-31b-it");
  MC.set("@cf/zai-org/glm-4.7-flash");
  // the three pins stay, and the model in use comes after them
  assert.deepEqual(ids(MC.your()), ["gpt-6-luna", "gpt-4o-mini", "google/gemma-4-31b-it", "@cf/zai-org/glm-4.7-flash"]);
});

test("a new visitor gets the admin's short list, and pins and last used are kept on the device", () => {
  const site = { featuredModels: ["@cf/qwen/qwen3-30b-a3b-fp8", "@cf/google/gemma-4-26b-a4b-it"] };
  const first = load({ cloudflare: true, site });
  // the model a visitor starts with is always among them, then the admin's list
  assert.deepEqual(names(first.MC.your()), ["GPT-6 Luna", "Qwen3 30B", "Gemma 4 26B"]);
  first.MC.togglePin("gpt-4o-mini");
  const again = load({ cloudflare: true, site, store: first.store });
  assert.equal(again.MC.isPinned("gpt-4o-mini"), true);
  assert.equal(names(again.MC.your())[0], "GPT-4o mini");
});

test("Other models are the rest, by provider in a fixed order", () => {
  const { MC } = load({ openrouter: true, cloudflare: true });
  const rest = MC.others();
  assert.deepEqual(Array.from(rest, (g) => g.group), ["Cloudflare Workers AI"]);
  assert.equal(rest[0].models.length, 3);
  MC.togglePin("@cf/qwen/qwen3-30b-a3b-fp8");
  MC.togglePin("@cf/zai-org/glm-4.7-flash");
  MC.togglePin("@cf/google/gemma-4-26b-a4b-it");
  const groups = Array.from(MC.others(), (g) => g.group);
  assert.deepEqual(groups, ["OpenAI", "OpenRouter"]);
  const listed = [...MC.your(), ...Array.from(MC.others()).flatMap((g) => Array.from(g.models))];
  assert.equal(new Set(ids(listed)).size, MC.models.length);
});

test("search matches the name, id, provider and tag, and every word has to match", () => {
  const { MC } = load({ openrouter: true, cloudflare: true });
  assert.deepEqual(names(MC.search("gem")), ["Gemma 4 31B", "Gemma 4 26B"]);
  assert.deepEqual(names(MC.search("QWEN")), ["Qwen3 30B"]);
  assert.deepEqual(names(MC.search("cloudflare")), ["Gemma 4 26B", "Qwen3 30B", "GLM 4.7 Flash"]);
  assert.deepEqual(names(MC.search("free cloud")), ["Gemma 4 26B", "Qwen3 30B", "GLM 4.7 Flash"]);
  assert.deepEqual(names(MC.search("gem openrouter")), ["Gemma 4 31B"]);
  assert.equal(MC.search("zzzz").length, 0);
  assert.equal(MC.search("  ").length, 0);
});

test("a model that is switched off is not offered or remembered", () => {
  const { MC } = load({ cloudflare: true, site: { disabledModels: ["@cf/qwen/qwen3-30b-a3b-fp8"] }, store: { "genie-model-pins": '["@cf/qwen/qwen3-30b-a3b-fp8"]' } });
  assert.ok(!ids(MC.models).includes("@cf/qwen/qwen3-30b-a3b-fp8"));
  assert.ok(!ids(MC.your()).includes("@cf/qwen/qwen3-30b-a3b-fp8"));
});

// ---- a choice for each mode: Chat and Agent --------------------------------------------------

const BOTH = { openrouter: true, cloudflare: true };

test("each mode starts on the admin's default for it, and the first visit takes nothing from the other mode", () => {
  const { MC } = load({ ...BOTH, site: { defaultModel: "gpt-4o-mini", genieAgentModel: "gpt-6-luna", genieAgentEffort: "medium" } });
  assert.equal(MC.get().model, "gpt-4o-mini");
  assert.equal(MC.get("agent").model, "gpt-6-luna");
  assert.equal(MC.get("agent").effort, "medium");
  assert.equal(MC.mode(), "chat");
  const r = MC.setMode("agent");
  assert.equal(r.changed, true);
  assert.equal(MC.mode(), "agent");
  assert.equal(MC.get().model, "gpt-6-luna", "the chip of Agent mode");
  assert.equal(MC.get("chat").model, "gpt-4o-mini", "the Chat model is still there");
  assert.equal(MC.setMode("agent").changed, false);
});

test("a pick is kept for its mode only, and comes back after a reload", () => {
  const first = load({ ...BOTH, site: { defaultModel: "gpt-4o-mini", genieAgentModel: "gpt-6-luna" } });
  first.MC.set("google/gemma-4-31b-it"); // Chat
  first.MC.setMode("agent");
  first.MC.set("gpt-4o-mini"); // Agent
  assert.equal(first.MC.get("chat").model, "google/gemma-4-31b-it");
  assert.equal(first.MC.get("agent").model, "gpt-4o-mini");
  const again = load({ ...BOTH, site: { defaultModel: "gpt-4o-mini", genieAgentModel: "gpt-6-luna" }, store: first.store });
  assert.equal(again.MC.get("chat").model, "google/gemma-4-31b-it");
  assert.equal(again.MC.get("agent").model, "gpt-4o-mini");
  assert.equal(again.MC.get().model, "google/gemma-4-31b-it", "it opens in Chat");
});

test("a model that is not allowed for Agent mode cannot be chosen there, and the Chat model is not changed by trying", () => {
  const site = { defaultModel: "gpt-4o-mini", genieAgentModel: "gpt-6-luna", genieAgentModels: ["gpt-6-luna"] };
  const { MC } = load({ ...BOTH, site });
  assert.equal(MC.agentOk("gpt-6-luna"), true);
  assert.equal(MC.agentOk("gpt-4o-mini"), false);
  assert.equal(MC.agentOk("not-a-model"), false);
  assert.deepEqual(ids(MC.agentModels), ["gpt-6-luna"]);
  assert.ok(MC.models.length > 1, "Chat still lists every model");
  MC.setMode("agent");
  MC.set("gpt-4o-mini");
  assert.equal(MC.get().model, "gpt-6-luna", "ignored in Agent mode");
  const f = MC.fields({}, "agent");
  assert.equal(f.model, "gpt-6-luna");
  MC.setMode("chat");
  MC.set("gpt-4o-mini");
  assert.equal(MC.get().model, "gpt-4o-mini", "allowed in Chat");
  assert.equal(MC.fields({}, "agent").model, "gpt-6-luna", "the fields of Agent mode, while Chat is on (a task goes on in another tab)");
});

test("with no list every model may be used in Agent mode, and a saved pick that was taken off falls back with a note", () => {
  assert.equal(load({ ...BOTH, site: {} }).MC.agentOk("gpt-4o-mini"), true);
  // the visitor had picked Qwen for Agent; the admin has since allowed only Luna
  const site = { genieAgentModel: "gpt-6-luna", genieAgentModels: ["gpt-6-luna"] };
  const store = { "genie-model-agent": JSON.stringify({ model: "@cf/qwen/qwen3-30b-a3b-fp8", effort: "low" }) };
  const { MC } = load({ ...BOTH, site, store });
  const r = MC.setMode("agent");
  assert.match(r.note, /Qwen3 30B is no longer supported for Agent mode\. Using GPT-6 Luna\./);
  assert.equal(MC.get().model, "gpt-6-luna");
});

test("pressing Agent while the Chat model is not supported there says so, and says the Chat model is unchanged", () => {
  const site = { defaultModel: "@cf/qwen/qwen3-30b-a3b-fp8", genieAgentModel: "gpt-6-luna", genieAgentModels: ["gpt-6-luna"] };
  const { MC } = load({ ...BOTH, site });
  const r = MC.setMode("agent");
  assert.equal(r.note, "Qwen3 30B is not supported for Agent mode. Using GPT-6 Luna. Your Chat model is unchanged.");
  assert.equal(MC.setMode("chat").note, "");
});

test("the fallback of a model that is not answering stays inside the models of the mode", () => {
  const site = { genieAgentModel: "gpt-6-luna", genieAgentModels: ["gpt-6-luna", "gpt-4o-mini"] };
  const { MC } = load({ ...BOTH, site });
  MC.setMode("agent");
  assert.equal(MC.fallback("gpt-6-luna").id, "gpt-4o-mini");
  assert.equal(MC.fallback("gpt-4o-mini").id, "gpt-6-luna");
});

// ---- practice questions ----------------------------------------------------------------------

test("a practice question is made with the Agent model, not with the visitor's Chat or Agent pick, and a change in the dialog is for the page only", () => {
  const site = { defaultModel: "gpt-4o-mini", genieAgentModel: "gpt-6-luna", genieAgentModels: ["gpt-6-luna", "google/gemma-4-31b-it"], genieAgentEffort: "medium" };
  const { MC, store } = load({ ...BOTH, site });
  MC.set("gpt-4o-mini"); // the Chat pick
  assert.equal(MC.questionFields({ extraTokens: 3000 }).model, "gpt-6-luna");
  assert.equal(MC.questionFields({ extraTokens: 3000 }).reasoning_effort, "medium");
  assert.deepEqual(ids(MC.questionModels), ["gpt-6-luna", "google/gemma-4-31b-it"], "only the models allowed for Agent mode are offered");
  // another model for this page's questions: allowed ones only; the picks of the panel are not touched
  MC.setQuestion("gpt-4o-mini");
  assert.equal(MC.getQuestion().model, "gpt-6-luna", "a model that is not allowed is ignored");
  MC.setQuestion("google/gemma-4-31b-it");
  assert.equal(MC.questionFields({}).model, "google/gemma-4-31b-it");
  assert.equal(MC.get("chat").model, "gpt-4o-mini");
  assert.equal(MC.get("agent").model, "gpt-6-luna");
  assert.ok(!("genie-model-question" in store) && !Object.keys(store).some((k) => /question/.test(k)), "nothing is saved for the dialog");
  // the next page starts on the Agent model again
  assert.equal(load({ ...BOTH, site, store }).MC.getQuestion().model, "gpt-6-luna");
});

test("a model kept for signed-in users is listed, and a guest may not choose it", () => {
  const site = { customModels: [{ id: "gpt-7-sol", name: "Sol", provider: "openai", guests: false }, { id: "gpt-4.1", name: "4.1", provider: "openai", guests: true }] };
  const guest = load({ site }).MC;
  assert.ok(guest.models.some((m) => m.id === "gpt-7-sol"), "it is still listed");
  assert.equal(guest.guestOk("gpt-7-sol"), false);
  assert.equal(guest.guestOk("gpt-4.1"), true);
  assert.equal(guest.guestOk("gpt-4o-mini"), true);
  assert.equal(load({ site, signedIn: true }).MC.guestOk("gpt-7-sol"), true);
});

test("a built-in model kept for signed-in users is closed to a guest, and a guest starts on one they may use", () => {
  const site = { defaultModel: "gpt-6-luna", genieGuestBlocked: ["gpt-6-luna"] };
  const guest = load({ site }).MC;
  assert.equal(guest.guestOk("gpt-6-luna"), false);
  assert.notEqual(guest.get("chat").model, "gpt-6-luna");
  const user = load({ site, signedIn: true }).MC;
  assert.equal(user.guestOk("gpt-6-luna"), true);
  assert.equal(user.get("chat").model, "gpt-6-luna");
});
