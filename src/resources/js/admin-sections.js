/*
 * The sections of Site settings (admin.html, admin.js: viewSite). Plain code with no page in it, so that node runs it in
 * src/js/test/admin-sections.test.mjs: which card is in which section, the address of a section and of one setting, and
 * the search over the settings. admin.js draws it.
 *
 * It is loaded before admin.js and puts itself on window.AdminSections; node reads it with require().
 */
(function (root) {
  'use strict';

  // The groups of the list on the left, and the sections of each, in the order they are shown. `cards` are the titles of the
  // cards that were one long page; a section with `tabs` shows one tab at a time.
  var GROUPS = [
    { id: 'site', title: 'Site' },
    { id: 'genie', title: 'Genie' },
    { id: 'access', title: 'Access' }
  ];

  var SECTIONS = [
    { id: 'general', group: 'site', title: 'General', blurb: 'What every visitor sees: the colour, a banner, and maintenance mode.',
      cards: ['Look', 'Announcement', 'Maintenance mode'] },
    { id: 'workspaces', group: 'site', title: 'Workspaces', blurb: 'Files, snapshots, the Preview pane, the languages, and the database.',
      cards: ['Files', 'Workspace snapshots', 'Practice', 'Preview', 'Languages', 'Database capacity'] },
    { id: 'genie-models', group: 'genie', title: 'Models and prices', blurb: 'The models visitors can pick, the ones you add, and what they cost.',
      tabs: [
        { id: 'models', title: 'Models', cards: ['Default models', 'Genie'] },
        { id: 'added', title: 'Added models', cards: ['Added models'] },
        { id: 'prices', title: 'Prices and cost', cards: ['Genie prices and cost'] }
      ] },
    { id: 'genie-limits', group: 'genie', title: 'Limits and context', blurb: 'What Genie is told about the page and how long its answers may be.',
      cards: ['Genie limits'] },
    { id: 'genie-agent', group: 'genie', title: 'Agent', blurb: 'Agent mode: its steps, the risky actions and the tools.',
      cards: ['Agent mode'] },
    { id: 'genie-chats', group: 'genie', title: 'Chats and credits', blurb: 'Saved chats, the summary of long chats, and credits and plans.',
      cards: ['Genie chat history', 'Usage record and credits'] },
    { id: 'access', group: 'access', title: 'API keys and admins', blurb: 'The keys the site uses and who may open this dashboard.',
      cards: ['API keys', 'Admin accounts'] }
  ];

  var DEFAULT_SECTION = 'general';

  function sectionById(id) {
    for (var i = 0; i < SECTIONS.length; i++) if (SECTIONS[i].id === id) return SECTIONS[i];
    return null;
  }

  // the cards of a section, over all its tabs
  function cardsOf(section) {
    if (section.tabs) return section.tabs.reduce(function (all, t) { return all.concat(t.cards); }, []);
    return section.cards.slice();
  }

  // where a card is: {section, tab}; null for a card that no section names
  function placeOfCard(title) {
    for (var i = 0; i < SECTIONS.length; i++) {
      var s = SECTIONS[i];
      if (s.tabs) {
        for (var j = 0; j < s.tabs.length; j++) if (s.tabs[j].cards.indexOf(title) >= 0) return { section: s.id, tab: s.tabs[j].id };
      } else if (s.cards.indexOf(title) >= 0) return { section: s.id, tab: '' };
    }
    return null;
  }

  // "Models and prices" -> "models-and-prices": the address of a setting
  function slug(text) {
    return String(text || '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 60);
  }

  // #site/genie-limits/summary-tokens -> {view: 'site', section: 'genie-limits', anchor: 'summary-tokens'}. A section that is not
  // known is the default one; an anchor is kept as it is written (it may belong to a setting that is not drawn yet).
  function parseHash(hash) {
    var parts = String(hash || '').replace(/^#/, '').split('/');
    var view = parts[0] || '';
    var section = parts[1] || '';
    return { view: view, section: sectionById(section) ? section : (section ? DEFAULT_SECTION : ''), anchor: parts[2] || '' };
  }

  function hashFor(section, anchor) {
    return '#site/' + section + (anchor ? '/' + anchor : '');
  }

  // Search over the settings. An entry is {section, tab, card, label, help, anchor}; every word of the query has to be in
  // the label, the help, the card or the section; a word at the start of a word of the label counts most.
  function search(entries, query, limit) {
    var words = String(query || '').toLowerCase().split(/\s+/).filter(Boolean);
    if (!words.length) return [];
    var scored = [];
    entries.forEach(function (e) {
      var label = String(e.label || '').toLowerCase();
      var help = String(e.help || '').toLowerCase();
      var where = (String(e.card || '') + ' ' + String(e.sectionTitle || '')).toLowerCase();
      var score = 0;
      for (var i = 0; i < words.length; i++) {
        var w = words[i];
        var inLabel = label.indexOf(w);
        var inHelp = help.indexOf(w);
        var inWhere = where.indexOf(w);
        if (inLabel < 0 && inHelp < 0 && inWhere < 0) return;
        if (inLabel >= 0) score += (inLabel === 0 || label.charAt(inLabel - 1) === ' ') ? 10 : 6;
        else if (inHelp >= 0) score += 3;
        else score += 1;
      }
      scored.push({ e: e, score: score });
    });
    scored.sort(function (a, b) { return b.score - a.score; });
    return scored.slice(0, limit || 12).map(function (s) { return s.e; });
  }

  // How the values of the controls of a section differ from what they were when the form was drawn: init and now are
  // {key: value}; a key that is only in one of them (a model row that was added or removed) counts as a change.
  function changeCount(init, now) {
    var seen = {};
    var n = 0;
    Object.keys(init).forEach(function (k) {
      seen[k] = true;
      if (!(k in now) || now[k] !== init[k]) n++;
    });
    Object.keys(now).forEach(function (k) { if (!seen[k]) n++; });
    return n;
  }

  // The words under the Save button: "2 changes: 1 in General, 1 in Models and prices".
  function changeSummary(counts) {
    var parts = [];
    var total = 0;
    SECTIONS.forEach(function (s) {
      var n = counts[s.id] || 0;
      if (n) { parts.push(n + ' in ' + s.title); total += n; }
    });
    if (!total) return '';
    return total + (total === 1 ? ' change' : ' changes') + ': ' + parts.join(', ');
  }

  // Whether a model is a small one, from its id; the same check as the page's (chat-widget agent-protocol.ts: isSmallModel) and the server's
  // (agent_models.go: isSmallModelID): a size in the id decides (40B or less is small), else mini, flash, lite, nano, tiny, small, free, auto
  // or router. The card of the models for Agent mode starts with the ones that are not small ticked.
  function isSmallModel(id) {
    var s = String(id || '').toLowerCase();
    var size = s.match(/(^|[^a-z0-9.])(\d{1,3})b([^a-z0-9]|$)/);
    if (size) return Number(size[2]) <= 40;
    return /(^|[^a-z0-9])(mini|flash|lite|nano|tiny|small|free|auto|router)([^a-z0-9]|$)/.test(s);
  }

  var api = { isSmallModel: isSmallModel, GROUPS: GROUPS, SECTIONS: SECTIONS, DEFAULT_SECTION: DEFAULT_SECTION, sectionById: sectionById, cardsOf: cardsOf, placeOfCard: placeOfCard,
    slug: slug, parseHash: parseHash, hashFor: hashFor, search: search, changeCount: changeCount, changeSummary: changeSummary };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  else root.AdminSections = api;
})(typeof window !== 'undefined' ? window : this);
