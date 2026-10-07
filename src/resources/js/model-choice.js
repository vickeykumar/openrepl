// The model and effort choice, shared by Genie (the chip in its panel, chat
// widget) and the New question dialog (js/common.js). Loaded before both.
//
// Three models are offered, and only these: server/chatmodels.go holds the same
// lists and sends nothing else on (a request for any other model is answered by
// GPT-4o mini). Two come from OpenAI; Gemma 4 31B comes through OpenRouter and
// is listed only when the server has an OpenRouter key (config.js sets
// openrouter_enabled). The choice is kept on this device, and the default is
// Luna with a Low effort.

(function () {
  "use strict";

  var ALL = [
    {
      id: "gpt-6-luna",
      name: "GPT-6 Luna",
      short: "Luna",
      group: "OpenAI",
      tag: "Thinks first",
      desc: "Newer. Thinks before it answers, so it handles tricky bugs better.",
      reasoning: true,
    },
    {
      id: "gpt-4o-mini",
      name: "GPT-4o mini",
      short: "4o mini",
      group: "OpenAI",
      tag: "Fast",
      desc: "Answers instantly. Good for quick questions and short snippets.",
      reasoning: false,
    },
  ];

  if (window.openrouter_enabled === true) {
    ALL.push({
      id: "google/gemma-4-31b-it",
      name: "Gemma 4 31B",
      short: "Gemma 31B",
      group: "OpenRouter",
      tag: "",
      desc: "Google's open model, run through OpenRouter. Answers without a thinking step.",
      reasoning: false,
    });
  }

  // An admin can switch models off and choose the one visitors start with
  // (settings.js: site_settings.disabledModels, defaultModel). The server refuses
  // a model that is off, so the page does not offer it. If that would leave
  // nothing to offer, everything stays listed and the server decides.
  var site = window.site_settings || {};
  var off = site.disabledModels || [];
  var MODELS = ALL.filter(function (m) {
    return off.indexOf(m.id) < 0;
  });
  if (!MODELS.length) MODELS = ALL.slice();

  // tokens is the answer budget. Luna's thinking counts against it, so it grows
  // with the effort.
  var EFFORTS = [
    { id: "none", label: "Off", tokens: 1000, note: "Answers right away. Best for quick questions." },
    { id: "low", label: "Low", tokens: 2000, note: "A short think first. A good default for debugging." },
    { id: "medium", label: "Medium", tokens: 3000, note: "Thinks longer. Better on tricky bugs, takes a few seconds more." },
    { id: "high", label: "High", tokens: 4000, note: "Thinks the longest. For hard problems; the slowest to answer." },
  ];

  var KEY = "genie-model";
  function find(list, id) {
    for (var i = 0; i < list.length; i++) {
      if (list[i].id === id) return list[i];
    }
    return null;
  }

  var DEFAULT = {
    model: (find(MODELS, site.defaultModel) || MODELS[0]).id,
    effort: "low",
  };

  // A model to offer instead of one that is not answering: the first other
  // OpenAI model that is on, else any other.
  function fallback(exceptId) {
    var others = MODELS.filter(function (m) {
      return m.id !== exceptId;
    });
    return (
      others.filter(function (m) {
        return m.group === "OpenAI";
      })[0] ||
      others[0] ||
      null
    );
  }

  function load() {
    var choice = { model: DEFAULT.model, effort: DEFAULT.effort };
    try {
      var saved = JSON.parse(localStorage.getItem(KEY) || "null");
      if (saved && find(MODELS, saved.model)) choice.model = saved.model;
      if (saved && find(EFFORTS, saved.effort)) choice.effort = saved.effort;
    } catch (e) {
      // storage blocked or not JSON: keep the default
    }
    return choice;
  }

  var choice = load();
  var listeners = [];

  function save() {
    try {
      localStorage.setItem(KEY, JSON.stringify(choice));
    } catch (e) {
      // the choice then lasts until the page closes
    }
  }

  function get() {
    return { model: choice.model, effort: choice.effort };
  }

  function changed() {
    listeners.slice().forEach(function (fn) {
      try {
        fn(get());
      } catch (e) {
        console.error("ModelChoice listener:", e);
      }
    });
  }

  // set("gpt-4o-mini") or set(undefined, "high"): a value that is not offered is ignored.
  function set(model, effort) {
    if (model && find(MODELS, model)) choice.model = model;
    if (effort && find(EFFORTS, effort)) choice.effort = effort;
    save();
    changed();
  }

  // The fields of a chat request for the chosen model, which differ: Luna
  // reasons, so it takes an effort and an answer budget and no temperature;
  // 4o mini takes a temperature and max_tokens. Options: extraTokens (more room
  // than a chat answer needs), maxTokens and temperature (4o mini).
  function fields(opts) {
    opts = opts || {};
    var model = find(MODELS, choice.model) || MODELS[0];
    if (model.reasoning) {
      var effort = find(EFFORTS, choice.effort) || find(EFFORTS, DEFAULT.effort);
      return {
        model: model.id,
        reasoning_effort: effort.id,
        max_completion_tokens: effort.tokens + (opts.extraTokens || 0),
      };
    }
    return {
      model: model.id,
      temperature: opts.temperature !== undefined ? opts.temperature : 0.5,
      max_tokens: opts.maxTokens !== undefined ? opts.maxTokens : 800 + (opts.extraTokens || 0),
    };
  }

  // Another tab changed it.
  window.addEventListener("storage", function (ev) {
    if (ev.key === KEY) {
      choice = load();
      changed();
    }
  });

  window.ModelChoice = {
    models: MODELS,
    efforts: EFFORTS,
    get: get,
    set: set,
    fields: fields,
    fallback: fallback,
    onChange: function (fn) {
      listeners.push(fn);
    },
  };
})();
