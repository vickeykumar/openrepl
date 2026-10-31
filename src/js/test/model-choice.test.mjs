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
function load({ openrouter = false, cloudflare = false, site = {}, store = {} } = {}) {
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
  const ctx = { window, localStorage, console, JSON, Array, Object };
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
