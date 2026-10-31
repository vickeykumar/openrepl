// The model and effort choice, shared by Genie (the chip in its panel, chat
// widget) and the New question dialog (js/common.js). Loaded before both.
//
// The built-in models and the ones an admin added and switched on are offered, and
// only these: server/chatmodels.go and custom_models.go hold the same lists and
// send nothing else on (a request for any other model is answered by the default). Two come from OpenAI; Gemma 4 31B comes through OpenRouter and
// is listed only when the server has an OpenRouter key (config.js sets
// openrouter_enabled). The choice is kept on this device, and the default is
// Luna with a Low effort. The picker shows "Your models" (pinned, then last used) and the rest under
// "Other models", with search; the pins and the last used are kept on this device. A page that wants a choice of its own (the blog
// editor) sets window.MODEL_CHOICE_KEY before loading this file.

(function () {
  "use strict";

  var ALL = [
    {
      id: "gpt-6-luna",
      name: "GPT-6 Luna",
      short: "Luna",
      group: "OpenAI",
      provider: "openai",
      tag: "Thinks first",
      desc: "Newer. Thinks before it answers, so it handles tricky bugs better.",
      reasoning: true,
    },
    {
      id: "gpt-4o-mini",
      name: "GPT-4o mini",
      short: "4o mini",
      group: "OpenAI",
      provider: "openai",
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
      provider: "openrouter",
      tag: "",
      desc: "Google's open model, run through OpenRouter. Answers without a thinking step.",
      reasoning: false,
    });
  }

  // Cloudflare Workers AI, listed only when the server has its token and account id
  // (config.js sets cloudflare_enabled). These are on the free plan: the site shares a daily
  // allowance between everyone, so a model can stop answering until it resets.
  var CF_LIMIT = "A daily allowance the whole site shares. It can run out and come back the next day.";
  if (window.cloudflare_enabled === true) {
    ALL.push(
      {
        id: "@cf/google/gemma-4-26b-a4b-it",
        name: "Gemma 4 26B",
        short: "Gemma 26B",
        group: "Cloudflare Workers AI",
        provider: "cloudflare",
        free: true,
        tag: "Free",
        desc: "Google's open model on Cloudflare. Good for everyday questions and short code.",
        limit: CF_LIMIT,
        reasoning: false,
      },
      {
        id: "@cf/qwen/qwen3-30b-a3b-fp8",
        name: "Qwen3 30B",
        short: "Qwen3 30B",
        group: "Cloudflare Workers AI",
        provider: "cloudflare",
        free: true,
        tag: "Free",
        desc: "A coding-minded open model on Cloudflare. It may think a little before it answers.",
        limit: CF_LIMIT,
        reasoning: false,
      },
      {
        id: "@cf/zai-org/glm-4.7-flash",
        name: "GLM 4.7 Flash",
        short: "GLM Flash",
        group: "Cloudflare Workers AI",
        provider: "cloudflare",
        free: true,
        tag: "Free",
        desc: "A fast open model on Cloudflare with a long memory, good for long files.",
        limit: CF_LIMIT,
        reasoning: false,
      }
    );
  }

  // Models an admin added in the dashboard and switched on come with the page
  // (settings.js: site_settings.customModels, the ones that are on), from any provider. One
  // whose provider has no key on the server is left out, as the built-in ones are.
  var GROUP_OF = { openai: "OpenAI", openrouter: "OpenRouter", cloudflare: "Cloudflare Workers AI" };
  ((window.site_settings || {}).customModels || []).forEach(function (c) {
    var provider = c.provider || "openrouter";
    if (provider === "openrouter" && window.openrouter_enabled !== true) return;
    if (provider === "cloudflare" && window.cloudflare_enabled !== true) return;
    var how = c.free
      ? provider === "openrouter"
        ? "A free model, run through OpenRouter. Its provider may keep what you send, and it can be busy."
        : "A free model on " + GROUP_OF[provider] + "."
      : "Run through " + GROUP_OF[provider] + ".";
    ALL.push({
      id: c.id,
      name: c.name,
      short: c.name.length > 16 ? c.name.slice(0, 15) + "…" : c.name,
      group: GROUP_OF[provider] || "OpenRouter",
      provider: provider,
      free: !!c.free,
      tag: c.free ? "Free" : "",
      desc: how,
      limit: c.free && provider !== "openai" ? CF_LIMIT : "",
      reasoning: provider === "openai" && !!c.reasoning,
    });
  });

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

  var KEY = window.MODEL_CHOICE_KEY || "genie-model";
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

  // ---- the short list ("Your models"), pins, and search --------------------------------------
  // Your models are the ones the visitor pinned, then the ones used last, three in all (more
  // when more are pinned). A visitor with none starts with the admin's short list
  // (site_settings.featuredModels), else the default model and the first others.
  var MAX_RECENT = 10;
  var YOUR_COUNT = 3;
  var GROUP_ORDER = ["OpenAI", "OpenRouter", "Cloudflare Workers AI"];

  function readList(suffix) {
    try {
      var v = JSON.parse(localStorage.getItem(KEY + suffix) || "[]");
      return Array.isArray(v)
        ? v.filter(function (id) {
            return typeof id === "string" && find(MODELS, id);
          })
        : [];
    } catch (e) {
      return [];
    }
  }
  function writeList(suffix, list) {
    try {
      localStorage.setItem(KEY + suffix, JSON.stringify(list));
    } catch (e) {
      // kept until the page closes only
    }
  }
  var pinned = readList("-pins");
  var recent = readList("-recent");

  function pins() {
    return pinned.slice();
  }
  function isPinned(id) {
    return pinned.indexOf(id) >= 0;
  }
  function togglePin(id) {
    if (!find(MODELS, id)) return;
    pinned = isPinned(id)
      ? pinned.filter(function (p) {
          return p !== id;
        })
      : pinned.concat([id]);
    writeList("-pins", pinned);
    changed();
  }
  function noteUsed(id) {
    recent = [id].concat(
      recent.filter(function (r) {
        return r !== id;
      })
    ).slice(0, MAX_RECENT);
    writeList("-recent", recent);
  }

  function featured() {
    var ids = (site.featuredModels || []).filter(function (id) {
      return find(MODELS, id);
    });
    var out = [];
    function add(id) {
      if (id && out.indexOf(id) < 0 && find(MODELS, id)) out.push(id);
    }
    ids.forEach(add);
    add(DEFAULT.model);
    MODELS.forEach(function (m) {
      add(m.id);
    });
    return out;
  }

  // the models of "Your models": pinned, then recent, then the short list, until there are three
  function your() {
    var out = [];
    function add(id) {
      if (id && out.indexOf(id) < 0 && find(MODELS, id)) out.push(id);
    }
    pinned.forEach(add);
    var want = Math.max(YOUR_COUNT, pinned.length);
    recent.forEach(function (id) {
      if (out.length < want) add(id);
    });
    // the model in use is always shown: in the place of the last one that is not pinned, or after
    // the pinned ones when they fill the list (a pin is never taken away)
    if (out.indexOf(choice.model) < 0 && find(MODELS, choice.model)) {
      if (out.length < want) {
        out.push(choice.model);
      } else {
        var last = -1;
        out.forEach(function (id, i) {
          if (pinned.indexOf(id) < 0) last = i;
        });
        if (last >= 0) out[last] = choice.model;
        else out.push(choice.model);
      }
    }
    featured().forEach(function (id) {
      if (out.length < want) add(id);
    });
    return out.map(function (id) {
      return find(MODELS, id);
    });
  }

  // every other model, by provider in a fixed order
  function others() {
    var yours = your().map(function (m) {
      return m.id;
    });
    var rest = MODELS.filter(function (m) {
      return yours.indexOf(m.id) < 0;
    });
    var groups = [];
    GROUP_ORDER.concat(
      rest
        .map(function (m) {
          return m.group;
        })
        .filter(function (g) {
          return GROUP_ORDER.indexOf(g) < 0;
        })
    ).forEach(function (g) {
      if (groups.some(function (x) {
        return x.group === g;
      })) return;
      var models = rest.filter(function (m) {
        return m.group === g;
      });
      if (models.length) groups.push({ group: g, models: models });
    });
    return groups;
  }

  // Search over the name, the id, the provider and the tag; every word has to match.
  function search(query) {
    var words = String(query || "")
      .toLowerCase()
      .split(/\s+/)
      .filter(Boolean);
    if (!words.length) return [];
    return MODELS.filter(function (m) {
      var hay = [m.name, m.id, m.group, m.tag, m.free ? "free" : ""].join(" ").toLowerCase();
      return words.every(function (w) {
        return hay.indexOf(w) >= 0;
      });
    });
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
    if (model && find(MODELS, model)) {
      choice.model = model;
      noteUsed(model);
    }
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
    your: your,
    others: others,
    search: search,
    pins: pins,
    isPinned: isPinned,
    togglePin: togglePin,
    onChange: function (fn) {
      listeners.push(fn);
    },
  };
})();
