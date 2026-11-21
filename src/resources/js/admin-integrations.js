/*
 * Integrations of the admin dashboard (admin.html): the servers Genie can be connected to, which an admin adds, tests, sets up and publishes here
 * (server/genie_integrations*.go, docs/lld/19 section 25). admin.js loads this file when the page is opened. It puts its view on
 * window.AdminIntegrations; the helpers it needs from admin.js are on window.AdminKit. Same rules as admin.js: everything from the server is put on
 * the page with textContent only, and no secret is ever shown (the server sends only whether one is set).
 */
(function () {
  'use strict';

  var K = window.AdminKit;
  var api = K.api, ask = K.ask, fail = K.fail, h = K.h, pageHead = K.pageHead, toast = K.toast;

  var STATUS = { draft: 'Draft', tested: 'Tested', published: 'Published', off: 'Switched off' };
  var LEVELS = [['', 'Choose…'], ['read', 'Read'], ['write', 'Write'], ['restricted', 'Restricted']];

  function card(title, sub, children) {
    return h('div', { class: 'card' }, h('h2', { text: title }), sub ? h('p', { class: 'sub', text: sub }) : null, children);
  }
  function field(label, input, hint) {
    return h('div', { class: 'form-row' }, h('div', { class: 'what' }, h('b', { text: label }), hint ? h('span', { text: hint }) : null), h('div', null, input));
  }
  function when(s) { return s ? String(s).replace('T', ' ').replace('Z', '') : 'never'; }
  function hostOf(u) { try { return new URL(u).host; } catch (e) { return String(u || ''); } }
  function words(text) { return String(text || '').split(/[,\n]+/).map(function (x) { return x.trim(); }).filter(Boolean); }

  function viewIntegrations() {
    var root = h('div'), body = h('div');
    var st = { tab: 'servers', list: [], reqs: [], redirect: '', canStore: true, open: '', busy: false };
    var addBtn = h('button', { class: 'btn primary', type: 'button', text: '+ Add a server', onclick: function () { st.tab = 'servers'; st.open = '+'; draw(); } });
    root.appendChild(pageHead('Integrations', 'The servers Genie can be connected to. A person sees a server only when you publish it, and signs in with their own account.', addBtn));
    root.appendChild(body);

    function find(id) { return st.list.filter(function (i) { return i.id === id; })[0]; }
    function load() {
      return Promise.all([api.get('admin/integrations'), api.get('admin/connection-requests')]).then(function (r) {
        st.list = r[0].integrations || [];
        st.redirect = r[0].redirect || '';
        st.canStore = r[0].canStore !== false;
        st.reqs = (r[1].requests || []).filter(function (q) { return q.kind === 'server'; });
        draw();
      }, fail);
    }
    // an op of the route: the answer holds the changed integration, which replaces the one in the list
    function act(body, okMsg) {
      if (st.busy) return Promise.resolve();
      st.busy = true;
      return api.post('admin/integrations', body).then(function (res) {
        st.busy = false;
        if (res.integration) {
          var k = st.list.map(function (i) { return i.id; }).indexOf(res.integration.id);
          if (k >= 0) st.list[k] = res.integration; else st.list.push(res.integration);
          if (st.open === '+') st.open = res.integration.id;
        }
        if (res.deleted) { st.list = st.list.filter(function (i) { return i.id !== res.deleted; }); st.open = ''; }
        if (okMsg) toast(okMsg);
        draw();
        return res;
      }, function (e) { st.busy = false; fail(e); draw(); });
    }

    function draw() {
      body.textContent = '';
      var waiting = st.reqs.filter(function (q) { return q.status === 'waiting'; }).length;
      var tabs = h('div', { class: 'site-tabs', role: 'tablist' },
        [['servers', 'Servers ' + st.list.length], ['requests', 'Requests' + (waiting ? ' ' + waiting : '')]].map(function (t) {
          return h('button', { type: 'button', class: 'site-tab', role: 'tab', 'aria-selected': st.tab === t[0] ? 'true' : 'false', text: t[1],
            onclick: function () { st.tab = t[0]; st.open = ''; draw(); } });
        }));
      body.appendChild(tabs);
      if (st.tab === 'requests') body.appendChild(drawRequests());
      else if (st.open) body.appendChild(drawEditor(st.open === '+' ? null : find(st.open)));
      else body.appendChild(drawList());
    }

    // ---- the list
    function drawList() {
      if (!st.list.length) return card('Servers', 'Nothing here yet. Add a server with its name and address, then test it.', null);
      var rows = st.list.map(function (i) {
        var lt = i.lastTest;
        return h('tr', null,
          h('td', null, h('b', { text: i.title }), h('div', { class: 'sub', text: hostOf(i.url) + ' · ' + (i.auth && i.auth.mode === 'oauth' ? (i.auth.auto ? 'automatic OAuth' : 'OAuth') : i.auth && i.auth.mode === 'none' ? 'no sign-in' : 'token') })),
          h('td', null, h('span', { class: 'pill', text: STATUS[i.status] || i.status })),
          h('td', { text: (i.toolsOn || 0) + ' of ' + (i.tools ? i.tools.length : 0) + (i.tools && i.tools.length ? '' : ' (not tested)') }),
          h('td', { text: ((i.usage && i.usage.enabled) || 0) + ' enabled · ' + ((i.usage && i.usage.connected) || 0) + ' connected' }),
          h('td', { text: lt ? when(lt.at) + (lt.ok ? ' · ok' : lt.needsAuth ? ' · needs a credential' : ' · failed') : 'never' }),
          h('td', null, h('button', { class: 'btn', type: 'button', text: 'Edit', onclick: function () { st.open = i.id; draw(); } })));
      });
      return card('Servers', 'Published servers are the only ones people can see. Switching a server off hides it at once and stops every call, but keeps each person\'s sign-in.',
        h('div', { class: 'table-wrap' }, h('table', null,
          h('thead', null, h('tr', null, ['Server', 'Status', 'Tools on', 'People', 'Last test', ''].map(function (t) { return h('th', { scope: 'col', text: t }); }))),
          h('tbody', null, rows))));
    }

    // ---- the editor of one server
    function drawEditor(i) {
      var isNew = !i;
      i = i || { title: '', url: '', keywords: [], description: '', help: '', status: 'draft', auth: { mode: 'token' }, audience: { everyone: false, people: [] }, limits: {}, tools: [], secrets: {} };
      var wrap = h('div');
      wrap.appendChild(h('p', null, h('button', { class: 'btn', type: 'button', text: '← All servers', onclick: function () { st.open = ''; draw(); } }),
        ' ', h('span', { class: 'pill', text: STATUS[i.status] || i.status })));

      // 1 · name and address
      var title = h('input', { type: 'text', class: 'field', maxlength: '40', value: i.title, 'aria-label': 'Name' });
      var addr = h('input', { type: 'url', class: 'field', maxlength: '300', value: i.url, placeholder: 'https://mcp.example.com/mcp', 'aria-label': 'Server address' });
      var keywords = h('input', { type: 'text', class: 'field', value: (i.keywords || []).join(', '), placeholder: 'notion, pages, notes', 'aria-label': 'Keywords' });
      var desc = h('input', { type: 'text', class: 'field', maxlength: '300', value: i.description || '', 'aria-label': 'Description' });
      [title, addr, keywords, desc].forEach(function (x) { x.style.width = '100%'; });
      wrap.appendChild(card('1 · Name and address', 'Only these two are needed. Everything else is found by the test.', [
        field('Name', title), field('Server address', addr, 'https only. The server is reached from this site.'),
        field('Keywords', keywords, 'How Genie knows that a task is about it.'), field('Description', desc, 'One line the person sees.')]));

      // 2 · sign-in
      var mode = (i.auth && i.auth.mode) || 'token';
      var choice = mode === 'oauth' ? (i.auth.auto ? 'auto' : 'oauth') : mode;
      var radios = {}, advanced = h('div');
      var lt = i.lastTest;
      var found = lt ? { auto: 'Automatic OAuth: the server offers registration, so nothing is typed.', oauth: 'The server has a sign-in but does not let Genie register: make an OAuth app and type its client.', token: 'No usable sign-in: a token.', none: 'The server answered without a credential.', unknown: 'The server could not be read.' }[lt.signIn] : '';
      function opt(value, label, hint) {
        var r = h('input', { type: 'radio', name: 'signin', value: value, checked: choice === value, onchange: function () { choice = value; paintAdvanced(); } });
        radios[value] = r;
        return h('label', { class: 'check' }, r, h('span', null, h('b', { text: label }), ' ', h('span', { class: 'sub', text: hint })));
      }
      var authUrl = h('input', { type: 'url', class: 'field', value: (i.auth && i.auth.authUrl) || '', 'aria-label': 'Authorization address' });
      var tokenUrl = h('input', { type: 'url', class: 'field', value: (i.auth && i.auth.tokenUrl) || '', 'aria-label': 'Token address' });
      var clientId = h('input', { type: 'text', class: 'field', value: (i.auth && i.auth.clientId) || '', 'aria-label': 'Client ID' });
      var scopes = h('input', { type: 'text', class: 'field', value: ((i.auth && i.auth.scopes) || []).join(' '), 'aria-label': 'Scopes' });
      [authUrl, tokenUrl, clientId, scopes].forEach(function (x) { x.style.width = '100%'; });
      var help = h('input', { type: 'text', class: 'field', maxlength: '600', value: i.help || '', 'aria-label': 'How to get a token' });
      help.style.width = '100%';
      function paintAdvanced() {
        advanced.textContent = '';
        if (choice === 'oauth') {
          advanced.appendChild(h('div', null, h('p', { class: 'sub', text: 'Register this redirect address with the service: ' + (st.redirect || '(open this page on the site\'s own address)') }),
            field('Authorization address', authUrl), field('Token address', tokenUrl), field('Client ID', clientId), field('Scopes', scopes, 'Separated by spaces. Leave empty if the service wants none.'),
            secretRow(i, 'oauthClientSecret', 'Client secret', 'Write-only: sealed on the server, never shown again.')));
        } else if (choice === 'token') {
          advanced.appendChild(field('How to get a token', help, 'Shown to the person above the field where they paste it.'));
        }
      }
      paintAdvanced();
      var signin = [
        found ? h('p', { class: 'sub', text: 'Found by the test: ' + found }) : h('p', { class: 'sub', text: 'Test the server to find out how people sign in to it.' }),
        opt('auto', 'Automatic OAuth', 'Genie registers itself; each person signs in. Nothing to type.'),
        opt('oauth', 'OAuth with an app you made', 'You type the client once.'),
        opt('token', 'Their own token', 'Each person pastes one, sealed for them alone.'),
        opt('none', 'No sign-in', 'An open server.'),
        advanced,
        secretRow(i, 'discoveryToken', 'Discovery token (optional)', 'Only for listing the tools of a server that wants a credential. Used by Test, never on a person\'s call.')
      ];
      wrap.appendChild(card('2 · Sign-in', 'People never type endpoints, client IDs or secrets: they sign in, or paste their own token.', signin));

      // 3 · test
      var checks = [];
      if (lt) {
        checks.push(h('p', { class: 'sub', text: 'Last test ' + when(lt.at) + ': ' + (lt.ok ? 'ok' : lt.needsAuth ? 'the server wants a credential' : 'failed') + (lt.protocol ? ' · speaks ' + lt.protocol : '') + (lt.ok ? ' · ' + lt.tools + ' tools' : '') +
          (lt.skipped ? ' · ' + lt.skipped + ' with a name that cannot be used were left out' : '') }));
        if (lt.error) checks.push(h('p', { class: 'warn', text: lt.error }));
        if (lt.detail) checks.push(h('p', { class: 'sub', text: lt.detail }));
      }
      var testBtn = h('button', { class: 'btn', type: 'button', text: 'Save and test', onclick: function () { saveThen(function () { return act({ op: 'test', id: i.id }, 'Tested.'); }); } });
      wrap.appendChild(card('3 · Test', 'It reads only: connect, list, look at how sign-in works. It never calls a tool.', [checks, h('p', null, testBtn)]));

      // 4 · tools
      var rows = (i.tools || []).map(function (t) {
        var on = h('input', { type: 'checkbox', checked: !!t.enabled, 'aria-label': t.name + ' is on' });
        var lv = h('select', { class: 'field', 'aria-label': 'Level of ' + t.name }, LEVELS.map(function (l) { return h('option', { value: l[0], text: l[1], selected: (t.level || '') === l[0] }); }));
        var note = h('input', { type: 'text', class: 'field', maxlength: '120', value: t.note || '', 'aria-label': 'Note on ' + t.name });
        t._on = on; t._lv = lv; t._note = note;
        return h('tr', null, h('td', null, on), h('td', null, h('code', { text: t.name }), t.changed ? h('span', { class: 'pill', text: 'changed' }) : null, t['new'] ? h('span', { class: 'pill', text: 'new' }) : null),
          h('td', null, lv), h('td', null, note));
      });
      var toolsBtn = h('button', { class: 'btn', type: 'button', text: 'Save tools', onclick: function () {
        act({ op: 'tools', id: i.id, tools: (i.tools || []).map(function (t) { return { name: t.name, enabled: t._on.checked, level: t._lv.value, note: t._note.value }; }) }, 'Tools saved.');
      } });
      wrap.appendChild(card('4 · Tools', 'Switch on what Genie may use and say how careful it must be: Read asks once, Write asks on a card, Restricted asks every time and is never remembered. A tool the server adds or changes later is off until you look.',
        (i.tools && i.tools.length)
          ? [h('div', { class: 'table-wrap' }, h('table', null, h('thead', null, h('tr', null, ['On', 'Tool', 'Level', 'Note'].map(function (x) { return h('th', { scope: 'col', text: x }); }))), h('tbody', null, rows))), h('p', null, toolsBtn)]
          : h('p', { class: 'sub', text: 'No tools yet: test the server first.' })));

      // 5 · who sees it, limits, publish
      var every = h('input', { type: 'radio', name: 'aud', checked: !!(i.audience && i.audience.everyone) });
      var some = h('input', { type: 'radio', name: 'aud', checked: !(i.audience && i.audience.everyone) });
      var people = h('textarea', { class: 'field', rows: '3', 'aria-label': 'People', placeholder: 'one e-mail address on each line' });
      people.value = ((i.audience && i.audience.people) || []).join('\n');
      people.style.width = '100%';
      var perTask = h('input', { type: 'number', class: 'field', min: '0', max: '20', value: (i.limits && i.limits.callsPerTask) || '', placeholder: '20', 'aria-label': 'Calls for a task' });
      var perMin = h('input', { type: 'number', class: 'field', min: '0', max: '60', value: (i.limits && i.limits.perMinute) || '', placeholder: '60', 'aria-label': 'Calls a minute' });
      var buttons = h('span', { class: 'actions' },
        h('button', { class: 'btn', type: 'button', text: 'Save', onclick: function () { saveThen(function () { toast('Saved.'); }); } }),
        isNew ? null : (i.status === 'published'
          ? h('button', { class: 'btn', type: 'button', text: 'Switch off', onclick: function () { act({ op: 'unpublish', id: i.id }, 'Switched off.'); } })
          : h('button', { class: 'btn primary', type: 'button', text: i.status === 'off' ? 'Publish again' : 'Publish', onclick: function () { saveThen(function () { return act({ op: 'publish', id: i.id }, 'Published.'); }); } })),
        isNew || i.status === 'published' ? null : h('button', { class: 'btn danger', type: 'button', text: 'Delete…', onclick: function () {
          ask({ title: 'Delete ' + i.title + '?', body: 'The record and its secrets go. People\'s saved sign-ins stay on their own accounts, unused.', confirm: 'Delete', danger: true }).then(function (yes) { if (yes) act({ op: 'delete', id: i.id }, 'Deleted.'); });
        } }));
      wrap.appendChild(card('5 · Who sees it, and publish', 'A draft is seen by admins only. Limits can only be lower than the site\'s (20 calls for a task, 60 a minute).', [
        field('Audience', h('div', null, h('label', { class: 'check' }, every, h('span', { text: 'Everyone signed in' })), h('label', { class: 'check' }, some, h('span', { text: 'Only these people' })), people)),
        field('Calls for a task', perTask), field('Calls a minute', perMin),
        i.publishable ? h('p', { class: 'sub', text: 'Before it can be published: ' + i.publishable }) : null,
        h('p', null, buttons)]));

      // reads the form into the integration the route takes, and saves it
      function auth() {
        var a = JSON.parse(JSON.stringify(i.auth || { mode: 'token' }));
        if (choice === 'auto') { a.mode = 'oauth'; a.auto = true; }
        else if (choice === 'oauth') { a.mode = 'oauth'; a.auto = false; a.authUrl = authUrl.value.trim(); a.tokenUrl = tokenUrl.value.trim(); a.clientId = clientId.value.trim(); a.scopes = words(scopes.value.replace(/\s+/g, ',')); }
        else { a.mode = choice; a.auto = false; }
        delete a.clients;
        return a;
      }
      function saveThen(next) {
        var rec = { id: isNew ? '' : i.id, title: title.value.trim(), url: addr.value.trim(), keywords: words(keywords.value), description: desc.value.trim(), help: help.value.trim(), auth: auth(),
          audience: { everyone: every.checked, people: words(people.value) },
          limits: { callsPerTask: parseInt(perTask.value, 10) || 0, perMinute: parseInt(perMin.value, 10) || 0 } };
        return act({ op: 'save', integration: rec }).then(function (res) { if (res && res.integration) { if (isNew) i = res.integration; return next ? next() : null; } });
      }
      return wrap;
    }

    // a secret is written and removed, never read: only whether one is set is shown
    function secretRow(i, kind, label, hint) {
      var set = !!(i.secrets && i.secrets[kind]);
      var input = h('input', { type: 'password', class: 'field', autocomplete: 'new-password', placeholder: set ? 'Saved. Type to replace it.' : 'Not set', 'aria-label': label });
      var save = h('button', { class: 'btn', type: 'button', text: 'Save', onclick: function () {
        if (!input.value) return;
        if (!i.id) { toast('Save the server first.', 'error'); return; }
        act({ op: 'secret', id: i.id, kind: kind, value: input.value }, label + ' saved.');
        input.value = '';
      } });
      var remove = set ? h('button', { class: 'btn', type: 'button', text: 'Remove', onclick: function () { act({ op: 'secret', id: i.id, kind: kind, value: '' }, label + ' removed.'); } }) : null;
      return field(label, h('span', { class: 'actions' }, input, save, remove), hint + (st.canStore ? '' : ' OPENREPL_SECRET is not set on the server: nothing can be kept.'));
    }

    // ---- the requests for a server
    function drawRequests() {
      if (!st.reqs.length) return card('Requests for a server', 'Nobody has asked for a server yet. A person asks from the Connections tab of the Plugins dialog; it is only text until you answer.', null);
      function decide(q, action) {
        if (action === 'decline') {
          var reason = h('input', { type: 'text', class: 'field', maxlength: '200', placeholder: 'Why (shown to the person)', 'aria-label': 'Reason' });
          reason.style.width = '100%';
          ask({ title: 'Decline ' + q.title + '?', body: 'The person sees the reason.', content: reason, confirm: 'Decline', danger: true }).then(function (yes) { if (yes) post(q, action, reason.value.trim()); });
          return;
        }
        post(q, action, '');
      }
      function post(q, action, reason) {
        api.post('admin/connection-requests', { id: q.id, action: action, reason: reason }).then(function (res) {
          if (res.integration) {
            var k = st.list.map(function (i) { return i.id; }).indexOf(res.integration.id);
            if (k >= 0) st.list[k] = res.integration; else st.list.push(res.integration);
          }
          toast(action === 'decline' ? 'Declined.' : res.published ? 'Approved and published.' : 'Approved as a draft: ' + (res.why || 'finish it in Servers.'));
          if (action === 'customize' && res.integration) { st.tab = 'servers'; st.open = res.integration.id; }
          return load().then(function () { if (action === 'customize' && res.integration) { st.tab = 'servers'; st.open = res.integration.id; draw(); } });
        }, fail);
      }
      var cards = st.reqs.map(function (q) {
        var buttons = q.status === 'waiting'
          ? h('span', { class: 'actions' },
              h('button', { class: 'btn primary', type: 'button', text: 'Approve for this person', onclick: function () { decide(q, 'approve_user'); } }),
              h('button', { class: 'btn', type: 'button', text: 'Approve for everyone', onclick: function () { decide(q, 'approve'); } }),
              h('button', { class: 'btn', type: 'button', text: 'Customize…', onclick: function () { decide(q, 'customize'); } }),
              h('button', { class: 'btn', type: 'button', text: 'Decline…', onclick: function () { decide(q, 'decline'); } }))
          : h('span', { class: 'sub', text: q.status + (q.scope ? ' (' + q.scope + ')' : '') + (q.reason ? ': ' + q.reason : '') });
        var what = q.url + (q.note ? ' · "' + q.note + '"' : '') + (q.signin && q.signin !== 'unknown' ? ' · they think the sign-in is ' + q.signin : '') + (q.docs ? ' · docs: ' + q.docs : '') + ' · ' + q.user + ' · ' + when(q.at);
        return h('div', { class: 'form-row' }, h('div', { class: 'what' }, h('b', { text: q.title }), h('span', { text: what })), h('div', null, buttons));
      });
      return card('Requests for a server', 'Plain text, as typed: nothing has contacted the address. Approving makes the server (or finds the one with that address), tests it, switches on the tools that the server calls read-only and publishes it for that person or everyone. If that is not enough it stays a draft.', cards);
    }

    return { root: root, refresh: load, dirty: function () { return false; }, destroy: function () {} };
  }

  window.AdminIntegrations = { make: viewIntegrations };
})();
