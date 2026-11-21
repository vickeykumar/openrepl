/*
 * Integrations: the servers Genie can be connected to, which an admin adds, tests, sets up and publishes (server/genie_integrations*.go, docs/lld/19 section 25).
 * It is a card of Site settings, under Genie (admin-site.js loads this file when the section is opened). The screens are those of the canvas
 * claude.ai/artifact/4SvDQfUd4FR6v8Xv9mi6AF: board 4 the list, 5 add a server, 6 the test, 7 the tools and publish, and the requests for a server (board 9).
 * It puts its view on window.AdminIntegrations; the helpers it needs from admin.js are on window.AdminKit. Same rules as admin.js: everything from the
 * server is put on the page with textContent only, and no secret is ever shown (the server sends only whether one is set).
 */
(function () {
  'use strict';

  var K = window.AdminKit;
  var api = K.api, ask = K.ask, fail = K.fail, h = K.h, toast = K.toast;

  var STATUS = { draft: 'Draft', tested: 'Tested', published: 'Published', off: 'Switched off' };
  var STATUS_CLASS = { draft: '', tested: 'info', published: 'ok', off: 'fail' };
  var LEVELS = ['read', 'write', 'restricted'];

  function words(text) { return String(text || '').split(/[,\n;]+/).map(function (x) { return x.trim(); }).filter(Boolean); }
  function addrText(u) { return String(u || '').replace(/^https?:\/\//, '').replace(/\/$/, ''); }
  function two(n) { return (n < 10 ? '0' : '') + n; }
  // "today 08:40", "yesterday", "3 days ago", "3 weeks ago", "never"
  function ago(iso) {
    if (!iso) return 'never';
    var d = new Date(iso);
    if (isNaN(d.getTime())) return String(iso);
    var now = new Date(), days = Math.floor((new Date(now.getFullYear(), now.getMonth(), now.getDate()) - new Date(d.getFullYear(), d.getMonth(), d.getDate())) / 86400000);
    if (days <= 0) return 'today ' + two(d.getHours()) + ':' + two(d.getMinutes());
    if (days === 1) return 'yesterday';
    if (days < 14) return days + ' days ago';
    return Math.floor(days / 7) + ' weeks ago';
  }
  function lastTestText(i) {
    var lt = i.lastTest;
    if (!lt) return 'never';
    var changed = (i.tools || []).filter(function (t) { return t.changed; }).length;
    return ago(lt.at) + ' · ' + (changed ? changed + ' changed' : lt.ok ? 'ok' : lt.needsAuth ? 'needs a credential' : 'failed');
  }
  function signInText(i) {
    var a = i.auth || {};
    if (a.mode === 'oauth') return a.auto ? 'sign-in with ' + i.title : 'OAuth';
    return a.mode === 'none' ? 'no sign-in' : 'token';
  }
  function peopleText(i) {
    if (i.status === 'published') return ((i.usage && i.usage.enabled) || 0) + ' enabled · ' + ((i.usage && i.usage.connected) || 0) + ' connected';
    if (i.status === 'off') return 'hidden from users';
    return i.fromRequest ? 'from a request' : 'not published';
  }
  function toolsOnText(i) {
    var n = (i.tools || []).length;
    return n ? ((i.toolsOn || 0) + ' of ' + n) : 'not tested';
  }
  function firstName(email) {
    var l = String(email || '').split('@')[0].split(/[._-]/)[0];
    return l ? l.charAt(0).toUpperCase() + l.slice(1) : 'this person';
  }
  function copy(o) { return JSON.parse(JSON.stringify(o)); }
  function btn(text, cls, onclick) { return h('button', { class: 'btn' + (cls ? ' ' + cls : ''), type: 'button', text: text, onclick: onclick }); }
  function field(label, control, hint) { return h('div', null, h('label', { class: 'int-lb', text: label }), control, hint ? h('p', { class: 'int-hint', text: hint }) : null); }
  function wide(x) { x.style.width = '100%'; return x; }

  // the logo of a server: the small image, or the first letter of its name in a square
  function logoTile(i, size) {
    var t = h('span', { class: 'int-logo', 'aria-hidden': 'true' });
    if (size) { t.style.width = t.style.height = size + 'px'; }
    if (i.logo && /^https:\/\//.test(i.logo)) t.appendChild(h('img', { src: i.logo, alt: '' }));
    else t.textContent = (String(i.title || '?').trim().charAt(0) || '?').toUpperCase();
    return t;
  }

  function make(opts) {
    opts = opts || {};
    var root = h('div'), body = h('div');
    var st = { tab: 'servers', list: [], reqs: [], redirect: '', canStore: true, open: '', screen: 'setup', busy: false, usage: { connected: [], calls: [] }, live: {}, sel: '' };
    var w = null; // the working copy of the server that is open

    root.appendChild(body);
    function find(id) { return st.list.filter(function (i) { return i.id === id; })[0]; }

    function load() {
      return Promise.all([api.get('admin/integrations'), api.get('admin/connection-requests'), api.get('admin/connections').catch(function () { return { connected: [], calls: [] }; })]).then(function (r) {
        st.list = r[0].integrations || [];
        st.redirect = r[0].redirect || '';
        st.canStore = r[0].canStore !== false;
        st.reqs = (r[1].requests || []).filter(function (q) { return q.kind === 'server'; });
        st.usage = { connected: r[2].connected || [], calls: r[2].calls || [] };
        if (st.open && st.open !== '+' && !w) w = copy(find(st.open) || {});
        draw();
      }, fail);
    }

    // an op of the route: the answer holds the changed integration, which replaces the one in the list
    function act(req, okMsg) {
      if (st.busy) return Promise.resolve(null);
      st.busy = true;
      return api.post('admin/integrations', req).then(function (res) {
        st.busy = false;
        if (res.integration) {
          var k = st.list.map(function (i) { return i.id; }).indexOf(res.integration.id);
          if (k >= 0) st.list[k] = res.integration; else st.list.push(res.integration);
          if (st.open === '+') st.open = res.integration.id;
          if (st.open === res.integration.id) w = Object.assign(copy(res.integration), keepEdits(w, res.integration));
        }
        if (res.live && res.integration) st.live[res.integration.id] = res.live;
        if (res.deleted) { st.list = st.list.filter(function (i) { return i.id !== res.deleted; }); st.open = ''; w = null; }
        if (okMsg) toast(okMsg);
        draw();
        return res;
      }, function (e) { st.busy = false; fail(e); draw(); return null; });
    }
    // what the admin typed on the screen but did not save yet stays when an op returns (the tools and the audience are on the next screen)
    function keepEdits(old, fresh) { return old && old.id === fresh.id ? { audience: old.audience, limits: old.limits } : {}; }

    // ---- the top of the card: the tabs, and the buttons of the list
    function draw() {
      body.textContent = '';
      var waiting = st.reqs.filter(function (q) { return q.status === 'waiting'; }).length;
      var buttons = h('span', { class: 'actions' },
        h('a', { class: 'btn', href: opts.switchesHref || '#site', text: 'Switches' }),
        btn('+ Add a server', 'primary', function () { st.tab = 'servers'; st.open = '+'; st.screen = 'setup'; w = blank(); draw(); }));
      var tabs = h('div', { class: 'int-tabs', role: 'tablist' }, [['servers', 'Servers ' + st.list.length], ['requests', 'Requests' + (waiting ? ' ' + waiting : '')], ['usage', 'Usage']].map(function (t) {
        return h('button', { type: 'button', class: 'int-tab', role: 'tab', 'aria-selected': st.tab === t[0] ? 'true' : 'false', text: t[1],
          onclick: function () { st.tab = t[0]; st.open = ''; w = null; draw(); } });
      }));
      if (!st.open || st.tab !== 'servers') {
        body.appendChild(h('div', { class: 'int-top' }, h('span'), buttons));
        body.appendChild(tabs);
      }
      if (st.tab === 'requests') body.appendChild(drawRequests());
      else if (st.tab === 'usage') body.appendChild(drawUsage());
      else if (st.open) body.appendChild(drawEditor());
      else body.appendChild(drawList());
    }
    function blank() {
      return { title: '', url: '', keywords: [], description: '', help: '', logo: '', status: 'draft', auth: { mode: 'token' }, audience: { everyone: false, people: [] }, limits: {}, tools: [], secrets: {} };
    }

    // ---- board 4: the list
    function drawList() {
      var wrap = h('div');
      st.list.forEach(function (i) {
        var changed = (i.tools || []).filter(function (t) { return t.changed; }).length, nw = (i.tools || []).filter(function (t) { return t['new']; }).length;
        if (!changed && !nw) return;
        var text = ': ' + (changed ? changed + (changed === 1 ? ' tool changed since you approved it. It is' : ' tools changed since you approved them. They are') + ' off until you look.' : '') +
          (nw ? (changed ? ' ' : '') + nw + (nw === 1 ? ' new tool was found and is off.' : ' new tools were found and are off.') : '');
        wrap.appendChild(h('div', { class: 'int-banner warn' }, h('span', null, h('b', { text: i.title }), document.createTextNode(text)),
          h('button', { class: 'int-link', type: 'button', text: 'Review', onclick: function () { st.open = i.id; st.screen = 'publish'; w = copy(i); draw(); } })));
      });
      if (!st.list.length) wrap.appendChild(h('p', { class: 'sub', text: 'Nothing here yet. Add a server with its name and address, then test it.' }));
      else {
        var rows = st.list.map(function (i) {
          return h('tr', null,
            h('td', null, h('div', { class: 'int-who' }, logoTile(i), h('div', null, h('b', { text: i.title }), h('div', { class: 'sub', style: 'margin:0', text: addrText(i.url) + ' · ' + signInText(i) })))),
            h('td', null, h('span', { class: 'pill ' + (STATUS_CLASS[i.status] || ''), text: STATUS[i.status] || i.status })),
            h('td', { text: toolsOnText(i) }), h('td', { text: peopleText(i) }), h('td', { text: lastTestText(i) }),
            h('td', null, h('button', { class: 'int-link', type: 'button', text: 'Edit', onclick: function () { st.open = i.id; st.screen = i.lastTest ? 'test' : 'setup'; w = copy(i); draw(); } })));
        });
        wrap.appendChild(h('div', { class: 'table-wrap' }, h('table', null,
          h('thead', null, h('tr', null, ['Server', 'Status', 'Tools on', 'People', 'Last test', ''].map(function (t) { return h('th', { scope: 'col', text: t }); }))),
          h('tbody', null, rows))));
      }
      wrap.appendChild(h('p', { class: 'int-note', text: 'Published servers are the only ones a user can see. Switching a server off hides it at once and stops every call, but keeps each person\'s saved sign-in, so switching it on again does not make them sign in again.' }));
      return wrap;
    }

    // ---- the usage tab: who is connected, and the last calls (the server, the tool, the outcome: never what was sent or returned)
    function drawUsage() {
      var who = st.usage.connected || [], calls = st.usage.calls || [];
      function table(head, rows) { return h('div', { class: 'table-wrap' }, h('table', null, h('thead', null, h('tr', null, head.map(function (t) { return h('th', { scope: 'col', text: t }); }))), h('tbody', null, rows))); }
      return h('div', null,
        h('h4', { text: 'Who is connected' }), who.length ? table(['Who', 'Connected to', 'Since'], who.slice(0, 50).map(function (x) { return h('tr', null, h('td', { text: x.user }), h('td', { text: x.conn }), h('td', { text: ago(x.at) })); })) : h('p', { class: 'sub', text: 'Nobody is connected to a server yet.' }),
        h('h4', { text: 'Recent calls' }), calls.length ? table(['When', 'Who', 'Server', 'Tool', 'Level', 'Outcome'], calls.slice(0, 50).map(function (c) { return h('tr', null, h('td', { text: ago(c.t) }), h('td', { text: c.user }), h('td', { text: c.conn }), h('td', { text: c.tool || c.op }), h('td', { text: c.level || '' }), h('td', { text: c.ok ? 'ok' : 'failed' })); })) : h('p', { class: 'sub', text: 'No calls yet.' }),
        h('p', { class: 'int-note', text: 'The server, the tool, the level and the outcome. Never what was sent or what came back. The files are kept 14 days and are not in a backup.' }));
    }

    // ---- the editor: three screens, as the canvas draws them (boards 5, 6 and 7)
    function steps() {
      var cur = st.screen, isNew = st.open === '+';
      var on = cur === 'publish' ? [false, false, true, true] : [isNew, !isNew, false, false];
      var done = cur === 'publish' ? [true, true, false, false] : isNew ? [false, false, false, false] : [true, false, false, false];
      var names = ['1 Name and address', '2 Test and sign-in', '3 Tools', '4 Publish'];
      var go = [function () { st.screen = 'setup'; draw(); }, function () { st.screen = w.lastTest ? 'test' : 'setup'; draw(); }, function () { st.screen = 'publish'; draw(); }, function () { st.screen = 'publish'; draw(); }];
      return h('div', { class: 'int-steps' }, names.map(function (n, k) {
        return h('button', { type: 'button', class: 'int-step' + (on[k] ? ' on' : done[k] ? ' dn' : ''), text: n, disabled: isNew && k > 0, onclick: go[k] });
      }));
    }
    function head(title) { return h('div', { class: 'int-top' }, h('h3', { text: title }), steps()); }
    function back() { return h('p', null, h('button', { class: 'int-link', type: 'button', text: '← All servers', onclick: function () { st.open = ''; w = null; draw(); } })); }

    function drawEditor() {
      if (!w) w = blank();
      var wrap = h('div');
      wrap.appendChild(back());
      if (st.screen === 'test' && w.id) wrap.appendChild(drawTest());
      else if (st.screen === 'publish' && w.id) wrap.appendChild(drawPublish());
      else wrap.appendChild(drawSetup());
      return wrap;
    }

    // ---- board 5: add a server
    function drawSetup() {
      var isNew = st.open === '+', wrap = h('div');
      wrap.appendChild(head(isNew ? 'Add a server' : w.title + ' · Name and sign-in'));
      var lt = w.lastTest, a = w.auth || { mode: 'token' };
      var choice = a.mode === 'oauth' ? (a.auto ? 'auto' : 'oauth') : a.mode || 'token';
      var title = wide(h('input', { type: 'text', class: 'field', maxlength: '40', value: w.title || '', 'aria-label': 'Name' }));
      var keywords = wide(h('input', { type: 'text', class: 'field', value: (w.keywords || []).join(', '), placeholder: 'notion, page, wiki, notes', 'aria-label': 'Keywords' }));
      var addr = wide(h('input', { type: 'url', class: 'field', maxlength: '300', value: w.url || '', placeholder: 'https://mcp.example.com/mcp', 'aria-label': 'Server address' }));
      var hint = h('p', { class: 'int-hint' });
      function paintHint() {
        hint.textContent = '';
        if (/^https:\/\/[^\/\s]+\.[^\/\s]+/.test(addr.value.trim())) { hint.appendChild(h('span', { class: 'int-ok', text: '✓' })); hint.appendChild(document.createTextNode(' https · public address · host is on your list of servers')); }
        else hint.textContent = 'https only.';
      }
      addr.addEventListener('input', paintHint);
      paintHint();

      // the logo (optional): shown before the name in the people's list; no logo: the first letter of the name
      var logo = w.logo || '';
      var logoBox = h('div', { class: 'int-logorow' });
      var logoIn = h('input', { type: 'url', class: 'field', maxlength: '300', value: logo, placeholder: 'https://cdn.simpleicons.org/notion', 'aria-label': 'Logo', style: 'flex:1 1 220px;min-width:0' });
      var tile = h('span');
      function paintLogo() {
        var v = logoIn.value.trim();
        logo = /^https:\/\//.test(v) ? v : '';
        tile.replaceWith(tile = logoTile({ title: title.value || w.title, logo: logo }, 40));
      }
      logoIn.addEventListener('input', paintLogo);
      title.addEventListener('input', function () { if (!logo) paintLogo(); });
      logoBox.appendChild(tile);
      logoBox.appendChild(logoIn);
      paintLogo();

      // the sign-in cards: the one found by the test is chosen; a card is dim when it is not the one in use
      var cards = h('div', { style: 'display:flex;flex-direction:column;gap:8px' });
      var authUrl = wide(h('input', { type: 'url', class: 'field', value: a.authUrl || '', placeholder: 'Authorization URL', 'aria-label': 'Authorization URL' }));
      var tokenUrl = wide(h('input', { type: 'url', class: 'field', value: a.tokenUrl || '', placeholder: 'Token URL', 'aria-label': 'Token URL' }));
      var clientId = wide(h('input', { type: 'text', class: 'field', value: a.clientId || '', placeholder: 'Client ID', 'aria-label': 'Client ID' }));
      var help = wide(h('input', { type: 'text', class: 'field', maxlength: '600', value: w.help || '', placeholder: 'How to get a token (shown to the person)', 'aria-label': 'How to get a token' }));
      var adv = h('div', { class: 'int-adv' });
      function paintModes() {
        cards.textContent = '';
        function card(value, titleText, tag, tagOk, text) {
          return h('button', { type: 'button', class: 'int-mode' + (choice === value ? ' on' : ' dim'), 'aria-pressed': choice === value ? 'true' : 'false', onclick: function () { choice = value; paintModes(); } },
            h('span', { class: 'dot' }), h('span', null, h('b', null, document.createTextNode(titleText), tag ? h('span', { class: 'int-tag' + (tagOk ? ' ok' : ''), text: tag }) : null), h('span', { class: 't', text: text })));
        }
        var found = lt && lt.signIn;
        cards.appendChild(card('auto', 'Automatic OAuth', found === 'auto' ? 'offered by this server' : found ? 'not offered' : '', found === 'auto',
          'Genie registers itself with ' + (w.title || 'the server') + ' and each person signs in. You fill in nothing and never see a secret.'));
        cards.appendChild(card('oauth', 'OAuth with an app you register', found === 'auto' ? 'not needed here' : found === 'oauth' ? 'needed here' : '', found === 'oauth',
          'Used only when the server does not offer automatic registration: you create an app at the service once.'));
        cards.appendChild(card('token', 'Their own token', 'fallback', false,
          'For a server with no sign-in at all. Each person pastes a token that is sealed for them alone. You may add a short "how to get it" note.'));
        if (found === 'none' || choice === 'none') cards.appendChild(card('none', 'No sign-in', found === 'none' ? 'found by the test' : '', found === 'none', 'The server answered without a credential: an open server. Enabling it is all a person does.'));
        if (choice === 'token') cards.appendChild(help);
        paintAdvanced();
      }
      function paintAdvanced() {
        var on = choice === 'oauth';
        adv.className = 'int-adv' + (on ? '' : ' dim');
        adv.textContent = '';
        [authUrl, tokenUrl, clientId].forEach(function (x) { x.disabled = !on; });
        var sec = secretField(w, 'oauthClientSecret', 'Client secret (write-only, kept sealed)', !on);
        adv.appendChild(h('div', { class: 'h' }, h('span', { text: 'Advanced: set the OAuth app by hand' }), h('span', { class: 'int-tag', text: 'shown only when automatic fails' })));
        adv.appendChild(h('div', { class: 'fields' }, h('div', { class: 'int-two' }, h('div', null, authUrl), h('div', null, tokenUrl)), h('div', { class: 'int-two' }, h('div', null, clientId), h('div', null, sec)),
          h('p', { class: 'int-hint', text: 'Register this redirect address with the service: ' + (st.redirect || 'https://your-site/genie_conn_oauth') })));
      }
      var testBtn = btn('Test the server', 'primary', function () { saveRecord().then(function (res) { if (res) return act({ op: 'test', id: w.id }, 'Tested.').then(function (r2) { if (r2) { st.screen = 'test'; draw(); } }); }); });
      var left = h('div', { class: 'int-panel int-main' },
        h('div', { class: 'int-sec', text: '1 · Only two things to type' }),
        h('div', { class: 'int-two' }, h('div', null, field('Name', title)), h('div', null, field('Keywords (how Genie knows a task is about it)', keywords))),
        h('div', null, h('label', { class: 'int-lb', text: 'Server address (https only)' }), addr, hint),
        h('div', null, h('label', { class: 'int-lb', text: 'Logo (optional)' }), logoBox, h('p', { class: 'int-hint', text: 'An https image address. It is shown before the name in the list people see; without one, the first letter of the name is shown.' })),
        h('div', { style: 'display:flex;gap:8px;align-items:center;flex-wrap:wrap' }, testBtn, h('span', { class: 'int-hint', style: 'margin:0', text: 'It reads only: connect, list, look at how sign-in works.' })),
        h('div', { class: 'int-sec', text: '2 · Sign-in, detected by the test' }), cards, adv,
        h('div', { class: 'int-foot' }, btn('Save draft', '', function () { saveRecord().then(function (r) { if (r) toast('Saved.'); }); }), btn('Next: tools', 'primary', function () { saveRecord().then(function (r) { if (r) { st.screen = 'publish'; draw(); } }); })));
      paintModes();

      var mode = choice === 'auto' || choice === 'oauth' ? 'OAuth' : choice === 'none' ? 'open' : 'token';
      var disc = secretField(w, 'discoveryToken', 'Discovery token (optional)', false);
      var side = h('div', { class: 'int-side' },
        h('div', { class: 'int-panel lime' }, h('h3', { text: 'What a person will see' }), h('div', { class: 'int-who' }, logoTile({ title: w.title, logo: logo }, 28), h('span', null, document.createTextNode((w.title || 'Your server') + ' · '), h('span', { class: 'pill', text: mode }))),
          h('p', { class: 'sub', style: 'margin:0', text: w.description || 'A line about the server, from the description you give it.' }),
          h('p', null, h('span', { class: 'btn primary', text: choice === 'none' ? 'Enable' : choice === 'token' ? 'Connect' : 'Sign in with ' + (w.title || 'the server') })),
          h('p', { class: 'int-hint', text: choice === 'oauth' || choice === 'auto' ? 'One click, then the service asks them to allow access. No fields.' : choice === 'none' ? 'One click: an open server has nothing to give.' : 'They paste a token that is sealed for them alone.' })),
        h('div', { class: 'int-panel' }, h('h3', { text: 'Who sets up what' }), h('p', null, h('b', { text: 'Admin: ' }), document.createTextNode('name and address, once per server. If automatic sign-in is not offered, the OAuth app, once.')),
          h('p', null, h('b', { text: 'Person: ' }), document.createTextNode('only the sign-in click, or their own token for a server that has no sign-in. They never type endpoints, client IDs or secrets.'))),
        h('div', { class: 'int-panel' }, h('h3', { text: 'A draft is invisible' }), h('p', { text: 'Nobody but admins can see a draft or a tested server. Nothing is connected to it. Publish is the last step. A discovery token, if the server needs one just to list its tools, is optional, sealed, and used only by Test.' }), disc));
      wrap.appendChild(h('div', { class: 'int-cols' }, left, side));

      // reads the form and saves it (a new server gets its id here)
      function saveRecord() {
        var au = copy(w.auth || { mode: 'token' });
        if (choice === 'auto') { au.mode = 'oauth'; au.auto = true; }
        else if (choice === 'oauth') { au.mode = 'oauth'; au.auto = false; au.authUrl = authUrl.value.trim(); au.tokenUrl = tokenUrl.value.trim(); au.clientId = clientId.value.trim(); }
        else { au.mode = choice; au.auto = false; }
        delete au.clients;
        var rec = { id: w.id || '', title: title.value.trim(), url: addr.value.trim(), keywords: words(keywords.value), description: w.description || '', help: help.value.trim() || w.help || '', logo: logo, auth: au,
          audience: w.audience || { everyone: false, people: [] }, limits: w.limits || {} };
        return act({ op: 'save', integration: rec });
      }
      return wrap;
    }

    // a secret is written and removed, never read: only whether one is set is shown
    function secretField(i, kind, label, disabled) {
      var set = !!(i.secrets && i.secrets[kind]);
      var input = h('input', { type: 'password', class: 'field', autocomplete: 'new-password', placeholder: set ? label + ' · saved, type to replace' : label, 'aria-label': label, disabled: disabled });
      var save = h('button', { class: 'btn', type: 'button', text: 'Save', disabled: disabled, onclick: function () {
        if (!input.value) return;
        if (!i.id) { toast('Save the server first.', 'error'); return; }
        var v = input.value;
        input.value = '';
        act({ op: 'secret', id: i.id, kind: kind, value: v }, label.replace(/ \(.*$/, '') + ' saved.');
      } });
      var rm = set ? h('button', { class: 'btn', type: 'button', text: 'Remove', disabled: disabled, onclick: function () { act({ op: 'secret', id: i.id, kind: kind, value: '' }, 'Removed.'); } }) : null;
      return h('div', { style: 'display:flex;gap:6px;align-items:center;flex-wrap:wrap' }, input, save, rm, st.canStore ? null : h('span', { class: 'int-hint', text: 'OPENREPL_SECRET is not set on the server: nothing can be kept.' }));
    }

    // ---- board 6: discover and test
    function drawTest() {
      var wrap = h('div');
      wrap.appendChild(head(w.title + ' · Discover and test'));
      var lt = w.lastTest;
      var live = st.live[w.id] || null;
      var rows = [];
      function ck(kind, title, small) { return h('div', { class: 'int-ck' + (kind === 'w' ? ' w' : kind === 'x' ? ' bad' : '') }, h('b', { text: kind === 'w' ? '!' : kind === 'x' ? '✗' : '✓' }), h('span', null, document.createTextNode(title), small ? h('small', { text: small }) : null)); }
      if (!lt) rows.push(h('p', { class: 'sub', text: 'Not tested yet.' }));
      else {
        if (lt.signIn === 'unknown') rows.push(ck('x', 'The server could not be reached', lt.error || lt.detail || ''));
        else rows.push(ck('', 'Reached the server', 'https · public address' + (lt.ms ? ' · ' + lt.ms + ' ms' : '')));
        if (lt.protocol) rows.push(ck('', 'Protocol', lt.protocol === '2026-07-28' ? 'server/discover answered: 2026-07-28 (older servers fall back to initialize)' : 'initialize answered: ' + lt.protocol + ' (this server does not speak the 2026 revision)'));
        var si = { auto: ['Sign-in found: automatic OAuth', 'the server offers registration, so nothing to fill in (a discovery token, if used, only lists tools)'], oauth: ['Sign-in found: OAuth, by hand', 'the server does not let Genie register: make an OAuth app and type its client'], token: ['No usable sign-in found', 'the server wants a token'], none: ['No sign-in needed', 'the server answered without a credential'] }[lt.signIn];
        if (si) rows.push(ck('', si[0], si[1]));
        if (lt.ok) {
          rows.push(ck('', 'Tools listed: ' + lt.tools, (lt.bytes ? Math.max(1, Math.round(lt.bytes / 1024)) + ' KB' : '')));
          rows.push(ck('', 'Schemas read: ' + (lt.schemas || 0) + ' of ' + lt.tools, 'valid JSON Schema, arguments named'));
          if (lt.noDescription) rows.push(ck('w', (lt.noDescription === 1 ? 'One tool has' : lt.noDescription === 2 ? 'Two tools have' : lt.noDescription + ' tools have') + ' no description', 'Shown to the model as the bare name. Consider leaving them off.'));
          if (lt.skipped) rows.push(ck('w', (lt.skipped === 1 ? 'One name was refused' : lt.skipped + ' names were refused'), (lt.refused || []).map(function (n) { return '"' + n + '"'; }).join(', ') + ' has characters a call cannot use. It is not listed.'));
        } else if (lt.error) rows.push(ck('x', 'The tools were not listed', lt.error));
      }
      var left = h('div', { class: 'int-panel', style: 'flex:.8 1 280px' }, h('h3', { text: 'Test run' + (lt ? ' · ' + ago(lt.at) : '') }), rows,
        h('div', { class: 'int-foot' }, btn('Run again', '', function () { act({ op: 'test', id: w.id }, 'Tested.'); }), btn('Next: tools', 'primary', function () { st.screen = 'publish'; draw(); })),
        h('p', { class: 'int-hint', text: 'A test makes only reads: connect, list. It never calls a tool.' }));

      // the tools the server described, with its own hint; a click shows the schema it sent
      var tools = live || (w.tools || []).map(function (t) { return { name: t.name, description: '', args: [], hint: '', noInfo: true }; });
      if (!st.sel || !tools.some(function (t) { return t.name === st.sel; })) st.sel = tools.length ? tools[0].name : '';
      var sel = tools.filter(function (t) { return t.name === st.sel; })[0];
      var trs = tools.map(function (t) {
        var pol = (w.tools || []).filter(function (p) { return p.name === t.name; })[0];
        var tag = pol && pol['new'] ? h('span', { class: 'int-tg new', text: 'new' }) : h('span', { class: 'int-tg' + (t.hint === 'destructive' ? ' bad' : ''), text: t.hint || '' });
        return h('tr', { class: 'pick' + (t.name === st.sel ? ' sel' : ''), onclick: function () { st.sel = t.name; draw(); } },
          h('td', { class: 'int-mono' }, h('b', { text: t.name })), h('td', null, t.description ? document.createTextNode(t.description) : h('i', { text: t.noInfo ? '' : 'no description' })),
          h('td', { class: 'int-mono', text: (t.args || []).join(', ') }), h('td', null, tag));
      });
      var right = h('div', { style: 'display:flex;flex-direction:column;gap:14px;flex:1.5 1 320px;min-width:0' },
        h('div', { class: 'int-panel' }, h('h3', { text: tools.length + ' tools found' }),
          tools.length ? h('table', { class: 'int-table' }, h('thead', null, h('tr', null, ['Tool', 'What it says about itself', 'Arguments', 'Hint'].map(function (t) { return h('th', { scope: 'col', text: t }); }))), h('tbody', null, trs)) : h('p', { class: 'sub', text: 'No tools yet.' }),
          h('p', { class: 'int-hint', text: '"Hint" is the server\'s own claim. It is shown, never trusted: the admin sets the level in the next step.' })),
        h('div', { class: 'int-panel' }, h('h3', { text: (sel ? sel.name : 'A tool') + ' · schema as the server sent it' }),
          h('pre', { class: 'int-pre', text: sel && sel.schema ? JSON.stringify(sel.schema, null, 2) : live ? '(this tool takes no arguments)' : 'Run the test to see what each tool says about itself.' }),
          h('p', { class: 'int-hint', text: 'This is the schema the gateway checks every call against. It is fetched live and kept only as a hash.' })));
      wrap.appendChild(h('div', { class: 'int-cols' }, left, right));
      return wrap;
    }

    // ---- board 7: tools and publish
    function drawPublish() {
      var wrap = h('div');
      wrap.appendChild(head(w.title + ' · Tools and publish'));
      var live = st.live[w.id] || [];
      function liveOf(n) { return live.filter(function (t) { return t.name === n; })[0]; }
      var tools = w.tools || [];
      var list = h('div');
      function paintTools() {
        list.textContent = '';
        tools.forEach(function (t) {
          var lv = liveOf(t.name);
          var small = [lv ? (lv.description || 'no description') : (t.note || '')];
          if (lv && lv.hint === 'destructive') small.push('server says destructive');
          if (!t.level) small.push('off until you choose a level');
          var flag = t['new'] && t.changed ? 'NEW · changed' : t['new'] ? 'NEW' : t.changed ? 'changed' : '';
          list.appendChild(h('div', { class: 'int-tool' + (t.enabled ? '' : ' off') },
            h('button', { type: 'button', class: 'int-sw', role: 'switch', 'aria-checked': t.enabled ? 'true' : 'false', 'aria-label': t.name + ' is on',
              onclick: function () { if (!t.enabled && !t.level) { toast(t.name + ' needs a level before it can be switched on.', 'error'); return; } t.enabled = !t.enabled; paintTools(); paintPublish(); } }),
            h('div', { class: 'int-name' }, h('b', { text: t.name }), flag ? h('span', { class: 'int-fl', text: flag }) : null, h('small', { text: small.filter(Boolean).join(' · ') })),
            h('div', { class: 'int-seg', role: 'group', 'aria-label': 'Level of ' + t.name }, LEVELS.map(function (l) {
              return h('button', { type: 'button', class: l, 'aria-pressed': t.level === l ? 'true' : 'false', text: l, onclick: function () { t.level = l; paintTools(); } });
            }))));
        });
        if (!tools.length) list.appendChild(h('p', { class: 'sub', text: 'No tools yet: test the server first.' }));
      }
      paintTools();
      var allReads = btn('All reads on', '', function () { tools.forEach(function (t) { var lv = liveOf(t.name); if (t.level === 'read' || (!t.level && lv && lv.hint === 'read-only')) { t.level = 'read'; t.enabled = true; } }); paintTools(); paintPublish(); });
      var allWrites = btn('All writes off', '', function () { tools.forEach(function (t) { if (t.level === 'write') t.enabled = false; }); paintTools(); paintPublish(); });
      var left = h('div', { class: 'int-panel', style: 'flex:1.6 1 420px' },
        h('div', { class: 'int-top', style: 'margin:0' }, h('h3', { text: 'Which tools may Genie use, and how carefully?' }), h('span', { class: 'actions' }, allReads, allWrites)),
        h('div', { class: 'int-legend' }, h('span', null, h('b', { text: 'read', style: 'color:var(--info)' }), document.createTextNode(' asks once')), h('span', null, h('b', { text: 'write', style: 'color:var(--warn)' }), document.createTextNode(' asks on a card')), h('span', null, h('b', { text: 'restricted', style: 'color:var(--danger)' }), document.createTextNode(' asks every time, never remembered'))),
        list, h('p', { class: 'int-hint', text: 'Default for a tool the server adds or changes later: off, flagged here, and the person\'s calls to it are refused. A tool that has no level cannot be switched on.' }));

      // who sees it, the limits, publish
      var aud = w.audience || { everyone: false, people: [] };
      var everyone = !!aud.everyone;
      var radios = h('div', { class: 'int-radios' });
      var people = wide(h('input', { type: 'text', class: 'field', value: (aud.people || []).join(', '), placeholder: 'ann@example.com, bob@example.com', 'aria-label': 'People' }));
      function paintRadios() {
        radios.textContent = '';
        [[true, 'Everyone signed in'], [false, 'Only these people']].forEach(function (o) {
          radios.appendChild(h('button', { type: 'button', class: 'int-radio', 'aria-pressed': everyone === o[0] ? 'true' : 'false', text: o[1], onclick: function () { everyone = o[0]; people.disabled = everyone; paintRadios(); } }));
        });
        people.disabled = everyone;
      }
      paintRadios();
      var perTask = wide(h('input', { type: 'number', class: 'field', min: '0', max: '20', value: (w.limits && w.limits.callsPerTask) || '', placeholder: '20', 'aria-label': 'Calls per task' }));
      var perMin = wide(h('input', { type: 'number', class: 'field', min: '0', max: '60', value: (w.limits && w.limits.perMinute) || '', placeholder: '60', 'aria-label': 'Calls per minute' }));
      var statusLine = h('p', { class: 'int-hint' });
      function paintPublish() {
        var on = tools.filter(function (t) { return t.enabled; }).length;
        statusLine.textContent = '';
        statusLine.appendChild(document.createTextNode('Status: '));
        statusLine.appendChild(h('b', { text: STATUS[w.status] || w.status, style: 'color:var(--info)' }));
        statusLine.appendChild(document.createTextNode(' · ' + on + ' tools on · ' + (tools.length - on) + ' off'));
      }
      paintPublish();
      // what is saved: the record (audience, limits), then the tools; Publish and Switch off are the ops after it
      function saveAll() {
        var rec = copy(w);
        rec.audience = { everyone: everyone, people: words(people.value) };
        rec.limits = { callsPerTask: parseInt(perTask.value, 10) || 0, perMinute: parseInt(perMin.value, 10) || 0 };
        delete rec.tools; delete rec.lastTest; delete rec.usage; delete rec.secrets; delete rec.publishable; delete rec.toolsOn;
        if (rec.auth) delete rec.auth.clients;
        return act({ op: 'save', integration: rec }).then(function (res) {
          if (!res) return null;
          if (!tools.length) return res;
          return act({ op: 'tools', id: w.id, tools: tools.map(function (t) { return { name: t.name, enabled: !!t.enabled, level: t.level || '', note: t.note || '' }; }) });
        });
      }
      var published = w.status === 'published';
      var buttons = published
        ? [btn('Save changes', 'primary', function () { saveAll().then(function (r) { if (r) toast('Saved.'); }); }), btn('Unpublish', '', function () { act({ op: 'unpublish', id: w.id }, 'Switched off.'); })]
        : [btn(w.status === 'off' ? 'Publish again' : 'Publish to users', 'primary', function () { saveAll().then(function (r) { if (r) return act({ op: 'publish', id: w.id }, 'Published.'); }); }), btn('Save as draft', '', function () { saveAll().then(function (r) { if (r) toast('Saved.'); }); })];
      var del = published ? null : h('button', { class: 'int-link', type: 'button', text: 'Delete this server…', onclick: function () {
        ask({ title: 'Delete ' + w.title + '?', body: 'The record and its secrets go. People\'s saved sign-ins stay on their own accounts, unused.', confirm: 'Delete', danger: true }).then(function (yes) { if (yes) act({ op: 'delete', id: w.id }, 'Deleted.'); });
      } });
      var right = h('div', { class: 'int-side' },
        h('div', { class: 'int-panel' }, h('h3', { text: 'Who can see it' }), radios, people,
          h('div', { class: 'int-two' }, h('div', null, field('Calls per task', perTask)), h('div', null, field('Calls per minute', perMin))),
          h('p', { class: 'int-hint', text: 'Limits can only be lower than the site\'s (20 and 60).' })),
        h('div', { class: 'int-panel' }, h('h3', { text: 'Publish' }), statusLine, h('div', { class: 'actions' }, buttons),
          w.publishable ? h('p', { class: 'int-hint', text: 'Before it can be published: ' + w.publishable }) : null,
          h('p', { class: 'int-hint', text: 'Publishing writes one line to the admin audit log: who, which server, which tools on at which level. Unpublish hides it at once and stops every call.' }), del));
      wrap.appendChild(h('div', { class: 'int-cols' }, left, right));
      return wrap;
    }

    // ---- board 9, the admin's column: the requests for a server
    function drawRequests() {
      var wrap = h('div');
      if (!st.reqs.length) {
        wrap.appendChild(h('p', { class: 'sub', text: 'Nobody has asked for a server yet. A person asks from the Integrations tab of the Plugins dialog; it is only text until you answer.' }));
        return wrap;
      }
      function post(q, action, reason) {
        api.post('admin/connection-requests', { id: q.id, action: action, reason: reason }).then(function (res) {
          if (res.integration) {
            var k = st.list.map(function (i) { return i.id; }).indexOf(res.integration.id);
            if (k >= 0) st.list[k] = res.integration; else st.list.push(res.integration);
          }
          toast(action === 'decline' ? 'Declined.' : res.published ? 'Approved and published for ' + (action === 'approve' ? 'everyone' : firstName(q.email)) + '.' : 'Approved as a draft: ' + (res.why || 'finish it.'));
          return load().then(function () {
            // approving shows one screen: the tools, with the safe defaults the test switched on
            if (res.integration && action !== 'decline') { st.tab = 'servers'; st.open = res.integration.id; st.screen = action === 'customize' ? 'setup' : 'publish'; w = copy(find(res.integration.id) || res.integration); draw(); }
          });
        }, fail);
      }
      function decide(q, action) {
        if (action !== 'decline') return post(q, action, '');
        var reason = wide(h('input', { type: 'text', class: 'field', maxlength: '200', placeholder: 'Why (shown to the person)', 'aria-label': 'Reason' }));
        ask({ title: 'Decline ' + q.title + '?', body: 'The person sees the reason.', content: reason, confirm: 'Decline', danger: true }).then(function (yes) { if (yes) post(q, 'decline', reason.value.trim()); });
      }
      st.reqs.forEach(function (q) {
        var same = st.reqs.filter(function (x) { return x.url === q.url && x.status === 'waiting'; }).length;
        var line = (q.email || q.user) + ' · ' + ago(q.at) + (q.status === 'waiting' ? ' · 1 of ' + same + (same === 1 ? ' person asking' : ' people asking') : '');
        var buttons = q.status === 'waiting'
          ? h('span', { class: 'actions' }, btn('Approve for ' + firstName(q.email || q.user) + ' only', 'primary', function () { decide(q, 'approve_user'); }), btn('Approve for everyone', '', function () { decide(q, 'approve'); }),
              btn('Customize…', '', function () { decide(q, 'customize'); }), btn('Decline', '', function () { decide(q, 'decline'); }))
          : h('span', { class: 'sub', text: q.status + (q.scope ? ' (' + q.scope + ')' : '') + (q.reason ? ': ' + q.reason : '') });
        wrap.appendChild(h('div', { class: 'int-panel', style: 'margin-bottom:12px' },
          h('div', null, h('b', { text: q.title }), document.createTextNode(' '), h('span', { class: 'pill ' + (q.status === 'waiting' ? 'warn' : q.status === 'approved' ? 'ok' : 'fail'), text: q.status })),
          h('p', { class: 'sub', style: 'margin:0', text: line }), h('p', { text: q.url }),
          q.note || (q.signin && q.signin !== 'unknown') ? h('p', { text: (q.note ? '"' + q.note + '"' : '') + (q.signin && q.signin !== 'unknown' ? ' · they think: ' + (q.signin === 'oauth' ? 'OAuth' : 'a token') : '') }) : null,
          q.docs ? h('p', { class: 'sub', style: 'margin:0', text: 'Docs: ' + q.docs }) : null,
          buttons));
      });
      wrap.appendChild(h('p', { class: 'int-note', text: 'Plain text, as typed. The server has not been contacted.' }));
      wrap.appendChild(h('p', { class: 'int-note', text: 'Approving runs Test and shows one screen, with safe defaults: reads on, writes off, restricted off. A tool added later starts off. The first button is the default (a pilot for that person): widen the audience later, nothing to redo. If automatic OAuth was found the admin sets nothing up; otherwise it stays a draft until the OAuth app or the token is set. Decline asks for a reason (at most 200 characters) that the requester sees.' }));
      return wrap;
    }

    return { root: root, refresh: load, dirty: function () { return false; }, destroy: function () {} };
  }

  window.AdminIntegrations = { make: make };
})();
