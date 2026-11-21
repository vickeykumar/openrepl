/*
 * The admin dashboard (admin.html). Plain JavaScript, no libraries.
 *
 * Everything the server sends comes from visitors or from the machine: feedback
 * text, shared code, email addresses, log lines. It is only ever put on the
 * page with textContent or setAttribute, never as HTML (see h() below), and the
 * page's Content-Security-Policy would stop an inline script anyway.
 */
(function () {
  'use strict';

  var BASE = location.pathname.replace(/admin\/?$/, '');
  var POLL_MS = 5000;
  var CSRF = 'openrepl-admin'; // the header every change must carry (server/admin_core.go)

  // ---- small DOM helpers -------------------------------------------------------

  var SVG_NS = 'http://www.w3.org/2000/svg';

  function append(el, kid) {
    if (kid === null || kid === undefined || kid === false) return;
    if (Array.isArray(kid)) { kid.forEach(function (k) { append(el, k); }); return; }
    el.appendChild(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }

  // h('button', {class: 'btn', onclick: fn}, 'Label', child, [more children])
  function h(tag, props) {
    var el = document.createElement(tag);
    Object.keys(props || {}).forEach(function (k) {
      var v = props[k];
      if (v === undefined || v === null || v === false) return;
      if (k === 'class') el.className = v;
      else if (k === 'text') el.textContent = v;
      else if (k === 'vars') Object.keys(v).forEach(function (n) { el.style.setProperty(n, v[n]); });
      else if (k.slice(0, 2) === 'on' && typeof v === 'function') el.addEventListener(k.slice(2), v);
      else if (k === 'value' || k === 'checked' || k === 'disabled' || k === 'selected') el[k] = v;
      else el.setAttribute(k, v === true ? '' : v);
    });
    for (var i = 2; i < arguments.length; i++) append(el, arguments[i]);
    return el;
  }

  function svg(tag, attrs) {
    var el = document.createElementNS(SVG_NS, tag);
    Object.keys(attrs || {}).forEach(function (k) { el.setAttribute(k, attrs[k]); });
    for (var i = 2; i < arguments.length; i++) if (arguments[i]) el.appendChild(arguments[i]);
    return el;
  }

  var ICONS = {
    overview: '<rect x="3" y="3" width="7" height="9" rx="1"/><rect x="14" y="3" width="7" height="5" rx="1"/><rect x="14" y="12" width="7" height="9" rx="1"/><rect x="3" y="16" width="7" height="5" rx="1"/>',
    workers: '<rect x="3" y="4" width="18" height="6" rx="2"/><rect x="3" y="14" width="18" height="6" rx="2"/><path d="M7 7h.01M7 17h.01"/>',
    sessions: '<path d="M4 17l6-6-6-6M12 19h8"/>',
    site: '<path d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6"/>',
    feedback: '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>',
    snippets: '<path d="M16 18l6-6-6-6M8 6l-6 6 6 6"/>',
    users: '<path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75"/>',
    health: '<path d="M22 12h-4l-3 9L9 3l-3 9H2"/>',
    jobs: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
    params: '<path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/>',
    logs: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6M16 13H8M16 17H8M10 9H8"/>',
    map: '<circle cx="5" cy="6" r="2"/><circle cx="19" cy="6" r="2"/><circle cx="12" cy="18" r="2"/><path d="M7 6h10M6 8l5 8M18 8l-5 8"/>',
    usage: '<path d="M3 20h18M6 16V9M11 16V5M16 16v-4M20 16V8"/>',
    traffic: '<path d="M3 17l6-6 4 4 8-8M15 7h6v6"/>',
    audit: '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>',
    apps: '<path d="M21 8l-9-5-9 5v8l9 5 9-5V8z M3 8l9 5 9-5 M12 13v8"/>',
    copy: '<rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>',
    close: '<path d="M18 6L6 18M6 6l12 12"/>',
    play: '<path d="M6 4l14 8-14 8z"/>',
    pause: '<rect x="6" y="5" width="4" height="14" rx="1"/><rect x="14" y="5" width="4" height="14" rx="1"/>'
  };

  // The icon paths are constants of this file, never server data.
  function icon(name) {
    var s = svg('svg', { viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': '2',
      'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true' });
    var tpl = document.createElement('template');
    tpl.innerHTML = '<svg xmlns="' + SVG_NS + '">' + (ICONS[name] || '') + '</svg>';
    Array.prototype.slice.call(tpl.content.firstChild.childNodes).forEach(function (n) { s.appendChild(n); });
    return s;
  }

  // ---- formatting --------------------------------------------------------------

  function plural(n, one, many) { return n + ' ' + (n === 1 ? one : many); }

  function fmtDur(sec) {
    sec = Math.max(0, Math.floor(sec));
    var d = Math.floor(sec / 86400), hh = Math.floor(sec % 86400 / 3600), m = Math.floor(sec % 3600 / 60), s = sec % 60;
    if (d) return d + 'd ' + hh + 'h';
    if (hh) return hh + 'h ' + m + 'm';
    if (m) return m < 10 ? m + 'm ' + s + 's' : m + 'm';
    return s + 's';
  }

  // active time as people say it: "48 min", "38 h 20 min"
  function fmtActive(sec) {
    sec = Math.max(0, Math.round(Number(sec) || 0));
    if (sec < 60) return sec ? sec + ' s' : '0 min';
    var m = Math.round(sec / 60);
    if (m < 60) return m + ' min';
    var hh = Math.floor(m / 60), r = m % 60;
    return r ? hh + ' h ' + r + ' min' : hh + ' h';
  }

  // a country code as its name ("IN" -> "India"); what the browser cannot name stays as it came
  function countryName(code) {
    if (!code || code === 'unknown') return 'Unknown';
    try { return new Intl.DisplayNames(['en'], { type: 'region' }).of(code) || code; } catch (e) { return code; }
  }

  function fmtMB(n) {
    n = Number(n) || 0;
    return n >= 1024 ? (n / 1024).toFixed(1) + ' GB' : n + ' MB';
  }

  function localTime(iso) {
    var t = Date.parse(iso);
    return isNaN(t) ? '' : new Date(t).toLocaleString();
  }

  // A time that keeps itself current: "3m ago", or "in 12m" when until is set.
  function when(iso, until) {
    if (!iso) return h('span', { class: 'sub', text: until ? 'never' : '–' });
    var el = h('span', { 'data-ts': iso, 'data-until': until ? '1' : '', title: localTime(iso) });
    tickOne(el);
    return el;
  }

  function tickOne(el) {
    var t = Date.parse(el.getAttribute('data-ts'));
    if (isNaN(t)) return;
    var s = (el.getAttribute('data-until') ? t - Date.now() : Date.now() - t) / 1000;
    var text;
    if (el.getAttribute('data-until')) text = s <= 0 ? 'expired' : 'in ' + fmtDur(s);
    else text = s < 5 ? 'just now' : fmtDur(s) + ' ago';
    if (el.textContent !== text) el.textContent = text;
  }

  setInterval(function () {
    Array.prototype.forEach.call(document.querySelectorAll('[data-ts]'), tickOne);
    tickLive();
  }, 1000);

  function pill(text, tone) { return h('span', { class: 'pill ' + (tone || ''), text: text }); }
  function mono(text) { return h('span', { class: 'mono', text: text }); }

  function stateTone(state) {
    return { ONLINE: 'ok', DRAINING: 'warn', SYNCING: 'info', OFFLINE: 'fail' }[state] || '';
  }

  // ---- talking to the server ---------------------------------------------------

  var signedOutShown = false;

  function request(method, path, body) {
    var opts = { method: method, credentials: 'same-origin', headers: { Accept: 'application/json' } };
    if (method !== 'GET') {
      opts.headers['X-Requested-With'] = CSRF;
      if (body !== undefined) {
        opts.headers['Content-Type'] = 'application/json';
        opts.body = JSON.stringify(body);
      }
    }
    return fetch(BASE + path, opts).then(function (r) {
      var json = (r.headers.get('Content-Type') || '').indexOf('json') >= 0;
      return (json ? r.json() : r.text()).then(function (data) {
        if (r.ok) return data;
        if (r.status === 401) showSignedOut();
        var msg = typeof data === 'string' ? data.trim() : (data && data.error);
        var err = new Error(msg || 'The server answered ' + r.status + '.');
        err.status = r.status;
        throw err;
      });
    });
  }

  var api = {
    get: function (path) { return request('GET', path); },
    post: function (path, body) { return request('POST', path, body === undefined ? {} : body); }
  };

  function showSignedOut() {
    if (signedOutShown) return;
    signedOutShown = true;
    stopPolling();
    var main = $('view');
    main.textContent = '';
    main.appendChild(h('div', { class: 'signed-out' },
      h('h1', { text: 'You are signed out' }),
      h('p', { text: 'Your session ended, or this account is not an admin. Sign in again as an admin to continue.' }),
      h('a', { class: 'btn primary', href: BASE || './', text: 'Go to the site' })));
    setLive('error', 'Signed out');
  }

  // ---- feedback to the admin: toasts and dialogs --------------------------------

  function $(id) { return document.getElementById(id); }

  function toast(msg, kind) {
    var t = h('div', { class: 'toast ' + (kind || 'ok'), text: msg });
    $('toasts').appendChild(t);
    setTimeout(function () { if (t.parentNode) t.parentNode.removeChild(t); }, kind === 'error' ? 8000 : 4500);
  }

  function fail(err) { toast(err && err.message ? err.message : String(err), 'error'); }

  // ask shows a dialog and resolves true if the admin confirms. content is an
  // optional node (a select, say) the caller reads afterwards.
  function ask(o) {
    return new Promise(function (resolve) {
      var dlg = $('dialog');
      dlg.className = o.wide ? 'wide' : '';
      dlg.textContent = '';
      var answer = false;
      var confirmBtn = h('button', { class: 'btn ' + (o.danger ? 'danger' : 'primary'), type: 'button', text: o.confirm || 'OK',
        onclick: function () {
          if (o.validate) { var problem = o.validate(); if (problem) { toast(problem, 'error'); return; } }
          answer = true; dlg.close();
        } });
      dlg.appendChild(h('h2', { text: o.title }));
      if (o.body) dlg.appendChild(typeof o.body === 'string' ? h('p', { text: o.body }) : o.body);
      if (o.content) dlg.appendChild(o.content);
      var cancelBtn = o.cancel === false ? null : h('button', { class: 'btn', type: 'button', text: o.cancel || 'Cancel', onclick: function () { dlg.close(); } });
      dlg.appendChild(h('div', { class: 'buttons' }, cancelBtn, confirmBtn));
      dlg.onclose = function () { dlg.onclose = null; resolve(answer); };
      dlg.showModal();
      // Enter must not confirm something destructive by accident
      (o.danger && cancelBtn ? cancelBtn : confirmBtn).focus();
    });
  }

  var drawer = null; // {back, panel, id, onclose}

  function closeDrawer() {
    if (!drawer) return;
    var d = drawer; drawer = null;
    d.back.remove(); d.panel.remove();
    document.removeEventListener('keydown', d.onkey);
    if (d.onclose) d.onclose();
  }

  function openDrawer(id, title, body, onclose) {
    closeDrawer();
    var back = h('div', { class: 'drawer-back', onclick: closeDrawer });
    var content = h('div', null, body);
    var panel = h('aside', { class: 'drawer', role: 'dialog', 'aria-label': title },
      h('header', null, h('h2', { text: title }),
        h('button', { class: 'icon-btn', type: 'button', 'aria-label': 'Close', onclick: closeDrawer }, icon('close'))),
      content);
    var onkey = function (e) { if (e.key === 'Escape') closeDrawer(); };
    document.addEventListener('keydown', onkey);
    document.body.appendChild(back);
    document.body.appendChild(panel);
    drawer = { back: back, panel: panel, content: content, id: id, onkey: onkey, onclose: onclose };
    panel.querySelector('button').focus();
    return drawer;
  }

  // ---- the log of the installs of an app, per server (LLD 18, section 13) ---------------------------------------------------------------

  var lastStoreData = null; // the last answer of GET /admin/store: the servers a log can be read on

  // linkToLog makes a server label open the log of that app on that server (a real button for the keyboard too).
  function linkToLog(el, app, node) {
    el.classList.add('loglink');
    el.setAttribute('role', 'button');
    el.setAttribute('tabindex', '0');
    el.setAttribute('title', 'Open the log of ' + app + ' on ' + node);
    el.addEventListener('click', function () { openLog(app, node); });
    el.addEventListener('keydown', function (e) { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); openLog(app, node); } });
    return el;
  }

  function two(n) { return (n < 10 ? '0' : '') + n; }
  function clock(ms) { var d = new Date(ms); return two(d.getHours()) + ':' + two(d.getMinutes()) + ':' + two(d.getSeconds()); }
  function lapse(s) { s = Math.max(0, Math.round(s)); return s < 90 ? s + ' s' : Math.floor(s / 60) + ' min ' + two(s % 60) + ' s'; }

  // the steps of an attempt, from its lines: each run of lines of one step (the output and the commands belong to the step before them)
  function logSteps(lines) {
    var out = [], cur = null;
    lines.forEach(function (l) {
      if (l.s === 'output' || l.s === 'command' || l.s === 'env') { if (cur) cur.last = l.t; return; }
      if (!cur || cur.name !== l.s) { cur = { name: l.s, first: l.t, last: l.t, bad: false }; out.push(cur); }
      cur.last = l.t; if (l.l === 'error') cur.bad = true;
    });
    for (var i = 0; i < out.length; i++) { out[i].end = i + 1 < out.length ? out[i + 1].first : out[i].last; }
    return out;
  }

  function logText(lines) {
    return lines.map(function (l) { return clock(l.t) + ' [' + l.s + '] ' + l.x; }).join('\n');
  }

  // what an admin pastes to ask for help: the app, the server, the attempt, the environment, the steps and the last lines
  function supportText(st) {
    var i = st.info || {}, lines = st.lines, steps = logSteps(lines);
    var env = lines.filter(function (l) { return l.s === 'env'; })[0];
    var t = ['App: ' + st.app + (i.version ? ' ' + i.version : '') + (i.manager ? ' (' + i.manager + ')' : '') + ' on ' + st.node,
      'Attempt #' + (i.n || '?') + ' · ' + (i.label || '') + ' · ' + (i.result || '?') + (i.start ? ' · started ' + clock(i.start * 1000) : '') + (i.end ? ' · took ' + lapse(i.end - i.start) : ''),
      env ? 'Environment: ' + env.x : 'Environment: not in the log', i.err ? 'Reason: ' + i.err : '', '', 'Steps:'];
    steps.forEach(function (s) { t.push('  ' + s.name + ' ' + lapse((s.end - s.first) / 1000) + (s.bad ? ' (error)' : '')); });
    t.push('', 'Last 40 lines:', logText(lines.slice(-40)));
    return t.filter(function (x, k) { return x !== '' || k > 0; }).join('\n');
  }

  function openLog(app, node0) {
    var st = { app: app, node: node0 || 'gateway', attempt: 0, next: 0, lines: [], info: null, attempts: [], follow: true, filter: 'all', q: '', timer: null, closed: false, error: '', found: false, loading: true };
    var nodes = ['gateway'];
    ((lastStoreData && lastStoreData.nodes) || []).forEach(function (n) { if (nodes.indexOf(n.node) < 0) nodes.push(n.node); });
    if (nodes.indexOf(st.node) < 0) nodes.push(st.node);
    var body = h('div', { class: 'logpanel' });
    var view = h('div', { class: 'logview', tabindex: '0', 'aria-label': 'The log' });
    var head = h('div', { class: 'loghead' }), side = h('div', { class: 'logside' }), bar = h('div', { class: 'logbar' }), foot = h('div', { class: 'logfoot' });
    var search = h('input', { type: 'search', placeholder: 'Search', 'aria-label': 'Search the log', class: 'logsearch', oninput: function () { st.q = search.value; drawLines(); } });
    var follow = h('input', { type: 'checkbox', checked: true, onchange: function () { st.follow = follow.checked; if (st.follow) view.scrollTop = view.scrollHeight; } });
    body.appendChild(head); body.appendChild(h('div', { class: 'logmain' }, side, h('div', { class: 'logright' }, bar, view, foot)));

    function stop() { if (st.timer) { clearTimeout(st.timer); st.timer = null; } }
    function reset() { st.next = 0; st.lines = []; st.error = ''; st.loading = true; stop(); load(); }

    function drawHead() {
      head.textContent = '';
      var i = st.info || {};
      var tone = i.result === 'installed' ? 'ok' : i.result === 'running' ? 'warn' : i.result ? 'bad' : '';
      head.appendChild(h('div', { class: 'loghead-row' }, h('b', { text: st.app }), i.version ? mono(i.version) : null, i.manager ? pill(i.manager) : null, i.result ? pill(i.result === 'running' && i.start ? 'running ' + lapse(Date.now() / 1000 - i.start) : i.result, tone) : null,
        h('span', { class: 'grow' }), h('span', { class: 'sub', text: 'a server\'s log is fetched when you open its tab' })));
      var tabsRow = h('div', { class: 'loghead-row' }, h('span', { class: 'sub', text: 'Server' }));
      nodes.forEach(function (n) { tabsRow.appendChild(h('button', { class: 'btn' + (n === st.node ? ' primary' : ''), type: 'button', text: n, onclick: function () { if (n !== st.node) { st.node = n; st.attempt = 0; st.attempts = []; st.info = null; drawHead(); drawSide(); reset(); } } })); });
      head.appendChild(tabsRow);
    }

    function drawSide() {
      side.textContent = '';
      side.appendChild(h('h3', { text: 'Attempts' }));
      if (!st.attempts.length) side.appendChild(h('p', { class: 'sub', text: st.loading ? 'Loading…' : 'No install has been logged on this server yet.' }));
      st.attempts.forEach(function (a) {
        var sel = (st.attempt === 0 ? a === st.attempts[0] : a.n === st.attempt);
        side.appendChild(h('button', { class: 'logattempt' + (sel ? ' on' : ''), type: 'button', onclick: function () { st.attempt = a.n; reset(); } },
          h('b', { text: '#' + a.n }), ' ' + a.label + ' · ' + clock(a.start * 1000) + ' · ', h('span', { class: a.result === 'installed' ? 'ok' : a.result === 'running' ? 'warn' : 'bad', text: a.result }),
          h('div', { class: 'sub', text: (a.version ? a.version + ' · ' : '') + (a.logGone ? 'the log is gone, only this line is left' : a.err ? a.err : (a.end ? lapse(a.end - a.start) : '')) })));
      });
      var steps = logSteps(st.lines);
      if (steps.length) {
        side.appendChild(h('h3', { text: 'This attempt' }));
        steps.forEach(function (s, k) {
          var done = k + 1 < steps.length || (st.info && st.info.result !== 'running');
          side.appendChild(h('div', { class: 'logstep' }, h('span', { class: s.bad ? 'bad' : done ? 'ok' : 'warn', text: (s.bad ? '✕ ' : done ? '✓ ' : '● ') + s.name }), h('span', { class: 'sub', text: lapse((s.end - s.first) / 1000) })));
        });
      }
    }

    function visible() {
      var q = st.q.toLowerCase();
      return st.lines.filter(function (l) {
        if (st.filter === 'steps' && (l.s === 'output' || l.s === 'command')) return false;
        if (st.filter === 'output' && l.s !== 'output' && l.s !== 'command') return false;
        if (st.filter === 'errors' && l.l !== 'error' && l.l !== 'warn') return false;
        return !q || (l.x + ' ' + l.s).toLowerCase().indexOf(q) >= 0;
      });
    }

    function drawLines() {
      view.textContent = '';
      var vs = visible();
      if (st.error) view.appendChild(h('div', { class: 'logmsg bad', text: st.error }));
      else if (!st.found && !st.loading) view.appendChild(h('div', { class: 'logmsg', text: st.info && st.info.logGone ? 'The log of this attempt is gone (the disk of the server was wiped). The summary is in the list.' : 'There is no log of this app on this server yet.' }));
      else if (st.loading && !vs.length) view.appendChild(h('div', { class: 'logmsg', text: 'Loading…' + (st.node !== 'gateway' ? ' (a server answers with its next heartbeat, a few seconds)' : '') }));
      vs.forEach(function (l) {
        view.appendChild(h('div', { class: 'logline lv-' + l.l },
          h('span', { class: 'lt', text: clock(l.t) }), ' ', h('span', { class: 'ls', text: '[' + l.s + ']' }), ' ', h('span', { class: 'lx', text: l.x })));
      });
      if (st.info && st.info.result === 'running') view.appendChild(h('div', { class: 'logmsg', text: '▮ live' }));
      if (st.follow) view.scrollTop = view.scrollHeight;
      foot.textContent = '';
      foot.appendChild(h('label', { class: 'logfollow' }, follow, ' Follow the end'));
      foot.appendChild(h('span', { class: 'sub', text: st.lines.length + ' lines' + (st.info && st.info.bytes ? ' · ' + Math.round(st.info.bytes / 1024) + ' KB' : '') + ' · paths shown as /store, tokens hidden' }));
    }

    function drawBar() {
      bar.textContent = '';
      [['all', 'All'], ['steps', 'Steps'], ['output', 'Output'], ['errors', 'Errors']].forEach(function (f) {
        bar.appendChild(h('button', { class: 'btn' + (st.filter === f[0] ? ' primary' : ''), type: 'button', text: f[1], onclick: function () { st.filter = f[0]; drawBar(); drawLines(); } }));
      });
      bar.appendChild(search);
      bar.appendChild(h('span', { class: 'grow' }));
      bar.appendChild(h('button', { class: 'btn primary', type: 'button', text: 'Copy for support', title: 'The app, the server, the environment, the steps and the last 40 lines', onclick: function () { copyText(supportText(st)); } }));
      bar.appendChild(h('button', { class: 'btn', type: 'button', text: 'Copy all', onclick: function () { copyText(logText(st.lines)); } }));
      bar.appendChild(h('button', { class: 'btn', type: 'button', text: 'Download .txt', onclick: function () {
        var url = URL.createObjectURL(new Blob([supportText(st) + '\n\nFull log:\n' + logText(st.lines) + '\n'], { type: 'text/plain' }));
        var a = h('a', { href: url, download: st.app + '-' + st.node + '-attempt-' + ((st.info && st.info.n) || 0) + '.txt' });
        document.body.appendChild(a); a.click(); a.remove(); setTimeout(function () { URL.revokeObjectURL(url); }, 2000);
      } }));
    }

    function load() {
      if (st.closed) return;
      var node = st.node, attempt = st.attempt, from = st.next;
      api.get('admin/store/log?app=' + encodeURIComponent(st.app) + '&node=' + encodeURIComponent(node) + '&attempt=' + attempt + '&from=' + from).then(function (r) {
        if (st.closed || node !== st.node || attempt !== st.attempt || from !== st.next) return; // the admin moved on: this answer is stale
        st.loading = false;
        if (r.error) { st.error = r.error; drawHead(); drawSide(); drawLines(); st.timer = setTimeout(load, 5000); return; }
        var c = r.chunk || {};
        st.error = '';
        st.attempts = c.attempts || st.attempts; st.info = c.info || st.info; st.found = !!c.found;
        if (c.found) { st.lines = st.lines.concat(c.lines || []); st.next = c.next; }
        drawHead(); drawSide(); drawLines();
        // a running install is read again (a live tail); a finished one is read to its end and then left
        if (c.found && (c.running || (c.lines || []).length)) st.timer = setTimeout(load, c.running ? (node === 'gateway' ? 2000 : 1500) : 300);
      }, function (e) { if (!st.closed) { st.loading = false; st.error = e.message || 'The log could not be read.'; drawLines(); st.timer = setTimeout(load, 8000); } });
    }

    drawHead(); drawSide(); drawBar(); drawLines();
    ask({ title: 'Install log: ' + app, content: body, cancel: false, confirm: 'Close' }).then(function () { st.closed = true; stop(); });
    var dlg = $('dialog'); dlg.classList.add('logs');
    load();
  }

  function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text).then(function () { toast('Copied'); }, fail);
    }
    var ta = h('textarea', { 'aria-hidden': 'true' });
    ta.value = text;
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand('copy'); toast('Copied'); } catch (e) { fail(e); }
    ta.remove();
  }

  function codeBlock(text) {
    return h('div', { class: 'copy-row' },
      h('pre', { class: 'code', text: text }),
      h('button', { class: 'icon-btn', type: 'button', title: 'Copy', 'aria-label': 'Copy to the clipboard', onclick: function () { copyText(text); } }, icon('copy')));
  }

  // ---- building blocks ---------------------------------------------------------

  function pageHead(title, sub, extra) {
    return h('div', { class: 'page-head' },
      h('div', null, h('h1', { text: title }), sub ? h('p', { text: sub }) : null),
      h('span', { class: 'grow' }), extra);
  }

  function stat(label, value, note, tone, href) {
    var body = [h('div', { class: 'label', text: label }), h('div', { class: 'value', text: String(value) }),
      note ? h('div', { class: 'note', text: note }) : null];
    var card = h('div', { class: 'card stat', 'data-tone': tone || '' }, body);
    if (!href) return card;
    return h('a', { href: href, class: 'stat-link' }, card);
  }

  // A button that reads the latest from the database. The dashboard keeps its big lists for a few
  // minutes (a scan costs a read per record on Firestore), so these views do not refresh by
  // themselves; load(true) asks the server for a new scan (at most one in half a minute).
  function refreshButton(load) {
    return h('button', { class: 'btn', type: 'button', text: 'Refresh', title: 'Read the latest from the database',
      onclick: function () { load(true).then(function () { toast('Refreshed'); }, fail); } });
  }
  function freshPath(path, fresh) { return path + (fresh ? (path.indexOf('?') < 0 ? '?' : '&') + 'fresh=1' : ''); }

  function emptyState(text) { return h('div', { class: 'empty', text: text }); }

  function sortRows(rows, cols, sort) {
    if (!sort) return rows;
    var col = cols.filter(function (c) { return c.key === sort.key; })[0];
    if (!col || !col.sort) return rows;
    var dir = sort.dir === 'asc' ? 1 : -1;
    return rows.slice().sort(function (a, b) {
      var x = col.sort(a), y = col.sort(b);
      if (x < y) return -dir;
      if (x > y) return dir;
      return 0;
    });
  }

  // table builds a table from column definitions: {key, label, num, sort(row), cell(row)}.
  function table(cols, rows, o) {
    o = o || {};
    var head = h('tr', null, cols.map(function (c) {
      var th = h('th', { scope: 'col', class: c.num ? 'num' : '' });
      if (c.sort && o.onSort) {
        var on = o.sort && o.sort.key === c.key;
        th.setAttribute('aria-sort', on ? (o.sort.dir === 'asc' ? 'ascending' : 'descending') : 'none');
        th.appendChild(h('button', { class: 'sort', type: 'button', onclick: function () { o.onSort(c.key); } },
          c.label, on ? (o.sort.dir === 'asc' ? ' ▲' : ' ▼') : ''));
      } else {
        th.textContent = c.label;
      }
      return th;
    }));
    function rowEl(row) {
      var tr = h('tr', { class: o.onRow ? 'click' : '' }, cols.map(function (c) {
        return h('td', { class: c.num ? 'num' : '' }, c.cell(row));
      }));
      if (o.onRow) {
        tr.setAttribute('tabindex', '0');
        tr.addEventListener('click', function (e) { if (!e.target.closest('button, a, input, select')) o.onRow(row); });
        tr.addEventListener('keydown', function (e) { if (e.key === 'Enter' && e.target === tr) o.onRow(row); });
      }
      return tr;
    }
    var holder = h('div', { class: 'paged' });
    var pkey = o.pageKey || cols.map(function (c) { return c.label; }).join('|');
    function draw() {
      var st = pageState(pkey), n = rows.length, size = st.size;
      var pages = Math.max(1, Math.ceil(n / size));
      if (st.page > pages) st.page = pages;
      var from = (st.page - 1) * size;
      var body = rows.slice(from, from + size).map(rowEl);
      if (!rows.length) body = [h('tr', null, h('td', { colspan: cols.length }, emptyState(o.empty || 'Nothing here yet.')))];
      holder.innerHTML = '';
      holder.appendChild(h('div', { class: 'table-wrap' }, h('table', null, h('thead', null, head), h('tbody', null, body))));
      if (n > PAGE_SIZES[0]) {
        holder.appendChild(pager(n, st, function () { draw(); var w = holder.getBoundingClientRect(); if (w.top < 0) holder.scrollIntoView({ block: 'start' }); }));
      }
    }
    draw();
    return holder;
  }

  // ---- paging: a long list shows one page at a time, with a pager at its foot -------------------------------------------------
  var PAGE_SIZES = [25, 50, 100];
  var pageStates = {};
  function pageState(key) {
    if (!pageStates[key]) {
      var size = 25;
      try { var v = parseInt(localStorage.getItem('openrepl.admin.pagesize'), 10); if (PAGE_SIZES.indexOf(v) >= 0) size = v; } catch (e) { /* storage may be blocked */ }
      pageStates[key] = { page: 1, size: size };
    }
    return pageStates[key];
  }
  // pager draws "Showing a to b of n", the page size, Previous, numbers and Next. st is {page, size}; change() is called after it moves.
  function pager(n, st, change, sizes) {
    sizes = sizes || PAGE_SIZES;
    var count = Math.max(1, Math.ceil(n / st.size));
    var bar = h('nav', { class: 'toolbar pager', 'aria-label': 'Pages' });
    var a = n ? (st.page - 1) * st.size + 1 : 0, b = Math.min(n, st.page * st.size);
    bar.appendChild(h('span', { class: 'sub', text: 'Showing ' + a + ' to ' + b + ' of ' + n }));
    var sel = h('select', { 'aria-label': 'Rows per page', onchange: function (ev) {
      st.size = parseInt(ev.target.value, 10); st.page = 1;
      try { localStorage.setItem('openrepl.admin.pagesize', String(st.size)); } catch (e) { /* ignore */ }
      change();
    } }, sizes.map(function (z) { var o = h('option', { value: String(z), text: z + ' per page' }); if (z === st.size) o.selected = true; return o; }));
    bar.appendChild(sel);
    bar.appendChild(h('span', { class: 'grow' }));
    function go(p) { return function () { st.page = p; change(); }; }
    bar.appendChild(h('button', { class: 'btn', type: 'button', text: '\u2190 Previous', disabled: st.page <= 1, onclick: go(st.page - 1) }));
    var lo = Math.max(1, Math.min(st.page - 3, count - 6)), hi = Math.min(count, lo + 6);
    for (var i = lo; i <= hi; i++) {
      bar.appendChild(h('button', { class: 'btn' + (i === st.page ? ' primary' : ''), type: 'button', text: String(i), 'aria-label': 'Page ' + i, 'aria-current': i === st.page ? 'page' : null, onclick: go(i) }));
    }
    bar.appendChild(h('button', { class: 'btn', type: 'button', text: 'Next \u2192', disabled: st.page >= count, onclick: go(st.page + 1) }));
    bar.appendChild(h('span', { class: 'sub', text: 'Page ' + st.page + ' of ' + count }));
    return bar;
  }

  function usageBar(used, max) {
    if (!max) return h('div', null, h('div', { class: 'bar-label', text: fmtMB(used) + ' in use, no limit' }));
    var p = Math.min(100, Math.round(used / max * 100));
    return h('div', null,
      h('div', { class: 'bar ' + (p >= 90 ? 'danger' : p >= 75 ? 'warn' : ''), vars: { '--p': p } }, h('i')),
      h('div', { class: 'bar-label', text: fmtMB(used) + ' of ' + fmtMB(max) + ' (' + p + '%)' }));
  }

  function kv(pairs) {
    var dl = h('dl', { class: 'kv' });
    pairs.forEach(function (p) {
      if (p === null) return;
      dl.appendChild(h('dt', { text: p[0] }));
      dl.appendChild(h('dd', null, p[1] === undefined || p[1] === null || p[1] === '' ? h('span', { class: 'sub', text: '–' }) : p[1]));
    });
    return dl;
  }

  function yesno(v) { return pill(v ? 'yes' : 'no', v ? 'ok' : ''); }

  // ---- live indicator and polling ----------------------------------------------

  var lastUpdate = 0, liveState = 'live', paused = false, timer = null, current = null, busy = false;
  var params = null; // /admin/gateway, loaded once and refreshed by the overview

  function setLive(state, text) {
    liveState = state;
    $('live-dot').setAttribute('data-state', state);
    if (text) $('live-text').textContent = text;
  }

  function tickLive() {
    if (paused || liveState === 'error' || !lastUpdate) return;
    var s = Math.round((Date.now() - lastUpdate) / 1000);
    $('live-text').textContent = s < 2 ? 'Live' : 'Updated ' + s + 's ago';
  }

  function stopPolling() { clearInterval(timer); timer = null; }

  function startPolling() {
    stopPolling();
    timer = setInterval(tick, POLL_MS);
  }

  function tick() {
    if (paused || document.hidden || !current || !current.poll || busy || signedOutShown) return;
    refreshCurrent();
  }

  function refreshCurrent() {
    if (!current || busy) return Promise.resolve();
    busy = true;
    var view = current;
    return Promise.resolve().then(function () { return view.refresh(); }).then(function () {
      if (view !== current) return;
      lastUpdate = Date.now();
      setLive(paused ? 'paused' : 'live');
      tickLive();
    }, function (err) {
      if (view !== current || signedOutShown) return;
      var network = err instanceof TypeError && /fetch|network|load failed/i.test(err.message);
      if (!network) console.error(err);
      setLive('error', network ? 'Connection lost, retrying…' : 'Error: ' + (err && err.message));
    }).then(function () { busy = false; });
  }

  $('pause').addEventListener('click', function () {
    paused = !paused;
    var b = $('pause');
    b.textContent = '';
    b.appendChild(icon(paused ? 'play' : 'pause'));
    b.setAttribute('aria-label', paused ? 'Resume automatic refresh' : 'Pause automatic refresh');
    b.title = b.getAttribute('aria-label');
    if (paused) setLive('paused', 'Paused');
    else { setLive('live', 'Live'); refreshCurrent(); }
  });

  document.addEventListener('visibilitychange', function () { if (!document.hidden) tick(); });

  // keep a ticker from replacing a form the admin is typing in
  function render(container, key, node) {
    if (container.__key === key) return false;
    container.__key = key;
    container.textContent = '';
    container.appendChild(node);
    return true;
  }

  // ---- views -------------------------------------------------------------------
  //
  // A view is {root, refresh(), poll, dirty()}. refresh() fetches what the view
  // shows and redraws it only when it changed.

  var POPULAR_COLOURS = [
    '#ffc0cb', '#008080', '#ff0000', '#ffd700', '#00ffff', '#40e0d0',
    '#ff7373', '#0000ff', '#ffa500', '#b0e0e6', '#7fffd4', '#c6e2ff',
    '#faebd7', '#800080', '#cccccc', '#fa8072', '#ffb6c1', '#333333',
    '#800000', '#00ff00', '#003366', '#c0c0c0', '#66cdaa', '#ff6666',
    '#666666', '#c39797', '#00ced1', '#ffdab9', '#ff00ff', '#008000',
    '#FE6A6B', '#088da5', '#c0d6e4', '#660066', '#0e2f44', '#808080',
    '#8b0000', '#ff7f50', '#990000', '#daa520', '#00ff7f', '#66cccc',
    '#8a2be2', '#81d8d0', '#3399ff', '#a0db8e', '#0bd800', '#ff4040',
    '#794044', '#cc0000', '#000080', '#3b5998', '#ccff00', '#999999',
    '#191970', '#31698a', '#6897bb', '#0099cc', '#ff4444', '#ff1493',
    '#6dc066',
  ];

  // The same arithmetic as ColorOfTheDay() in js/src/preprocessing.js. Every
  // visitor then gets a slightly lighter or darker shade of it.
  function colourOfTheDay() {
    var x = Math.floor(Date.now() / 86400000), n = POPULAR_COLOURS.length;
    for (var i = 0; i < 100; i++) x = (x ^ (x << 1) ^ (x >> 1)) % n;
    return POPULAR_COLOURS[Math.abs(x) % n];
  }

  // ---- overview

  function viewOverview() {
    var root = h('div'), box = h('div');
    root.appendChild(pageHead('Overview', 'How the site is doing right now.'));
    root.appendChild(box);
    var slow = 0, extra = { settings: null, stats: null, audit: null, feedback: null };

    // The numbers that move are fetched every few seconds; the rest, which
    // changes only when somebody does something, every half minute.
    function refresh() {
      var wantSlow = Date.now() - slow > 30000 || !extra.stats;
      var jobs = [api.get('admin/gateway'), api.get('admin/health')];
      if (wantSlow) jobs.push(api.get('admin/settings'), api.get('admin/stats'), api.get('admin/audit'), api.get('admin/feedback'));
      return Promise.all(jobs).then(function (r) {
        params = r[0];
        if (r.length > 2) {
          extra.settings = r[2]; extra.stats = r[3]; extra.audit = r[4]; extra.feedback = r[5];
          slow = Date.now();
          setUnread(r[5].unread);
        }
        var key = JSON.stringify([r[0].counts, r[0].runtime.goroutines, r[1], extra.settings.maintenance, extra.stats, extra.audit.entries.slice(0, 5), extra.feedback.unread]);
        // the uptime and times change every second on their own; they are not part of the key
        render(box, key, overview(r[0], r[1], extra.settings, extra));
      });
    }
    return { root: root, refresh: refresh, poll: true };
  }

  function overview(p, health, settings, extra) {
    var out = h('div');
    var days = (extra.stats && extra.stats.days) || [];
    var today = days.length ? days[days.length - 1] : { visitors: 0, terminals: 0 };
    var bad = health.checks.filter(function (c) { return c.status !== 'ok'; });
    var gateway = p.mode === 'gateway';

    if (settings.maintenance && settings.maintenance.enabled) {
      out.appendChild(h('div', { class: 'callout gap' },
        h('b', { text: 'Maintenance mode is on. ' }), 'Visitors cannot start terminals. ',
        h('a', { href: '#site', text: 'Change it' }), '.'));
    }

    var cards = h('div', { class: 'grid cards' });
    cards.appendChild(stat('Status', health.status === 'ok' ? 'Healthy' : health.status === 'warn' ? 'Check' : 'Problem',
      bad.length ? plural(bad.length, 'check needs', 'checks need') + ' a look' : 'All checks pass',
      health.status === 'ok' ? 'ok' : health.status === 'warn' ? 'warn' : 'danger', '#health'));
    cards.appendChild(stat('Terminals open', p.counts.terminals, fmtMB(p.counts.terminalWeightMB) + ' of REPL memory'));
    if (gateway) {
      cards.appendChild(stat('Sessions', p.counts.sessions, 'known to the gateway', '', '#sessions'));
      cards.appendChild(stat('Workers online', p.counts.workersOnline + ' / ' + p.counts.workersTotal,
        'including the gateway itself', p.counts.workersOnline < p.counts.workersTotal ? 'warn' : '', '#workers'));
    }
    cards.appendChild(stat('Visitors today', today.visitors, plural(today.terminals, 'terminal', 'terminals') + ' started today'));
    cards.appendChild(stat('Unread feedback', extra.feedback ? extra.feedback.unread : '–', extra.feedback ? plural(extra.feedback.feedback.length, 'message', 'messages') + ' in all' : '',
      extra.feedback && extra.feedback.unread ? 'warn' : '', '#feedback'));
    cards.appendChild(h('div', { class: 'card stat' }, h('div', { class: 'label', text: 'Uptime' }),
      h('div', { class: 'value' }, p.started ? uptimeEl(p.started) : '–'),
      h('div', { class: 'note', text: (p.version || 'dev') + (p.commit ? ' · ' + p.commit.slice(0, 8) : '') })));
    cards.appendChild(stat('Server memory', p.runtime.allocMB + ' MB', p.runtime.goroutines + ' goroutines · ' + p.runtime.cpus + ' CPUs'));
    out.appendChild(cards);

    var charts = h('div', { class: 'grid two' });
    charts.appendChild(h('div', { class: 'card' }, h('h2', { text: 'Terminals started per day' }),
      h('p', { class: 'sub', text: 'The last 30 days. A terminal is counted when it opens.' }), barChart(days)));
    charts.appendChild(h('div', { class: 'card' }, h('h2', { text: 'Terminals by language' }),
      h('p', { class: 'sub', text: 'The last 30 days.' }), languageBars((extra.stats && extra.stats.languages) || {})));
    out.appendChild(charts);

    var lower = h('div', { class: 'grid two' });
    var attention = h('div', { class: 'card' }, h('h2', { text: 'Needs attention' }));
    if (!bad.length) attention.appendChild(h('p', { class: 'sub', text: 'Every health check passes.' }));
    bad.slice(0, 6).forEach(function (c) {
      attention.appendChild(h('div', { class: 'health-item' }, pill(c.status === 'fail' ? 'Problem' : 'Check', c.status),
        h('div', null, h('b', { text: c.name }), h('p', { text: c.detail }))));
    });
    if (bad.length) attention.appendChild(h('a', { href: '#health', text: 'All checks →' }));
    lower.appendChild(attention);

    var recent = h('div', { class: 'card' }, h('h2', { text: 'Recent admin activity' }));
    var entries = (extra.audit && extra.audit.entries.slice(0, 5)) || [];
    if (!entries.length) recent.appendChild(h('p', { class: 'sub', text: 'No changes have been made yet.' }));
    entries.forEach(function (e) {
      recent.appendChild(h('div', { class: 'health-item' }, h('span', { class: 'sub' }, when(e.time)),
        h('div', null, h('b', { text: e.action }), h('p', { text: (e.detail ? e.detail + ' · ' : '') + e.admin }))));
    });
    if (entries.length) recent.appendChild(h('a', { href: '#audit', text: 'Full audit log →' }));
    lower.appendChild(recent);
    out.appendChild(lower);

    out.appendChild(quickLinks());
    return out;
  }

  function uptimeEl(startedISO) {
    var el = h('span', { 'data-started': startedISO });
    var set = function () {
      var t = Date.parse(startedISO);
      el.textContent = isNaN(t) ? '–' : fmtDur((Date.now() - t) / 1000);
    };
    set();
    var iv = setInterval(function () { if (!document.body.contains(el)) clearInterval(iv); else set(); }, 1000);
    return el;
  }

  function quickLinks() {
    var link = function (href, title, sub, blank) {
      return h('a', { href: href, target: blank ? '_blank' : null, rel: blank ? 'noopener' : null }, title, h('small', { text: sub }));
    };
    var links = [
      link('#feedback', 'Feedback inbox', 'Messages from visitors'),
      link(BASE + 'editblog.html', 'Blog editor', 'Write and edit posts', true),
      link(BASE + 'doc.html', 'Documentation', 'What visitors read', true),
      link(BASE + 'admin/gateway', 'Parameters (JSON)', 'Raw /admin/gateway', true),
      link(BASE + 'admin/health', 'Health (JSON)', 'Raw /admin/health', true)
    ];
    if (params && params.mode === 'gateway') {
      links.push(link(BASE + 'admin/workers', 'Workers (JSON)', 'Raw /admin/workers', true));
      links.push(link(BASE + 'admin/sessions', 'Sessions (JSON)', 'Raw /admin/sessions', true));
    }
    return h('div', { class: 'card' }, h('h2', { text: 'Quick links' }), h('div', { class: 'links' }, links));
  }

  function barChart(days) {
    var W = 560, H = 170, padL = 30, padB = 20, padT = 8;
    var max = Math.max.apply(null, days.map(function (d) { return d.terminals; }).concat([1]));
    var step = max <= 5 ? 1 : Math.pow(10, Math.floor(Math.log10(max)));
    var top = Math.ceil(max / step) * step;
    var plotW = W - padL - 6, plotH = H - padB - padT;
    var s = svg('svg', { class: 'chart', viewBox: '0 0 ' + W + ' ' + H, role: 'img',
      'aria-label': 'Terminals started per day over the last 30 days' });
    [0, 0.5, 1].forEach(function (f) {
      var y = padT + plotH * (1 - f);
      s.appendChild(svg('line', { class: 'axis', x1: padL, x2: W - 6, y1: y, y2: y }));
      var t = svg('text', { x: padL - 5, y: y + 3, 'text-anchor': 'end' });
      t.textContent = String(Math.round(top * f));
      s.appendChild(t);
    });
    var bw = plotW / Math.max(days.length, 1);
    days.forEach(function (d, i) {
      var bh = d.terminals ? Math.max(2, plotH * d.terminals / top) : 0;
      var rect = svg('rect', { class: 'bar-rect', x: padL + i * bw + 1.5, y: padT + plotH - bh, width: Math.max(bw - 3, 1), height: bh, rx: 2 });
      var title = svg('title');
      title.textContent = d.date + ': ' + plural(d.terminals, 'terminal', 'terminals') + ', ' + plural(d.visitors, 'visitor', 'visitors');
      rect.appendChild(title);
      s.appendChild(rect);
      if ((i % 7 === 0 && i < days.length - 4) || i === days.length - 1) {
        var lab = svg('text', { x: padL + i * bw + bw / 2, y: H - 5, 'text-anchor': i === 0 ? 'start' : i === days.length - 1 ? 'end' : 'middle' });
        lab.textContent = d.date.slice(5);
        s.appendChild(lab);
      }
    });
    return s;
  }

  function languageBars(totals) {
    var names = Object.keys(totals).sort(function (a, b) { return totals[b] - totals[a]; });
    if (!names.length) return emptyState('No terminals started yet.');
    var max = totals[names[0]] || 1;
    var box = h('div', { class: 'hbars' });
    names.slice(0, 10).forEach(function (n) {
      box.appendChild(h('div', null, h('span', { class: 'name', text: n, title: n }),
        h('div', { class: 'bar', vars: { '--p': Math.round(totals[n] / max * 100) } }, h('i')),
        h('span', { class: 'n', text: String(totals[n]) })));
    });
    if (names.length > 10) box.appendChild(h('p', { class: 'sub', text: '…and ' + (names.length - 10) + ' more languages.' }));
    return box;
  }

  // ---- workers

  // The largest weight the server accepts (server/node_weights.go maxNodeWeight).
  var MAX_NODE_WEIGHT = 1000;

  // nodeShares is what each node's share of new sessions would be with the weight of one of them changed: the
  // nodes that are online and have a weight above 0 share them out, as the gateway's pool does. weights maps a
  // node id to the weight to use instead of its own.
  function nodeShares(workers, weights) {
    var sum = 0, rows = [];
    workers.forEach(function (n) {
      var wt = weights && weights[n.id] !== undefined ? weights[n.id] : n.weight;
      var takes = n.state === 'ONLINE' && wt > 0;
      if (takes) sum += wt;
      rows.push({ id: n.id, weight: wt, takes: takes });
    });
    rows.forEach(function (r) { r.percent = r.takes && sum ? Math.round(1000 * r.weight / sum) / 10 : 0; });
    return { rows: rows, sum: sum };
  }

  // editWeight opens the dialog that changes the weight of a node. It resolves when it is done (saved, reset or
  // cancelled).
  function editWeight(node, workers) {
    var name = node.id === 'local' ? 'the gateway' : node.id;
    var input = h('input', { type: 'number', class: 'field weight-input', min: '0', max: String(MAX_NODE_WEIGHT), step: '1', value: String(node.weight), 'aria-label': 'New weight of ' + name });
    var preview = h('div', { class: 'weight-preview' });
    var note = h('div', { class: 'sub weight-note' });
    function current() {
      var raw = input.value.trim();
      var n = Number(raw);
      return raw !== '' && isFinite(n) && Math.floor(n) === n && n >= 0 && n <= MAX_NODE_WEIGHT ? n : null;
    }
    function bar(pct) { return h('div', { class: 'bar', vars: { '--p': pct } }, h('i')); }
    function update() {
      var n = current();
      preview.textContent = '';
      if (n === null) { note.textContent = 'A whole number from 0 to ' + MAX_NODE_WEIGHT + '.'; return; }
      var before = nodeShares(workers, null), after = nodeShares(workers, (function () { var o = {}; o[node.id] = n; return o; })());
      if (!after.sum) { note.textContent = 'No node would take new sessions with this weight. Keep at least one online node above 0.'; return; }
      note.textContent = n === node.weight ? 'The weight is not changed.' : '';
      preview.appendChild(h('div', { class: 'sub', text: 'Share of new sessions, before and after' }));
      after.rows.forEach(function (r, i) {
        var b = before.rows[i];
        preview.appendChild(h('div', { class: 'weight-row' + (r.id === node.id ? ' me' : '') },
          h('span', { class: 'weight-name', text: r.id === 'local' ? 'gateway' : r.id }),
          bar(r.percent),
          h('span', { class: 'weight-pct', text: (b.takes ? b.percent + '%' : 'none') + ' → ' + (r.takes ? r.percent + '%' : 'none') })));
      });
    }
    input.oninput = update;
    var reset = null;
    if (node.weightSet) {
      reset = h('button', { class: 'btn small', type: 'button', text: 'Back to its own (' + node.configuredWeight + ')',
        onclick: function () {
          api.post('admin/workers/' + encodeURIComponent(node.id) + '/weight', { weight: null }).then(function () {
            toast(name + ' uses its own weight (' + node.configuredWeight + ') again.');
            $('dialog').close();
            refreshCurrent();
          }, fail);
        } });
    }
    var content = h('div', null,
      h('div', { class: 'weight-edit' }, h('label', { for: 'weight-input', text: 'New weight' }), input,
        h('span', { class: 'sub', text: 'now ' + node.weight + (node.weightSet ? ', set here' : '') + ' · its own is ' + node.configuredWeight })),
      note, preview, reset ? h('div', { class: 'actions' }, reset) : null);
    input.id = 'weight-input';
    update();
    return ask({
      title: 'Change the weight of ' + name,
      body: 'New sessions are shared out in proportion to the weights. A change applies to the next new session; sessions that are running stay where they are.',
      content: content,
      confirm: 'Save weight',
      validate: function () {
        var n = current();
        if (n === null) return 'The weight must be a whole number from 0 to ' + MAX_NODE_WEIGHT + '.';
        if (n === node.weight) return 'The weight is not changed.';
        if (!nodeShares(workers, (function () { var o = {}; o[node.id] = n; return o; })()).sum) return 'No node would take new sessions with this weight.';
        return '';
      }
    }).then(function (yes) {
      if (!yes) return null;
      var n = current();
      // a big step is confirmed: the node goes to 0, or more than half of the share moves
      var before = nodeShares(workers, null), after = nodeShares(workers, (function () { var o = {}; o[node.id] = n; return o; })());
      var moved = 0;
      after.rows.forEach(function (r, i) { moved += Math.abs(r.percent - before.rows[i].percent); });
      var risky = n === 0 || moved / 2 > 50;
      var go = risky ? ask({
        title: 'Change the weight of ' + name + '?',
        body: n === 0 ? name + ' will take no new sessions (it keeps the ones it has).' : 'This moves more than half of the new sessions to other nodes.',
        confirm: 'Change it'
      }) : Promise.resolve(true);
      return go.then(function (ok) {
        if (!ok) return null;
        return api.post('admin/workers/' + encodeURIComponent(node.id) + '/weight', { weight: n }).then(function () {
          toast('The weight of ' + name + ' is ' + n + '.');
        }, fail);
      });
    });
  }

  function drainOrUndrain(w) {
    var action = w.state === 'DRAINING' ? 'undrain' : 'drain';
    return api.post('admin/workers/' + encodeURIComponent(w.id) + '/' + action).then(function () {
      toast(w.id + (action === 'drain' ? ' is draining: it keeps its sessions and takes no new ones.' : ' takes new sessions again.'));
    }, fail);
  }

  function reconnectWorker(w) {
    return ask({
      title: 'Reconnect ' + w.id + '?',
      body: 'The worker drops its connection and connects again by itself. Its open terminals close, and its sessions show "execution node is away" until it is back, usually a few seconds.',
      confirm: 'Reconnect'
    }).then(function (yes) {
      if (!yes) return;
      return api.post('admin/workers/' + encodeURIComponent(w.id) + '/reconnect').then(function () {
        toast(w.id + ' was told to reconnect.');
      }, fail);
    });
  }

  // ptraceNote says what a node's host answered about tracing programs: it is
  // what Debug and the assembly REPL (rappel) need. "" means it is not known.
  function ptraceBad(answer) { return !!answer && answer.indexOf('ok') !== 0; }

  // languagesCard is the Languages card of a node's drawer: for each language what
  // the node says, what an admin decided, and a choice. It keeps itself (the
  // drawer around it is redrawn every few seconds, which would close a menu).
  function languagesCard(node) {
    var box = h('div'), where = node === 'local' ? 'the gateway' : node;
    var intro = h('p', { class: 'sub', text: 'Default is what the worker declares. Off refuses new terminals of a language the worker can run; On takes one it did not declare, for example one you installed after starting it. Open terminals keep running.' });
    var note = h('div'), list = h('div', { class: 'node-langs' });
    box.appendChild(intro); box.appendChild(note); box.appendChild(list);

    function status(r, ptrace) {
      var pills = [];
      if (r.siteOff) pills.push(pill('off for the whole site', 'info'));
      else if (r.takes) pills.push(pill('takes terminals', 'ok'));
      else if (r.rule === 'off') pills.push(pill('switched off here', 'fail'));
      else if (!r.declared) pills.push(pill('not installed here', 'plain'));
      else pills.push(pill('node offline', 'plain'));
      if (r.rule === 'on' && !r.declared) pills.push(pill('on, not declared by the worker', 'info'));
      if (r.needsPtrace && ptraceBad(ptrace)) pills.push(pill('needs ptrace', 'warn'));
      return h('div', { class: 'actions status' }, pills);
    }

    function draw(reply) {
      note.textContent = '';
      if (ptraceBad(reply.ptrace)) {
        note.appendChild(h('p', { class: 'callout', text: 'This node\'s host cannot trace programs (' + reply.ptrace + '). The assembly REPL (rappel) cannot work here. Debug still works: it runs the program under QEMU and starts paused.' }));
      } else if (reply.ptrace) {
        note.appendChild(h('p', { class: 'sub' }, pill('ptrace works', 'ok')));
      }
      if (!reply.online) note.appendChild(h('p', { class: 'sub', text: 'This node is offline, so its own list of languages is not known. Your choices are kept.' }));
      list.textContent = '';
      reply.languages.forEach(function (r) {
        var sel = h('select', { class: 'field', 'aria-label': r.name + ' on ' + where,
          onchange: function () { set(r, sel); } },
          h('option', { value: 'default', text: 'Default (' + (r.declared ? 'on' : 'not installed') + ')' }),
          h('option', { value: 'off', text: 'Off' }),
          h('option', { value: 'on', text: 'On' }));
        sel.value = r.rule;
        list.appendChild(h('div', { class: 'node-lang' },
          h('div', { class: 'what' }, h('b', { text: r.name }), h('span', { class: 'sub', text: r.command || '' })),
          sel, status(r, reply.ptrace)));
      });
    }

    function load() {
      return api.get('admin/workers/' + encodeURIComponent(node) + '/languages').then(draw, fail);
    }

    function set(r, sel) {
      var rule = sel.value;
      sel.disabled = true;
      return api.post('admin/workers/' + encodeURIComponent(node) + '/languages', { language: r.value, rule: rule }).then(function (reply) {
        toast(r.name + (rule === 'off' ? ' is switched off on ' : rule === 'on' ? ' is switched on for ' : ' follows the list of ') + where + '.');
        draw(reply);
      }, function (err) { fail(err); return load(); });
    }

    load();
    return { el: box, load: load };
  }

  function viewWorkers() {
    var root = h('div'), box = h('div'), addBox = h('div');
    var data = null, sort = { key: 'id', dir: 'asc' }, openId = null;
    var topBox = null; // the part of the open drawer that is redrawn; the Languages card below it is not
    root.appendChild(pageHead('Workers', 'The gateway and the workers connected to it. New sessions are shared out in proportion to the weights.'));
    root.appendChild(box);
    root.appendChild(addBox);

    // the weight is a button that opens the dialog (a click on the row opens the drawer, so it must not reach it)
    function weightCell(w) {
      return h('div', { class: 'weight-cell' },
        h('button', { class: 'btn small', type: 'button', title: 'Change the weight of this node',
          onclick: function (e) { e.stopPropagation(); editWeight(w, data.workers).then(refreshCurrent); } }, String(w.weight) + ' \u270E'),
        w.weightSet ? pill('set here', 'info') : null,
        w.weightSet ? h('div', { class: 'sub', text: 'its own: ' + w.configuredWeight }) : null);
    }

    var cols = [
      { key: 'id', label: 'Node', sort: function (w) { return w.id; }, cell: function (w) {
        return h('div', null, h('b', { text: w.id === 'local' ? 'gateway (local)' : w.id }),
          h('div', { class: 'sub', text: w.id === 'local' ? 'runs sessions itself' : (w.version || '') }),
          ptraceBad(w.ptrace) ? pill('no ptrace', 'warn') : null); } },
      { key: 'state', label: 'State', sort: function (w) { return w.state; }, cell: function (w) { return pill(w.state.toLowerCase(), stateTone(w.state)); } },
      { key: 'weight', label: 'Weight', num: true, sort: function (w) { return w.weight; }, cell: function (w) { return weightCell(w); } },
      { key: 'mem', label: 'Memory', sort: function (w) { return w.usedMB; }, cell: function (w) { return usageBar(w.usedMB, w.maxMB); } },
      { key: 'terminals', label: 'Terminals', num: true, sort: function (w) { return w.terminals || 0; }, cell: function (w) { return String(w.terminals || 0); } },
      { key: 'sessions', label: 'Sessions', num: true, sort: function (w) { return w.sessions; }, cell: function (w) { return String(w.sessions); } },
      { key: 'picked', label: 'Picked', num: true, sort: function (w) { return w.picked; }, cell: function (w) {
        return h('div', null, h('b', { text: w.picked + ' · ' + w.pickedPercent + '%' }),
          h('div', { class: 'sub', text: w.weightPercent ? 'target ' + w.weightPercent + '%' : 'no new sessions' })); } },
      { key: 'actions', label: '', cell: function (w) {
        if (w.id === 'local') return h('span', { class: 'sub', text: 'own weight: --local-weight' });
        return h('div', { class: 'actions' },
          h('button', { class: 'btn small', type: 'button', text: w.state === 'DRAINING' ? 'Undrain' : 'Drain',
            title: w.state === 'DRAINING' ? 'Take new sessions again' : 'Keep its sessions, send it no new ones',
            onclick: function () { drainOrUndrain(w).then(refreshCurrent); } }),
          h('button', { class: 'btn small', type: 'button', text: 'Reconnect', onclick: function () { reconnectWorker(w).then(refreshCurrent); } })); } }
    ];

    function draw() {
      var rows = sortRows(data.workers, cols, sort);
      var since = data.pickedSince ? 'since the gateway started ' : '';
      var node = h('div', null,
        h('p', { class: 'sub', text: plural(data.workers.length, 'node', 'nodes') + ' · ' + plural(data.pickedTotal, 'session', 'sessions') + ' picked ' + since + '(' + localTime(data.pickedSince) + ')' }),
        table(cols, rows, { sort: sort, onSort: function (k) { sort = { key: k, dir: sort.key === k && sort.dir === 'asc' ? 'desc' : 'asc' }; box.__key = null; draw(); },
          onRow: function (w) { openWorker(w.id); }, empty: 'No nodes.' }));
      render(box, JSON.stringify([data, sort]), node);
      if (drawer && drawer.id === openId) {
        var w = data.workers.filter(function (x) { return x.id === openId; })[0];
        if (w) fillDrawer(w);
      }
    }

    function openWorker(id) {
      var w = data.workers.filter(function (x) { return x.id === id; })[0];
      if (!w) return;
      topBox = h('div');
      var langs = h('div', null, h('h3', { text: 'Languages on this node' }), languagesCard(id).el);
      openDrawer(id, id === 'local' ? 'gateway (local)' : id, h('div', null, topBox, langs), function () { openId = null; topBox = null; });
      openId = id; // after openDrawer: it closes the previous drawer, whose onclose clears this
      fillDrawer(w);
    }

    function fillDrawer(w) {
      var d = topBox;
      d.textContent = '';
      var offset = w.sync ? w.sync.clockOffsetMs : null;
      d.appendChild(h('div', { class: 'actions' }, pill(w.state.toLowerCase(), stateTone(w.state)),
        w.id === 'local' ? null : h('button', { class: 'btn small', type: 'button', text: w.state === 'DRAINING' ? 'Undrain' : 'Drain',
          onclick: function () { drainOrUndrain(w).then(refreshCurrent); } }),
        w.id === 'local' ? null : h('button', { class: 'btn small', type: 'button', text: 'Reconnect', onclick: function () { reconnectWorker(w).then(refreshCurrent); } })));
      d.appendChild(h('h3', { text: 'Node' }));
      d.appendChild(kv([
        ['Weight', h('span', null, String(w.weight), ' ', w.weightSet ? pill('set here', 'info') : null, ' ',
          h('button', { class: 'btn small', type: 'button', text: 'Change', onclick: function () { editWeight(w, data.workers).then(refreshCurrent); } }),
          w.weightSet ? h('span', { class: 'sub', text: ' its own is ' + w.configuredWeight }) : null)],
        w.version ? ['Version', w.version] : null,
        w.os ? ['System', w.os + ' / ' + w.arch] : null,
        w.remoteAddr ? ['Address', mono(w.remoteAddr)] : null,
        w.connectionId ? ['Connection', mono(w.connectionId)] : null,
        w.connected ? ['Connected', h('span', null, when(w.connected), ' ', h('span', { class: 'sub', text: '(' + localTime(w.connected) + ')' }))] : null,
        w.lastSeen ? ['Last heard from', when(w.lastSeen)] : null,
        w.id !== 'local' && w.connectionId ? ['Site rules', w.configCurrent ? pill('up to date', 'ok') : pill(w.configRev ? 'out of date (revision ' + w.configRev + ')' : 'not received yet', 'warn')] : null,
        ['Languages it declares', w.languages && w.languages.length ? h('span', { class: 'actions' }, w.languages.map(function (l) { return pill(l, 'plain'); })) : (w.id === 'local' ? 'all' : 'all it was started with')],
        w.ptrace ? ['Tracing programs', ptraceBad(w.ptrace) ? pill('no: ' + w.ptrace, 'warn') : pill('works', 'ok')] : null
      ]));
      d.appendChild(h('h3', { text: 'Load' }));
      d.appendChild(kv([
        ['Memory', usageBar(w.usedMB, w.maxMB)],
        ['Terminals', String(w.terminals || 0)],
        ['Sessions', String(w.sessions)],
        ['Sessions picked', w.picked + ' (' + w.pickedPercent + '% of all, target ' + (w.weightPercent ? w.weightPercent + '%' : 'none') + ')']
      ]));
      if (w.id !== 'local') {
        d.appendChild(h('h3', { text: 'Workspace sync' }));
        d.appendChild(w.sync ? kv([
          ['Homes kept in step', String(w.sync.homes)],
          ['Clock difference', h('span', null, (offset > 0 ? '+' : '') + offset + ' ms ', Math.abs(offset) > 2000 ? pill('too large', 'warn') : null)]
        ]) : h('p', { class: 'sub', text: params && params.gateway && params.gateway.workspaceSync ? 'No sync conversation with this worker right now.' : 'Workspace sync is off.' }));
      }
    }

    function addWorker() {
      var g = params && params.gateway;
      var card = h('details', { class: 'card' }, h('summary', null, h('b', { text: 'Add a worker' })));
      if (!g) return card;
      if (!g.workersEnabled) {
        card.appendChild(h('p', { class: 'callout', text: 'Workers are off. Start the gateway with --worker-token (or GOTTY_WORKER_TOKEN) to accept them.' }));
        return card;
      }
      var secure = location.protocol === 'https:';
      var wsURL = (secure ? 'wss://' : 'ws://') + location.host + (g.tunnelUrlPath ? (g.tunnelUrlPath[0] === '/' ? '' : '/') + g.tunnelUrlPath : '/api/tunnel');
      card.appendChild(h('p', { class: 'sub', text: 'On the new machine, with the same image and REPL setup as this server. Use the worker token you gave the gateway; it is not shown here.' }));
      var next = 'worker-' + ('0' + (data.workers.filter(function (w) { return w.id !== 'local'; }).length + 1)).slice(-2);
      card.appendChild(h('p', null, h('b', { text: 'Over HTTPS (the usual way)' })));
      card.appendChild(codeBlock('GOTTY_WORKER_TOKEN=<worker token> gotty -w --mode=worker --worker-id ' + next + ' \\\n  --worker-server ' + wsURL));
      if (!secure) card.appendChild(h('p', { class: 'callout info', text: 'This page is not on HTTPS, so the address above is ws://, which sends the token in clear text. Use it only on a test network.' }));
      if (g.tunnelAddr) {
        var port = g.tunnelAddr.slice(g.tunnelAddr.lastIndexOf(':') + 1);
        card.appendChild(h('p', null, h('b', { text: 'Over SSH (no HTTPS needed)' })));
        card.appendChild(codeBlock('GOTTY_WORKER_TOKEN=<worker token> gotty -w --mode=worker --worker-id ' + next + ' \\\n  --worker-server ssh://' + location.hostname + ':' + port + ' \\\n  --worker-hostkey \'' + (g.hostKeyFingerprint || '') + '\''));
        card.appendChild(h('p', { class: 'sub', text: 'The worker refuses a gateway whose key does not match this fingerprint.' }));
      } else if (g.hostKeyFingerprint) {
        card.appendChild(kv([['Tunnel host key', mono(g.hostKeyFingerprint)]]));
        card.appendChild(h('p', { class: 'sub', text: 'Start the gateway with --tunnel-addr to accept workers over SSH as well.' }));
      }
      return card;
    }

    function refresh() {
      return Promise.all([api.get('admin/workers'), params ? null : api.get('admin/gateway')]).then(function (r) {
        data = r[0];
        if (r[1]) params = r[1];
        draw();
        render(addBox, JSON.stringify([params.gateway, data.workers.length]), addWorker());
      });
    }
    return { root: root, refresh: refresh, poll: true };
  }

  // ---- sessions

  function viewSessions() {
    var root = h('div'), box = h('div');
    var data = null, workers = [], sort = { key: 'active', dir: 'desc' }, query = '', kind = 'all';

    var search = h('input', { type: 'search', placeholder: 'Search user, node or home', 'aria-label': 'Search sessions',
      oninput: function () { query = search.value.trim().toLowerCase(); box.__key = null; draw(); } });
    var filter = h('select', { 'aria-label': 'Filter sessions', onchange: function () { kind = filter.value; box.__key = null; draw(); } },
      [['all', 'All sessions'], ['user', 'Signed in'], ['guest', 'Guests'], ['open', 'With open terminals']].map(function (o) { return h('option', { value: o[0], text: o[1] }); }));
    root.appendChild(pageHead('Sessions', 'Who is placed on which node. Ending a session closes its terminals; the visitor\'s next request places it again. Files are not touched.'));
    root.appendChild(h('div', { class: 'toolbar' }, search, filter, h('span', { class: 'grow' }), h('span', { class: 'sub', id: 'sessions-count' })));
    root.appendChild(box);

    function label(s) {
      return s.uid ? (s.user || s.uid) : 'Guest ' + s.key.replace(/^g:/, '');
    }

    function endSession(s) {
      return ask({
        title: 'End the session of ' + label(s) + '?',
        body: (s.terminals ? plural(s.terminals, 'terminal', 'terminals') + ' will close. ' : '') + 'Their files stay. They are placed again on their next request.',
        confirm: 'End session', danger: true
      }).then(function (yes) {
        if (!yes) return;
        return api.post('admin/sessions/' + encodeURIComponent(s.key) + '/end').then(function (r) {
          toast('Session ended' + (r.terminalsClosed ? ' (' + plural(r.terminalsClosed, 'terminal', 'terminals') + ' closed).' : '.'));
        }, fail);
      }).then(refreshCurrent);
    }

    function moveSession(s) {
      var targets = workers.filter(function (w) { return w.state === 'ONLINE' && w.id !== s.backend; });
      if (!targets.length) { toast('No other node is online to move it to.', 'error'); return Promise.resolve(); }
      var pick = h('select', { class: 'field', 'aria-label': 'Move to' }, targets.map(function (w) { return h('option', { value: w.id, text: w.id === 'local' ? 'gateway (local)' : w.id }); }));
      return ask({
        title: 'Move the session of ' + label(s) + '?',
        body: 'It leaves ' + s.backend + ' and starts on the node below, which is first sent the gateway\'s copy of their files. Open terminals close, and running programs are lost. A file changed in the last few seconds may not have reached the gateway yet.',
        content: h('label', { class: 'text' }, h('span', { text: 'Move to' }), pick),
        confirm: 'Move session'
      }).then(function (yes) {
        if (!yes) return;
        return api.post('admin/sessions/' + encodeURIComponent(s.key) + '/move', { to: pick.value }).then(function () {
          toast('Session moved to ' + pick.value + '.');
        }, fail);
      }).then(refreshCurrent);
    }

    var cols = [
      { key: 'who', label: 'Session', sort: function (s) { return label(s).toLowerCase(); }, cell: function (s) {
        return h('div', null, h('b', { text: label(s) }), h('div', { class: 'sub mono', text: s.key })); } },
      { key: 'backend', label: 'Node', sort: function (s) { return s.backend; }, cell: function (s) { return s.backend === 'local' ? 'gateway (local)' : s.backend; } },
      { key: 'terminals', label: 'Terminals', num: true, sort: function (s) { return s.terminals; }, cell: function (s) { return String(s.terminals); } },
      { key: 'home', label: 'Home', sort: function (s) { return s.home || ''; }, cell: function (s) { return s.home ? mono(s.home) : h('span', { class: 'sub', text: '–' }); } },
      { key: 'created', label: 'Started', sort: function (s) { return s.created; }, cell: function (s) { return when(s.created); } },
      { key: 'active', label: 'Last active', sort: function (s) { return s.lastActive || s.created; }, cell: function (s) { return s.lastActive ? when(s.lastActive) : h('span', { class: 'sub', text: '–' }); } },
      { key: 'expires', label: 'Expires', sort: function (s) { return s.expires || '9'; }, cell: function (s) { return s.uid ? h('span', { class: 'sub', text: 'never' }) : when(s.expires, true); } },
      { key: 'actions', label: '', cell: function (s) {
        var sync = params && params.gateway && params.gateway.workspaceSync;
        return h('div', { class: 'actions' },
          h('button', { class: 'btn small', type: 'button', text: 'Move…', disabled: !sync, title: sync ? 'Place it on another node' : 'Moving a session needs workspace sync',
            onclick: function () { moveSession(s); } }),
          h('button', { class: 'btn small danger', type: 'button', text: 'End', onclick: function () { endSession(s); } })); } }
    ];

    function draw() {
      var rows = data.filter(function (s) {
        if (kind === 'user' && !s.uid) return false;
        if (kind === 'guest' && s.uid) return false;
        if (kind === 'open' && !s.terminals) return false;
        if (!query) return true;
        return [label(s), s.key, s.backend, s.home || ''].join(' ').toLowerCase().indexOf(query) >= 0;
      });
      $('sessions-count') && ($('sessions-count').textContent = plural(rows.length, 'session', 'sessions') + (rows.length !== data.length ? ' of ' + data.length : ''));
      render(box, JSON.stringify([rows, sort]), table(cols, sortRows(rows, cols, sort), {
        sort: sort, onSort: function (k) { sort = { key: k, dir: sort.key === k && sort.dir === 'asc' ? 'desc' : 'asc' }; box.__key = null; draw(); },
        empty: data.length ? 'No session matches.' : 'No sessions yet. A visitor appears here when their page loads.' }));
    }

    function refresh() {
      return Promise.all([api.get('admin/sessions'), api.get('admin/workers'), params ? null : api.get('admin/gateway')]).then(function (r) {
        data = r[0].sessions; workers = r[1].workers;
        if (r[2]) params = r[2];
        draw();
      });
    }
    return { root: root, refresh: refresh, poll: true };
  }

  // ---- site settings

  // ---- feedback

  function viewFeedback() {
    var root = h('div'), box = h('div');
    var data = null, show = 'unread';
    var filter = h('select', { 'aria-label': 'Which messages to show', onchange: function () { show = filter.value; box.__key = null; draw(); } },
      h('option', { value: 'unread', text: 'Unread' }), h('option', { value: 'all', text: 'All messages' }), h('option', { value: 'spam', text: 'Looks like spam' }));
    root.appendChild(pageHead('Feedback', 'Messages from the feedback form on the site.'));
    var spamBtn = h('button', { class: 'btn danger', type: 'button', hidden: true, onclick: function () { removeSpam(); } });
    root.appendChild(h('div', { class: 'toolbar' }, filter, h('span', { class: 'grow' }), h('span', { class: 'sub', id: 'fb-count' }), spamBtn,
      refreshButton(load), h('a', { class: 'btn', href: BASE + 'admin/feedback?format=csv', download: 'openrepl-feedback.csv', text: 'Export CSV' })));
    root.appendChild(box);

    function setRead(f, read) {
      return api.post('admin/feedback/' + f.id + '/' + (read ? 'read' : 'unread')).then(refreshCurrent, fail);
    }
    function remove(f) {
      return ask({ title: 'Delete this message?', body: 'From ' + (f.name || f.email || 'someone') + '. This cannot be undone.', confirm: 'Delete', danger: true }).then(function (yes) {
        if (!yes) return;
        return api.post('admin/feedback/' + f.id + '/delete').then(function () { toast('Message deleted.'); }, fail).then(refreshCurrent);
      });
    }

    function removeSpam() {
      var n = data.feedback.filter(function (f) { return f.spam; }).length;
      return ask({ title: 'Delete ' + plural(n, 'message', 'messages') + ' that look like spam?', body: 'Link tags, many links, casino and pill spam, and the same message sent again. This cannot be undone.', confirm: 'Delete', danger: true }).then(function (yes) {
        if (!yes) return;
        return api.post('admin/feedback/spam/delete').then(function (r) { toast(plural(r.deleted, 'message', 'messages') + ' deleted.'); }, fail).then(refreshCurrent);
      });
    }

    function draw() {
      var spamCount = data.feedback.filter(function (f) { return f.spam; }).length;
      spamBtn.hidden = spamCount === 0;
      spamBtn.textContent = 'Delete spam (' + spamCount + ')';
      var rows = data.feedback.filter(function (f) { return show === 'all' ? true : show === 'spam' ? !!f.spam : (!f.read && !f.spam); });
      $('fb-count').textContent = plural(data.unread, 'unread message', 'unread messages') + ' · ' + data.feedback.length + ' in all';
      var list = h('div', { class: 'stack' });
      rows.forEach(function (f) {
        list.appendChild(h('div', { class: 'card' },
          h('div', { class: 'toolbar' },
            f.read ? null : pill('new', 'info'),
            f.spam ? h('span', { title: 'Looks like spam: ' + f.spam }, pill('spam', 'warn')) : null,
            h('b', { text: f.name || 'Anonymous' }),
            f.email ? h('a', { href: 'mailto:' + f.email, text: f.email }) : null,
            h('span', { class: 'grow' }),
            h('span', { class: 'sub' }, when(f.time)),
            h('button', { class: 'btn small', type: 'button', text: f.read ? 'Mark unread' : 'Mark read', onclick: function () { setRead(f, !f.read); } }),
            h('button', { class: 'btn small danger', type: 'button', text: 'Delete', onclick: function () { remove(f); } })),
          h('div', { class: 'wrap-text', text: f.message || '(empty message)' })));
      });
      render(box, JSON.stringify([rows, show]), rows.length ? list : h('div', { class: 'card' }, emptyState(show === 'unread' ? 'No unread messages. 🎉' : show === 'spam' ? 'No spam.' : 'No feedback yet.')));
      setUnread(data.unread);
    }

    function load(fresh) { return api.get(freshPath('admin/feedback', fresh)).then(function (r) { data = r; draw(); }); }
    function refresh() { return load(false); }
    return { root: root, refresh: refresh, poll: false };
  }

  // ---- snippets

  function viewSnippets() {
    var root = h('div'), box = h('div');
    var data = null;
    root.appendChild(pageHead('Shared code', 'Snippets visitors saved with "Share code". Remove one that should not be public.'));
    root.appendChild(h('div', { class: 'toolbar' }, h('span', { class: 'grow' }), refreshButton(load)));
    root.appendChild(box);

    function view(s) {
      return api.get('admin/snippets/' + encodeURIComponent(s.id)).then(function (full) {
        var pre = h('pre', { class: 'code', text: full.code });
        return ask({ title: 'Snippet ' + s.id, wide: true, cancel: false, confirm: 'Close',
          body: h('p', { class: 'sub', text: full.lang + ' · ' + full.bytes + ' bytes · shared ' + localTime(full.created) }), content: pre });
      }, fail);
    }
    function remove(s) {
      return ask({ title: 'Delete snippet ' + s.id + '?', body: 'Its link will stop working. This cannot be undone.', confirm: 'Delete', danger: true }).then(function (yes) {
        if (!yes) return;
        return api.post('admin/snippets/' + encodeURIComponent(s.id) + '/delete').then(function () { toast('Snippet deleted.'); }, fail).then(refreshCurrent);
      });
    }

    var cols = [
      { key: 'id', label: 'Link', cell: function (s) { return h('a', { href: BASE + 's/' + s.id, target: '_blank', rel: 'noopener', class: 'mono', text: '/s/' + s.id }); } },
      { key: 'lang', label: 'Language', cell: function (s) { return pill(s.lang || '?', 'plain'); } },
      { key: 'created', label: 'Shared', cell: function (s) { return when(s.created); } },
      { key: 'expires', label: 'Expires', cell: function (s) { return s.expires ? when(s.expires) : h('span', { class: 'muted', text: 'not yet' }); } },
      { key: 'bytes', label: 'Size', num: true, cell: function (s) { return s.bytes + ' B'; } },
      { key: 'preview', label: 'Code', cell: function (s) { return h('div', { class: 'clip mono', text: s.preview.replace(/\s+/g, ' '), title: s.preview.slice(0, 200) }); } },
      { key: 'actions', label: '', cell: function (s) {
        return h('div', { class: 'actions' }, h('button', { class: 'btn small', type: 'button', text: 'View', onclick: function () { view(s); } }),
          h('button', { class: 'btn small danger', type: 'button', text: 'Delete', onclick: function () { remove(s); } })); } }
    ];

    function load(fresh) {
      return api.get(freshPath('admin/snippets', fresh)).then(function (r) {
        data = r;
        if (r.off) { render(box, 'off', h('div', { class: 'callout', text: 'The snippet database is not open on this server, so sharing is off.' })); return; }
        render(box, JSON.stringify(r.snippets), table(cols, r.snippets, { empty: 'Nobody has shared code yet.' }));
      });
    }
    function refresh() { return load(false); }
    return { root: root, refresh: refresh, poll: false };
  }

  // ---- users

  function viewUsers() {
    var root = h('div'), box = h('div');
    var data = null, query = '';
    var sort = { key: 'lastSignIn', dir: 'desc' }, show = 'all';
    var search = h('input', { type: 'search', placeholder: 'Search by name or email', 'aria-label': 'Search accounts',
      oninput: function () { query = search.value.trim().toLowerCase(); box.__key = null; draw(); } });
    var filter = h('select', { 'aria-label': 'Which accounts to show', onchange: function () { show = filter.value; box.__key = null; draw(); } },
      h('option', { value: 'all', text: 'All accounts' }), h('option', { value: 'live', text: 'Signed in now' }),
      h('option', { value: 'week', text: 'Signed in the last 7 days' }), h('option', { value: 'active7', text: 'Active in the last 7 days' }),
      h('option', { value: 'quiet', text: 'Not active in 30 days' }), h('option', { value: 'unrecorded', text: 'Sign-in not recorded' }));
    root.appendChild(pageHead('Users', 'Accounts that have signed in, and how long they spend on the site. Click a row for one person. Signing a user out ends their sessions; blocking also stops them signing in again.'));
    root.appendChild(h('div', { class: 'toolbar' }, search, filter, h('span', { class: 'grow' }), h('span', { class: 'sub', id: 'users-count' }), refreshButton(load)));

    // a time in milliseconds as the page tells times: "3h 5m ago", the exact time on hover; 0 is "not recorded"
    function stamp(ms, none) {
      if (!ms) return h('span', { class: 'sub', text: none || '–' });
      return when(new Date(ms).toISOString());
    }
    function sortBy(key) {
      sort = sort.key === key ? { key: key, dir: sort.dir === 'asc' ? 'desc' : 'asc' } : { key: key, dir: key === 'email' ? 'asc' : 'desc' };
      box.__key = null;
      draw();
    }
    root.appendChild(box);

    function act(u, action, title, body, confirm, done, danger) {
      return ask({ title: title, body: body, confirm: confirm, danger: danger }).then(function (yes) {
        if (!yes) return;
        return api.post('admin/users/' + encodeURIComponent(u.uid) + '/' + action).then(function () { toast(done); }, fail).then(refreshCurrent);
      });
    }

    var cols = [
      { key: 'email', label: 'Account', sort: true, cell: function (u) { return h('div', null, h('b', { text: u.email || '(no email)' }), h('div', { class: 'sub', text: u.name && u.name !== u.email ? u.name : '' })); } },
      { key: 'lastSignIn', label: 'Last signed in', sort: true, cell: function (u) {
        var live = u.sessions ? h('span', { title: plural(u.sessions, 'session', 'sessions') }, pill('signed in now', 'ok')) : null;
        if (!u.lastSignIn) return h('div', null, h('span', { class: 'sub', text: 'Not recorded' }), h('div', { class: 'sub', text: 'signed in before this was kept' }), live);
        return h('div', null, stamp(u.lastSignIn), h('div', { class: 'sub', text: localTime(new Date(u.lastSignIn).toISOString()) }), live);
      } },
      { key: 'activeSecs', label: 'Active time', sort: true, cell: function (u) {
        if (!u.visits) return h('span', { class: 'sub nowrap', text: 'Not recorded' });
        return h('div', { class: 'nowrap' }, h('b', { text: fmtActive(u.activeSecs) }), h('div', { class: 'sub', text: [u.topLang, u.lastVisitSecs ? 'last ' + fmtActive(u.lastVisitSecs) : ''].filter(Boolean).join(' · ') }));
      } },
      { key: 'days30', label: 'Last 30 days', sort: true, cell: function (u) {
        var peak = Math.max.apply(null, u.usage.bars.concat([1]));
        return h('div', { class: 'spark-row', title: u.days30 + ' of the last 30 days' },
          h('div', { class: 'spark', 'aria-hidden': 'true' }, u.usage.bars.map(function (m) { return h('i', { vars: { '--p': m ? Math.max(8, Math.round(m / peak * 100)) : 0 } }); })),
          h('span', { class: 'sub nowrap', text: u.days30 + ' d' }));
      } },
      { key: 'visits', label: 'Visits', sort: true, num: true, cell: function (u) { return u.visits ? String(u.visits) : h('span', { class: 'sub', text: '–' }); } },
      { key: 'status', label: 'Status', cell: function (u) {
        return h('span', { class: 'actions' }, u.admin ? pill('admin', 'info') : null, u.blocked ? pill('blocked', 'danger') : null, !u.admin && !u.blocked ? h('span', { class: 'sub', text: '–' }) : null); } },
      { key: 'actions', label: '', cell: function (u) {
        return h('div', { class: 'actions' },
          h('button', { class: 'btn small', type: 'button', text: 'Sign out everywhere', disabled: !u.sessions,
            onclick: function () { act(u, 'signout', 'Sign ' + (u.email || u.uid) + ' out?', 'Every browser they are signed in on is signed out. They can sign in again.', 'Sign out', 'Signed out.'); } }),
          u.blocked
            ? h('button', { class: 'btn small', type: 'button', text: 'Unblock', onclick: function () { act(u, 'unblock', 'Unblock ' + (u.email || u.uid) + '?', 'They can sign in again.', 'Unblock', 'Unblocked.'); } })
            : h('button', { class: 'btn small danger', type: 'button', text: 'Block', disabled: u.admin, title: u.admin ? 'An admin cannot be blocked' : '',
              onclick: function () { act(u, 'block', 'Block ' + (u.email || u.uid) + '?', 'They are signed out everywhere and cannot sign in until you unblock them.', 'Block', 'Blocked.', true); } })); } }
    ];

    function draw() {
      var weekAgo = Date.now() - 7 * 86400000;
      var rows = data.users.filter(function (u) {
        if (query && (u.email + ' ' + u.name).toLowerCase().indexOf(query) < 0) return false;
        return show === 'all' || (show === 'live' && u.sessions > 0) || (show === 'week' && u.lastSignIn >= weekAgo) || (show === 'unrecorded' && !u.lastSignIn) ||
          (show === 'active7' && u.lastActive >= weekAgo) || (show === 'quiet' && !(u.lastActive >= Date.now() - 30 * 86400000));
      });
      // an account with no time for the column is last, whichever way it is sorted
      rows.sort(function (a, b) {
        var x = a[sort.key], y = b[sort.key], dir = sort.dir === 'asc' ? 1 : -1;
        if (sort.key !== 'email') {
          x = x || 0; y = y || 0;
          if (!x !== !y) return !x ? 1 : -1;
          return x === y ? (a.email < b.email ? -1 : 1) : (x - y) * dir;
        }
        return String(x || '').localeCompare(String(y || '')) * dir;
      });
      $('users-count').textContent = plural(rows.length, 'account', 'accounts') + (rows.length !== data.users.length ? ' of ' + data.users.length : '');
      render(box, JSON.stringify([rows, sort]), table(cols, rows, { sort: sort, onSort: sortBy, onRow: userDrawer, empty: data.users.length ? 'No account matches.' : 'Nobody has signed in yet.' }));
    }
    function load(fresh) {
      return api.get(freshPath('admin/users', fresh)).then(function (r) {
        // the usage numbers sit in a row of their own; the table sorts on them as they are
        r.users.forEach(function (u) {
          u.usage = u.usage || { bars: [] };
          ['activeSecs', 'visits', 'days30', 'topLang', 'lastVisitSecs', 'lastActive', 'terminals'].forEach(function (k) { u[k] = u.usage[k]; });
        });
        data = r; draw();
      });
    }
    function refresh() { return load(false); }
    return { root: root, refresh: refresh, poll: false };
  }

  // one account's usage, in a side panel
  function userDrawer(u) {
    var body = h('div', null, h('p', { class: 'sub', text: 'Loading...' }));
    openDrawer('usage-' + u.uid, u.email || u.name || u.uid, body);
    api.get('admin/users/' + encodeURIComponent(u.uid) + '/usage').then(function (r) {
      var d = r.usage, tracked = d.visits > 0;
      var last = d.recent && d.recent.length ? d.recent[0] : null;
      var peak = Math.max.apply(null, d.dayMins.concat([1]));
      var node = h('div', { class: 'stack' },
        h('div', { class: 'grid three' },
          stat('Active time', tracked ? fmtActive(d.activeSecs) : '–', tracked ? 'since ' + d.since : 'counted from ' + d.since),
          stat('Last visit', last ? fmtActive(last.secs) : '–', last ? localTime(new Date(last.start).toISOString()) : 'none yet'),
          stat('Average visit', tracked ? fmtActive(d.avgVisitSecs) : '–', plural(d.visits, 'visit', 'visits'))),
        tracked ? h('div', null, h('h3', { text: 'Active minutes per day, last 30 days' }),
          h('div', { class: 'daybars', role: 'img', 'aria-label': 'Active minutes on each of the last 30 days' }, d.dayMins.map(function (m, i) {
            return h('i', { vars: { '--p': m ? Math.max(4, Math.round(m / peak * 100)) : 0 }, title: m + ' min, ' + (29 - i) + ' days ago' });
          })),
          h('div', { class: 'daybars-axis' }, h('span', { text: '30 days ago' }), h('span', { text: 'today' }))) : null,
        d.languages.length ? h('div', null, h('h3', { text: 'Languages (by active time)' }),
          h('div', { class: 'hbars' }, d.languages.map(function (l) {
            return h('div', null, h('span', { class: 'name', text: l.name, title: l.name }), h('div', { class: 'bar', vars: { '--p': l.share } }, h('i')), h('span', { class: 'n', text: fmtActive(l.count) }));
          }))) : null,
        h('div', null, h('h3', { text: 'Activity' }), kv([
          ['Terminals started', String(d.terminals || 0)],
          ['Genie answers this month', String(r.genieAnswers || 0)],
          ['Sign-ins', r.signIns ? String(r.signIns) : ''],
          ['First signed in', r.firstSignIn ? localTime(new Date(r.firstSignIn).toISOString()) : ''],
          ['Last signed in', r.lastSignIn ? localTime(new Date(r.lastSignIn).toISOString()) : ''],
          ['Devices', d.devices.map(function (x) { return x.name + ' ' + x.share + '%'; }).join(', ')]])),
        tracked ? h('div', null, h('h3', { text: 'Recent visits' }), table([
          { key: 'start', label: 'Started', cell: function (v) { return when(new Date(v.start).toISOString()); } },
          { key: 'secs', label: 'Active', cell: function (v) { return fmtActive(v.secs); } },
          { key: 'device', label: 'Device', cell: function (v) { return v.device || '–'; } },
          { key: 'country', label: 'From', cell: function (v) { return countryName(v.country); } },
          { key: 'source', label: 'Came from', cell: function (v) { return v.source || '–'; } }
        ], d.recent)) : h('p', { class: 'sub', text: 'Nothing has been recorded for this account yet. Time is counted while the person has the site open and is using it; an account that has not visited since ' + d.since + ' shows nothing.' }));
      body.textContent = '';
      body.appendChild(node);
    }, function (err) { body.textContent = ''; body.appendChild(h('p', { class: 'sub', text: err && err.message ? err.message : 'Could not load this account.' })); });
  }

  // ---- site usage: signed-in users and guests

  function viewTraffic() {
    var root = h('div'), box = h('div');
    var days = 30, mode = 'both', data = null;
    var modeBtns = {};
    var modeBox = h('div', { class: 'seg', role: 'group', 'aria-label': 'Who to show' }, [['both', 'Both'], ['signed', 'Signed in'], ['guests', 'Guests']].map(function (m) {
      var b = h('button', { class: 'seg-btn' + (m[0] === mode ? ' on' : ''), type: 'button', 'aria-pressed': m[0] === mode ? 'true' : 'false', text: m[1],
        onclick: function () {
          mode = m[0];
          Object.keys(modeBtns).forEach(function (k) { modeBtns[k].className = 'seg-btn' + (k === mode ? ' on' : ''); modeBtns[k].setAttribute('aria-pressed', k === mode ? 'true' : 'false'); });
          box.__key = null; if (data) draw();
        } });
      modeBtns[m[0]] = b;
      return b;
    }));
    var rangeSel = h('select', { 'aria-label': 'Period', onchange: function () { days = Number(rangeSel.value); box.__key = null; refresh(); } },
      h('option', { value: '7', text: 'Last 7 days' }), h('option', { value: '30', text: 'Last 30 days', selected: true }), h('option', { value: '90', text: 'Last 90 days' }));
    rangeSel.value = '30';
    root.appendChild(pageHead('Site usage', 'How people use the site, signed in and as guests. Guests are counted, never listed. Time counts only while a tab is in front and the person is typing, clicking or scrolling.'));
    root.appendChild(h('div', { class: 'toolbar' }, modeBox, rangeSel, h('span', { class: 'grow' }), refreshButton(load)));
    root.appendChild(box);

    function nm(n) { return Number(n || 0).toLocaleString('en-US'); }
    function list(items, label, fmt) {
      if (!items || !items.length) return emptyState('Nothing counted yet.');
      return h('div', { class: 'hbars wide' }, items.map(function (x) {
        return h('div', null, h('span', { class: 'name', text: fmt ? fmt(x.name) : x.name, title: fmt ? fmt(x.name) : x.name }), h('div', { class: 'bar', vars: { '--p': x.share } }, h('i')), h('span', { class: 'n', text: x.share + '%' }));
      }));
    }
    function pct(v) { return v < 0 ? '–' : v + '%'; }

    function draw() {
      var r = data;
      var peak = Math.max.apply(null, r.days.map(function (d) { return (mode !== 'guests' ? d.signed : 0) + (mode !== 'signed' ? d.guests : 0); }).concat([1]));
      var chart = h('div', { class: 'vbars', role: 'img', 'aria-label': 'Visitors per day' }, r.days.map(function (d) {
        var s = mode !== 'guests' ? d.signed : 0, g = mode !== 'signed' ? d.guests : 0;
        return h('div', { class: 'vbar', title: d.date + ': ' + plural(d.signed, 'signed-in user', 'signed-in users') + ', ' + plural(d.guests, 'guest', 'guests') },
          h('i', { class: 'g', vars: { '--p': g ? Math.max(2, Math.round(g / peak * 100)) : 0 } }), h('i', { class: 's', vars: { '--p': s ? Math.max(2, Math.round(s / peak * 100)) : 0 } }));
      }));
      var tracked = r.days.some(function (d) { return d.signed || d.guests; });
      var node = h('div', { class: 'stack' },
        !tracked ? h('p', { class: 'callout info', text: 'Nothing has been counted yet. Counting began on ' + r.since + ' and fills in as people use the site; pages must be reloaded once to start sending.' }) : null,
        h('div', { class: 'grid cards' },
          stat('Visitors per day', nm(r.avgSigned + r.avgGuests), nm(r.avgSigned) + ' signed in, ' + nm(r.avgGuests) + ' guests (average)'),
          stat('Active time per visit', fmtActive(r.avgVisitSecs), 'signed in ' + fmtActive(r.avgSignedVisitSecs) + ', guests ' + fmtActive(r.avgGuestVisitSecs)),
          stat('Guests who sign up', r.guestSignupPct ? r.guestSignupPct + '%' : '–', plural(r.newAccounts, 'new account', 'new accounts') + ' in this period'),
          stat('Back after 7 days', pct(r.back7Pct), 'of new signed-in users')),
        h('div', { class: 'card' },
          h('div', { class: 'toolbar' }, h('b', { text: 'Visitors per day' }), h('span', { class: 'grow' }),
            h('span', { class: 'legend' }, h('i', { class: 'dot s' }), 'Signed in', h('i', { class: 'dot g' }), 'Guests')),
          chart,
          h('div', { class: 'daybars-axis' }, h('span', { text: r.days[0].date.slice(5) }), h('span', { text: r.days[r.days.length - 1].date.slice(5) }))),
        h('div', { class: 'grid two' },
          h('div', { class: 'card' }, h('h2', { text: 'Languages, by active time' }), list(r.languages)),
          h('div', { class: 'card' }, h('h2', { text: 'Where visitors came from' }), list(r.sources))),
        h('div', { class: 'grid two' },
          h('div', { class: 'card' }, h('h2', { text: 'Devices' }), list(r.devices, null, function (n) { return n.charAt(0).toUpperCase() + n.slice(1); })),
          h('div', { class: 'card' }, h('h2', { text: 'Countries' }), list(r.countries, null, countryName))),
        h('div', { class: 'grid two' },
          h('div', { class: 'card' }, h('h2', { text: 'Guest journey' }),
            h('p', { class: 'sub', text: 'Guests are counted once for each day they visit, so a guest who comes on three days counts three times. No one is identified.' }),
            h('div', { class: 'hbars wide' }, r.funnel.map(function (f) {
              return h('div', null, h('span', { class: 'name', text: f.label, title: f.label }), h('div', { class: 'bar', vars: { '--p': f.pct } }, h('i')), h('span', { class: 'n', text: nm(f.count) }));
            }))),
          h('div', { class: 'card' }, h('h2', { text: 'Do new accounts come back?' }),
            h('p', { class: 'sub', text: 'Share of each week\'s new accounts that were active again within 1, 7 and 30 days. A dash means the week is too recent, or began before counting did.' }),
            table([
              { key: 'week', label: 'Joined, week of', cell: function (c) { return c.week; } },
              { key: 'size', label: 'Accounts', num: true, cell: function (c) { return String(c.size); } },
              { key: 'd1', label: 'Day 1', num: true, cell: function (c) { return pct(c.d1); } },
              { key: 'd7', label: 'Day 7', num: true, cell: function (c) { return pct(c.d7); } },
              { key: 'd30', label: 'Day 30', num: true, cell: function (c) { return pct(c.d30); } }
            ], r.cohorts, { empty: 'No accounts have joined yet.' }))));
      render(box, JSON.stringify([r, mode]), node);
    }
    function load(fresh) { return api.get(freshPath('admin/usage?days=' + days, fresh)).then(function (r) { data = r; draw(); }); }
    function refresh() { return load(false); }
    return { root: root, refresh: refresh, poll: false };
  }

  // ---- health

  function viewHealth() {
    var root = h('div'), box = h('div');
    root.appendChild(pageHead('Health', 'Checks of this server and its fleet. Nothing here shows a secret, only whether it is set.'));
    root.appendChild(box);
    function refresh() {
      return api.get('admin/health').then(function (r) {
        var node = h('div', { class: 'card' }, h('div', { class: 'toolbar' }, pill(r.status === 'ok' ? 'All good' : r.status === 'warn' ? 'Needs a look' : 'Problem', r.status)),
          r.checks.map(function (c) {
            return h('div', { class: 'health-item' }, pill(c.status === 'ok' ? 'OK' : c.status === 'warn' ? 'Check' : 'Problem', c.status),
              h('div', null, h('b', { text: c.name }), h('p', { text: c.detail })));
          }));
        render(box, JSON.stringify(r), node);
      });
    }
    return { root: root, refresh: refresh, poll: true };
  }

  // ---- parameters

  function viewParams() {
    var root = h('div'), box = h('div');
    root.appendChild(pageHead('Parameters', 'How this server was started. Read-only. Secrets are never shown, only whether they are set.'));
    root.appendChild(box);
    function section(title, pairs) { return h('div', { class: 'card' }, h('h2', { text: title }), kv(pairs)); }
    function refresh() {
      return api.get('admin/gateway').then(function (p) {
        params = p;
        var g = p.gateway, c = p.config;
        var groups = h('div', { class: 'grid two' },
          section('Build', [['Version', p.version || 'dev'], ['Commit', p.commit ? mono(p.commit) : null], ['Go', p.goVersion], ['System', p.os + ' / ' + p.arch],
            ['Mode', pill(p.mode, 'info')], ['Started', h('span', null, when(p.started), ' ', h('span', { class: 'sub', text: '(' + localTime(p.started) + ')' }))]]),
          section('Listening', [['Address', mono(p.listen.address + ':' + p.listen.port)], ['TLS', yesno(p.listen.tls)], ['Basic auth', yesno(p.listen.basicAuth)],
            ['Max connections', p.limits.maxConnection ? String(p.limits.maxConnection) : 'no limit'], ['Clients may write', yesno(p.limits.permitWrite)],
            ['Idle timeout', p.limits.timeoutSeconds ? p.limits.timeoutSeconds + ' s' : 'none']]),
          g ? section('Gateway', [['Gateway\'s own weight', String(g.localWeight)], ['Workers accepted', yesno(g.workersEnabled)], ['Tunnel path', mono(g.tunnelPath)],
            g.tunnelAddr ? ['Tunnel (SSH) address', mono(g.tunnelAddr)] : null, g.hostKeyFingerprint ? ['Tunnel host key', mono(g.hostKeyFingerprint)] : null,
            ['Workspace sync', yesno(g.workspaceSync)], g.relocateAfter ? ['Relocate after', g.relocateAfter] : null, g.syncStateDir ? ['Sync records in', mono(g.syncStateDir)] : null]) : null,
          section('Configuration', [['Environment', pill(c.env, c.env === 'dev' ? 'warn' : 'ok')],
            ['Env file', c.envFile.path ? h('span', null, mono(c.envFile.path), h('div', { class: 'sub', text: c.envFile.loaded + ' loaded, ' + c.envFile.kept + ' kept from the environment' + (c.envFile.ignored && c.envFile.ignored.length ? ', ignored: ' + c.envFile.ignored.join(', ') : '') })) : 'none'],
            ['Git-config file', c.gitConfigFile ? mono(c.gitConfigFile) : 'none'],
            ['Firebase project', h('span', null, mono(c.firebase.projectId || '?'), ' ', pill(c.firebase.custom ? 'from environment' : 'built in', c.firebase.custom ? 'info' : 'plain'))],
            ['Admin accounts', String(c.admins)], ['OpenAI key', yesno(c.openaiKeySet)], ['Public host', mono(c.host)]]),
          section('Runtime', [['Goroutines', String(p.runtime.goroutines)], ['Memory in use', p.runtime.allocMB + ' MB'], ['Memory from the system', p.runtime.sysMB + ' MB'], ['CPUs', String(p.runtime.cpus)]]));
        render(box, JSON.stringify(p), groups);
      });
    }
    return { root: root, refresh: refresh, poll: false };
  }

  // ---- logs

  function viewLogs() {
    var root = h('div'), box = h('div');
    var q = '', errorsOnly = false, rows = '200', follow = true, typing = null;
    var search = h('input', { type: 'search', placeholder: 'Search the log', 'aria-label': 'Search the log',
      oninput: function () { clearTimeout(typing); typing = setTimeout(function () { q = search.value.trim(); load(); }, 300); } });
    var errs = h('input', { type: 'checkbox', onchange: function () { errorsOnly = errs.checked; load(); } });
    var count = h('select', { 'aria-label': 'How many lines', onchange: function () { rows = count.value; load(); } },
      ['100', '200', '500', '1000'].map(function (n) { return h('option', { value: n, text: 'Last ' + n + ' lines', selected: n === rows }); }));
    var followBox = h('input', { type: 'checkbox', checked: true, onchange: function () { follow = followBox.checked; if (follow) load(); } });
    root.appendChild(pageHead('Log', 'The end of this server\'s log, with keys, tokens, cookies and email addresses masked.'));
    root.appendChild(h('div', { class: 'toolbar' }, search, count,
      h('label', null, errs, ' Errors only'), h('label', null, followBox, ' Follow'),
      h('span', { class: 'grow' }),
      h('a', { class: 'btn', href: BASE + 'admin/logs?download=1', download: 'openrepl-gotty-log.txt', text: 'Download log', title: 'The whole log file, with keys, tokens, cookies and email addresses masked' }),
      h('button', { class: 'btn', type: 'button', text: 'Refresh', onclick: function () { load(); } })));
    root.appendChild(box);

    function load() {
      var url = 'admin/logs?lines=' + rows + (q ? '&q=' + encodeURIComponent(q) : '') + (errorsOnly ? '&level=error' : '');
      return api.get(url).then(function (r) {
        var pre = h('pre', { class: 'log', tabindex: '0', 'aria-label': 'Log lines' });
        if (!r.lines.length) pre.appendChild(h('span', { text: r.note || 'No lines match.' }));
        r.lines.forEach(function (l) {
          pre.appendChild(h('span', { class: /\b(error|failed|panic|fatal)\b/i.test(l) ? 'err' : /\bwarn(ing)?\b/i.test(l) ? 'warn' : '', text: l }));
          pre.appendChild(document.createTextNode('\n'));
        });
        if (render(box, JSON.stringify([r.lines, r.truncated]), h('div', null, r.truncated ? h('p', { class: 'sub', text: 'Older lines are cut off; search or raise the number of lines to see more.' }) : null, pre)) && follow) {
          pre.scrollTop = pre.scrollHeight;
        }
      }, fail);
    }
    function refresh() { return follow ? load() : Promise.resolve(); }
    return { root: root, refresh: refresh, poll: true };
  }

  // ---- Genie log: the agent's failed steps and the end of each task (server/genie_log.go), no text of the user's

  function viewGenieLog() {
    var root = h('div'), box = h('div');
    var f = { days: '7', model: '', event: '', action: '', q: '' }, typing = null, lastRaw = null;
    var days = h('select', { 'aria-label': 'Period', onchange: function () { f.days = days.value; load(); } },
      [['1', 'Last day'], ['7', 'Last 7 days'], ['30', 'Last 30 days']].map(function (o) { return h('option', { value: o[0], text: o[1], selected: o[0] === f.days }); }));
    var model = h('select', { 'aria-label': 'Model', onchange: function () { f.model = model.value; load(); } }, h('option', { value: '', text: 'Model: all' }));
    var event = h('select', { 'aria-label': 'Event', onchange: function () { f.event = event.value; load(); } },
      [['', 'Event: all'], ['step_failed', 'Failed steps'], ['stuck', 'Stuck'], ['retry', 'Retries'], ['need_tools', 'More actions asked'], ['task_end', 'Task ends']].map(function (o) { return h('option', { value: o[0], text: o[1] }); }));
    var search = h('input', { type: 'search', placeholder: 'Search the reasons', 'aria-label': 'Search the reasons',
      oninput: function () { clearTimeout(typing); typing = setTimeout(function () { f.q = search.value.trim(); load(); }, 300); } });
    var dl = h('a', { class: 'btn', href: BASE + 'admin/genie-log?download=1&days=7', download: 'openrepl-genie-events.jsonl', text: 'Download whole log' });
    var copy = h('button', { class: 'btn', type: 'button', text: 'Copy for support', onclick: function () {
      var lines = (lastRaw && lastRaw.events || []).slice(0, 50).map(function (e) { return JSON.stringify(e); }).join('\n');
      var done = function () { toast('Copied the newest ' + Math.min(50, (lastRaw && lastRaw.events || []).length) + ' events'); };
      if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(lines).then(done, function () { toast('Could not copy'); });
      else toast('Could not copy');
    } });
    root.appendChild(pageHead('Genie log', 'What the agent reports on the free plan: failed steps, stuck tasks and how each task ended. It holds the model, the action, the file extension, a short reason with paths and quoted text taken out, and counts. Never the user\'s name, messages, files or terminal text.'));
    root.appendChild(h('div', { class: 'toolbar' }, days, model, event, search, h('span', { class: 'grow' }), copy, dl, refreshButton(function () { return load(); })));
    root.appendChild(box);

    function n(x) { return Number(x || 0).toLocaleString('en-US'); }
    function tile(label, value) { return h('div', { class: 'card' }, h('div', { class: 'sub', text: label }), h('b', { text: value })); }
    var cols = [
      { key: 'model', label: 'Model', cell: function (r) { return mono(r.model); } },
      { key: 'tasks', label: 'Tasks', num: true, cell: function (r) { return n(r.tasks); } },
      { key: 'done', label: 'Done', num: true, cell: function (r) { return r.tasks ? r.donePct + '%' : '–'; } },
      { key: 'stuck', label: 'Stuck', num: true, cell: function (r) { return n(r.stuck); } },
      { key: 'fail', label: 'Failed steps', num: true, cell: function (r) { return n(r.failures); } },
      { key: 'action', label: 'Top failing action', cell: function (r) { return r.topAction ? mono(r.topAction) : h('span', { class: 'sub', text: '–' }); } },
      { key: 'reason', label: 'Top reason', cell: function (r) { return h('span', { class: 'wrap-text', text: r.topReason || '–' }); } }
    ];
    var ecols = [
      { key: 't', label: 'When', cell: function (e) { return h('div', null, when(e.t), h('div', { class: 'sub', text: localTime(e.t) })); } },
      { key: 'event', label: 'Event', cell: function (e) { return pill(e.event, e.event === 'stuck' ? 'warn' : e.event === 'task_end' && e.outcome === 'done' ? 'ok' : 'plain'); } },
      { key: 'model', label: 'Model', cell: function (e) { return mono(e.model); } },
      { key: 'action', label: 'Action', cell: function (e) { return e.action ? h('span', null, mono(e.action), e.ext ? ' .' + e.ext : null) : ''; } },
      { key: 'step', label: 'Step', num: true, cell: function (e) { return e.step || ''; } },
      { key: 'detail', label: 'Reason or outcome', cell: function (e) {
        var text = e.event === 'task_end' ? e.outcome + (e.steps ? ' \u00b7 ' + e.steps + ' steps' : '') + (e.secs ? ' \u00b7 ' + e.secs + ' s' : '') + (e.tokens ? ' \u00b7 ' + n(e.tokens) + ' tokens' : '')
          : e.event === 'stuck_answer' ? 'the user chose: ' + e.choice + (e.rule ? ' (' + e.rule + ')' : '')
          : e.event === 'stuck' ? (e.rule ? e.rule + ': ' : '') + (e.reason || '')
          : (e.code ? e.code + ': ' : '') + (e.reason || '');
        return h('span', null, h('span', { class: 'wrap-text', text: text }), e.trail ? h('div', { class: 'sub', text: 'after: ' + e.trail.split(',').slice(-5).join(' \u2192 ') }) : null);
      } }
    ];
    var codeCols = [
      { key: 'code', label: 'Why a step failed', cell: function (r) { return mono(r.code); } },
      { key: 'n', label: 'Times', num: true, cell: function (r) { return n(r.n); } },
      { key: 'action', label: 'Most often in', cell: function (r) { return r.topAction ? mono(r.topAction) : h('span', { class: 'sub', text: '\u2013' }); } }
    ];
    function load() {
      var url = 'admin/genie-log?days=' + f.days + (f.model ? '&model=' + encodeURIComponent(f.model) : '') + (f.event ? '&event=' + f.event : '') + (f.q ? '&q=' + encodeURIComponent(f.q) : '');
      dl.setAttribute('href', BASE + 'admin/genie-log?download=1&days=' + f.days);
      return api.get(url).then(function (r) {
        lastRaw = r;
        // the model list follows what the log holds
        var have = {}; Array.prototype.forEach.call(model.options, function (o) { have[o.value] = true; });
        r.models.forEach(function (m) { if (!have[m.model]) model.appendChild(h('option', { value: m.model, text: m.model })); });
        var node = h('div', { class: 'stack' },
          r.off ? h('p', { class: 'sub', text: 'The Genie log is switched off in Site settings, so nothing new is kept.' }) : null,
          h('div', { class: 'grid five' },
            tile('Tasks', n(r.tasks)), tile('Done', r.tasks ? r.donePct + '%' : '–'), tile('Stuck', n(r.stuck)),
            tile('Failed steps', n(r.failures)), tile('Worst action', r.worstAction || '–')),
          table(cols, r.models, { empty: 'Nothing has been reported in this period.' }),
          r.codes && r.codes.length ? h('h3', { text: 'Most common failures' }) : null,
          r.codes && r.codes.length ? table(codeCols, r.codes, {}) : null,
          h('h3', { text: 'Events, newest first' }),
          table(ecols, r.events, { empty: 'No events match.' }),
          r.matching > r.events.length ? h('p', { class: 'sub', text: 'Showing the newest ' + r.events.length + ' of ' + n(r.matching) + '. Download the log for all of them.' }) : null,
          h('p', { class: 'sub', text: 'Kept ' + r.keepDays + ' days (Site settings, Genie).' + (r.dropped ? ' ' + n(r.dropped) + ' events were not kept because a day\'s file was full.' : '') }));
        render(box, JSON.stringify([r, f]), node);
      }, fail);
    }
    return { root: root, refresh: load, poll: false }; // not polled: it reads the days of the log from the disk
  }

  // ---- Genie health: how each model host answers Genie (server/genie_health.go), kept in memory by the hour

  function viewGenieHealth() {
    var root = h('div'), box = h('div');
    var hours = '24';
    var sel = h('select', { 'aria-label': 'Period', onchange: function () { hours = sel.value; load(); } },
      [['1', 'Last hour'], ['6', 'Last 6 hours'], ['24', 'Last 24 hours'], ['48', 'Last 48 hours']].map(function (o) { return h('option', { value: o[0], text: o[1], selected: o[0] === hours }); }));
    root.appendChild(pageHead('Genie health', 'For each model: how many of the calls to its host failed and why, and how long the host took to start answering. The alert limits are in Site settings, Genie. Kept in memory for 48 hours, so a restart starts it again; it holds no text and no user.'));
    root.appendChild(h('div', { class: 'toolbar' }, sel, h('span', { class: 'grow' }), refreshButton(function () { return load(); })));
    root.appendChild(box);

    function n(x) { return Number(x || 0).toLocaleString('en-US'); }
    function tile(label, value) { return h('div', { class: 'card' }, h('div', { class: 'sub', text: label }), h('b', { text: value })); }
    function ms(v) { return v === 0 ? '\u2013' : v < 0 ? 'over 32 s' : v < 1000 ? 'under ' + v + ' ms' : 'under ' + (v / 1000) + ' s'; }
    var REASON = { no_credit: 'no credit (402)', limit: 'limit (429)', no_host: 'no host (404)', host_error: 'host error (5xx)', refused: 'refused (4xx)', empty: 'no answer', network: 'network', timeout: 'timed out', cut: 'cut off' };
    var cols = [
      { key: 'model', label: 'Model', cell: function (r) { return h('div', null, h('div', { text: r.name || r.model }), h('div', { class: 'sub mono', text: r.model })); } },
      { key: 'calls', label: 'Calls', num: true, cell: function (r) { return n(r.calls); } },
      { key: 'err', label: 'Failed', num: true, cell: function (r) { return r.errors ? r.errPct.toFixed(r.errPct < 10 ? 1 : 0) + '% (' + n(r.errors) + ')' : '0%'; } },
      { key: 'retried', label: 'Retried', num: true, cell: function (r) { return r.retries ? n(r.retries) : '\u2013'; } },
      { key: 'why', label: 'Why', cell: function (r) {
        var keys = Object.keys(r.reasons || {}).sort(function (a, b) { return r.reasons[b] - r.reasons[a]; });
        return keys.length ? h('span', { class: 'wrap-text', text: keys.map(function (k) { return (REASON[k] || k) + ' \u00d7' + r.reasons[k]; }).join(' \u00b7 ') }) : h('span', { class: 'sub', text: '\u2013' });
      } },
      { key: 'p50', label: 'Starts answering, half', cell: function (r) { return ms(r.p50Ms); } },
      { key: 'p95', label: 'Starts answering, slowest 5%', cell: function (r) { return ms(r.p95Ms); } },
      { key: 'avg', label: 'Whole call, average', num: true, cell: function (r) { return r.avgMs ? (r.avgMs / 1000).toFixed(1) + ' s' : '\u2013'; } },
      { key: 'last', label: 'Last failure', cell: function (r) { return r.lastError ? when(new Date(r.lastError * 1000).toISOString()) : h('span', { class: 'sub', text: '\u2013' }); } },
      { key: 'alert', label: 'Status', cell: function (r) { return r.alerts && r.alerts.length ? h('span', null, r.alerts.map(function (a) { return pill(a, 'bad'); })) : pill('fine', 'ok'); } }
    ];
    var autoCols = [
      { key: 'role', label: 'Chosen', cell: function (r) { return h('div', null, h('div', { text: r.role }), h('div', { class: 'sub', text: r.s.name })); } },
      { key: 'calls', label: 'Calls', num: true, cell: function (r) { return n(r.s.calls); } },
      { key: 'err', label: 'Failed', num: true, cell: function (r) { return r.s.calls ? r.s.errPct.toFixed(r.s.errPct < 10 ? 1 : 0) + '%' : '\u2013'; } },
      { key: 'more', label: 'Asked for a stronger one', num: true, cell: function (r) { return r.extra; } }
    ];
    function load() {
      return api.get('admin/genie-health?hours=' + hours).then(function (r) {
        var calls = 0, errors = 0;
        r.rows.forEach(function (x) { calls += x.calls; errors += x.errors; });
        var node = h('div', { class: 'stack' },
          h('div', { class: 'grid four' }, tile('Models called', n(r.rows.length)), tile('Needing a look', n(r.alerting)), tile('Calls', n(calls)), tile('Failed', calls ? (errors * 100 / calls).toFixed(1) + '%' : '\u2013')),
          table(cols, r.rows, { empty: 'No model has been called since the server started.' }),
          r.auto ? h('h3', { text: 'Auto model choice' }) : null,
          r.auto ? table(autoCols, [{ role: 'Small', s: r.auto.small, extra: r.auto.stronger ? n(r.auto.stronger) + ' (' + r.auto.askedPct.toFixed(0) + '%)' : '\u2013' }, { role: 'Strong', s: r.auto.strong, extra: '\u2013' }], {}) : null,
          r.auto ? h('p', { class: 'sub', text: '\u201cAsked for a stronger one\u201d counts the clicks on Use a stronger one under an answer of the small model. If it is high, the rule sends too much to the small model.' }) : null,
          h('p', { class: 'sub', text: 'A model is flagged when at least ' + r.errPctLimit + '% of its calls failed, or when its slowest 5% took ' + (r.slowMsLimit / 1000) + ' s or more to start answering (with at least 5 calls). Counting began ' + localTime(new Date(r.since * 1000).toISOString()) + '.' }));
        render(box, JSON.stringify([r, hours]), node);
      }, fail);
    }
    return { root: root, refresh: load, poll: true };
  }

  // ---- audit log

  function viewAudit() {
    var root = h('div'), box = h('div');
    root.appendChild(pageHead('Audit log', 'Every change an admin made here: who, what, when and from where. The newest 500 are kept.'));
    root.appendChild(box);
    var cols = [
      { key: 'time', label: 'When', cell: function (e) { return h('div', null, when(e.time), h('div', { class: 'sub', text: localTime(e.time) })); } },
      { key: 'admin', label: 'Admin', cell: function (e) { return e.admin; } },
      { key: 'action', label: 'Action', cell: function (e) { return pill(e.action, 'plain'); } },
      { key: 'detail', label: 'Detail', cell: function (e) { return h('span', { class: 'wrap-text', text: e.detail || '' }); } },
      { key: 'from', label: 'From', cell: function (e) { return e.from ? mono(e.from) : h('span', { class: 'sub', text: '–' }); } }
    ];
    function refresh() {
      return api.get('admin/audit').then(function (r) { render(box, JSON.stringify(r.entries), table(cols, r.entries, { empty: 'No changes have been made yet.' })); });
    }
    return { root: root, refresh: refresh, poll: true };
  }

  // ---- Genie usage

  function viewGenieUsage() {
    var root = h('div'), box = h('div');
    var range = 'month';
    var csv = h('a', { class: 'btn', href: BASE + 'admin/genie-usage?format=csv&range=month', download: 'openrepl-genie-usage.csv', text: 'Download CSV' });
    var sel = h('select', { 'aria-label': 'Period', onchange: function () {
      range = sel.value;
      csv.setAttribute('href', BASE + 'admin/genie-usage?format=csv&range=' + range);
      box.__key = null;
      refresh();
    } }, h('option', { value: 'month', text: 'This month' }), h('option', { value: 'all', text: 'All time' }));
    root.appendChild(pageHead('Genie usage', 'The tokens each signed-in user has used, as the models report them, and what they cost.'));
    root.appendChild(h('div', { class: 'toolbar' }, sel, h('span', { class: 'grow' }), refreshButton(load), csv));
    root.appendChild(box);

    function n(x) { return Number(x || 0).toLocaleString('en-US'); }
    function usd(r) { return r.answers ? '$' + r.cost.toFixed(2) + (r.partial ? ' *' : '') : '\u2013'; }
    var cols = [
      { key: 'who', label: 'User', cell: function (r) {
        return h('div', null, h('div', { text: r.name || r.email || r.uid }), r.email && r.name ? h('div', { class: 'sub', text: r.email }) : null);
      } },
      { key: 'answers', label: 'Answers', num: true, cell: function (r) { return n(r.answers); } },
      { key: 'in', label: 'Sent', num: true, cell: function (r) { return n(r.in); } },
      { key: 'out', label: 'Written', num: true, cell: function (r) { return n(r.out); } },
      { key: 'cost', label: 'Cost', num: true, cell: function (r) { return usd(r); } },
      { key: 'models', label: 'By model (tokens)', cell: function (r) {
        return h('span', { class: 'sub', text: (r.models || []).map(function (m) { return m.name + ': ' + n(m.in + m.out) + (m.partial ? ' (no price)' : ''); }).join(' \u00b7 ') });
      } }
    ];

    function draw(r) {
      var rows = r.users.slice();
      var visitors = Object.assign({ name: 'Visitors (counted together)' }, r.visitors);
      if (visitors.answers) rows.push(visitors);
      var anyPartial = rows.some(function (x) { return x.partial; });
      var node = h('div', { class: 'stack' },
        table(cols, rows, { empty: 'No Genie answers have been counted ' + (range === 'month' ? 'this month' : 'yet') + '.' }),
        h('div', { class: 'card' }, h('div', { class: 'toolbar' }, h('b', { text: 'All users and visitors' }), h('span', { class: 'grow' }),
          h('span', { text: plural(r.total.answers, 'answer', 'answers') + ' \u00b7 ' + n(r.total.in) + ' sent \u00b7 ' + n(r.total.out) + ' written \u00b7 ' + usd(r.total) }))),
        anyPartial || !r.pricesSet ? h('p', { class: 'sub', text: (r.pricesSet ? '* A model with no price is not in this cost.' : 'No prices are set yet, so only the cost OpenRouter reports is counted.') + ' Set prices in Site settings, Genie prices and cost.' }) : null,
        r.truncated ? h('p', { class: 'sub', text: 'Only the first 500 users are listed.' }) : null);
      render(box, JSON.stringify([r, range]), node);
    }

    // how the agent is doing: counts of its tasks, and the baseline recorded before a change
    var measuresBox = h('div');
    root.appendChild(measuresBox);
    function per(a, b) { return b > 0 ? a / b : null; }
    function f1(x) { return x === null ? '\u2013' : (Math.round(x * 10) / 10).toLocaleString('en-US'); }
    function pct(a, b) { return b > 0 ? Math.round(a / b * 100) + '%' : '\u2013'; }
    function kb(x) { return x === null ? '\u2013' : (Math.round(x / 100) / 10).toLocaleString('en-US') + ' KB'; }
    // the rows for a set of counts
    function refusedText(m) {
      var parts = Object.keys(m || {}).map(function (id) { return id + ': ' + m[id]; });
      return parts.length ? parts.join(', ') : '\u2013';
    }
    function measureRows(s) {
      var ended = s.done + s.stopped + s.failed;
      return [
        f1(per(s.steps, s.tasks)),
        kb(per(s.reqBytes, s.steps)),
        pct(s.stepLimit, s.tasks),
        pct(s.unreadable + s.tooLong, ended),
        pct(s.stopped, ended),
        pct(s.checked, s.checked + s.notChecked),
        n(s.tasks),
        n(s.toolTries || 0),
        pct(s.toolFallbacks || 0, s.toolTries || 0),
        refusedText(s.toolRefused)
      ];
    }
    var MEASURES = ['Steps per task', 'Size of a step\'s request', 'Tasks that stopped at the step limit', 'Tasks stopped for an answer that was unreadable or too long', 'Tasks ended with Stop', 'Tasks that changed code and ended with a check', 'Tasks counted', 'Steps asked as a native tool call', 'Of those, refused by the host and sent again as text', 'Refused, by model'];
    function diff(a, b) {
      var o = {};
      Object.keys(a).forEach(function (k) {
        if (typeof a[k] === 'number') o[k] = a[k] - (b ? b[k] || 0 : 0);
        else if (a[k] && typeof a[k] === 'object') { // the refusals of each model
          o[k] = {};
          Object.keys(a[k]).forEach(function (m) { var d = a[k][m] - (b && b[k] ? b[k][m] || 0 : 0); if (d) o[k][m] = d; });
        }
      });
      return o;
    }
    function drawMeasures(m) {
      var hasBase = !!m.base;
      var since = measureRows(hasBase ? diff(m.now, m.base) : m.now), before = hasBase ? measureRows(m.base) : null;
      var rows = MEASURES.map(function (name, i) { return { name: name, before: before ? before[i] : '\u2013', since: since[i] }; });
      var node = h('div', { class: 'card' },
        h('div', { class: 'toolbar' }, h('b', { text: 'How the agent is doing' }), h('span', { class: 'grow' }),
          h('button', { class: 'btn', type: 'button', text: hasBase ? 'Record today as the new baseline' : 'Record today as the baseline', onclick: function () {
            api.post('admin/genie-measures', { action: 'baseline' }).then(function (r) { drawMeasures(r); });
          } })),
        table([
          { key: 'name', label: '', cell: function (r) { return r.name; } },
          { key: 'before', label: hasBase ? 'Baseline (' + new Date(m.baseAt * 1000).toLocaleDateString() + ')' : 'Baseline', num: true, cell: function (r) { return r.before; } },
          { key: 'since', label: hasBase ? 'Since the baseline' : 'Since counting began', num: true, cell: function (r) { return r.since; } }
        ], rows, { empty: '' }),
        h('p', { class: 'sub', text: 'Counts only: no user, no content. Record the baseline before a change to the agent, so that the change is a comparison and not a feeling.' }));
      render(measuresBox, JSON.stringify(m), node);
    }
    function loadMeasures(fresh) { return api.get('admin/genie-measures').then(drawMeasures); }

    function load(fresh) { return Promise.all([api.get(freshPath('admin/genie-usage?range=' + range, fresh)).then(draw), loadMeasures()]); }
    function refresh() { return load(false); }
    return { root: root, refresh: refresh, poll: false };
  }

  // ---- Live map
  //
  // The parts of the system, each with a status dot and a live number, joined by lines
  // with dots that move while traffic runs (GET admin/architecture, asked every few
  // seconds while the view is open). The boxes are drawn once and updated in place, so
  // the moving dots never restart; the animation is CSS and stops with Pause, with a
  // hidden tab and with the browser's reduced-motion setting.

  var MAP_W = 1440, MAP_H = 830;
  var MAP_PANELS = [
    { x: 64, y: 112, w: 272, h: 540, c: '#8B5CF6', name: 'Browser', nx: 82, ny: 124 },
    { x: 400, y: 112, w: 640, h: 540, c: '#38BDF8', name: 'OpenREPL host (this server)', nx: 418, ny: 124 },
    { x: 416, y: 168, w: 300, h: 468, c: '#38BDF8', name: 'gotty process', nx: 432, ny: 180, inner: true },
    { x: 740, y: 168, w: 284, h: 468, c: '#2DD4BF', name: 'Execution and workspaces', nx: 756, ny: 180, inner: true },
    { x: 1100, y: 112, w: 276, h: 540, c: '#F59E0B', name: 'Persistence', nx: 1118, ny: 124 },
    { x: 64, y: 680, w: 320, h: 130, c: '#D946EF', name: 'Firebase', nx: 82, ny: 692 },
    { x: 740, y: 680, w: 300, h: 130, c: '#D946EF', name: 'Language models', nx: 758, ny: 692 }
  ];
  var MAP_NODES = [
    { id: 'page', x: 80, y: 224, w: 240, h: 86, c: '#8B5CF6', t: 'page and IDE', s: 'editor, Files panel', g: 'Browser' },
    { id: 'terminals', x: 80, y: 334, w: 240, h: 86, c: '#8B5CF6', t: 'terminals', s: 'xterm.js tabs', g: 'Browser' },
    { id: 'genie', x: 80, y: 444, w: 240, h: 86, c: '#8B5CF6', t: 'chat-widget.js', s: 'Genie panel', g: 'Browser' },
    { id: 'http', x: 432, y: 224, w: 268, h: 86, c: '#38BDF8', t: 'HTTP mux', s: 'pages and REST APIs', g: 'gotty process' },
    { id: 'ws', x: 432, y: 334, w: 268, h: 86, c: '#38BDF8', t: 'WebSocket handlers', s: '/ws, /ws_python, …', g: 'gotty process' },
    { id: 'chat', x: 432, y: 444, w: 268, h: 86, c: '#38BDF8', t: 'chat proxy', s: '/chat/completions', g: 'gotty process' },
    { id: 'jobs', x: 530, y: 554, w: 170, h: 72, c: '#38BDF8', t: 'job scheduler', s: '', g: 'gotty process' },
    { id: 'procs', x: 756, y: 224, w: 252, h: 72, c: '#2DD4BF', t: 'terminal processes', s: '', g: 'Execution' },
    { id: 'files', x: 756, y: 316, w: 252, h: 72, c: '#2DD4BF', t: 'file browser', s: '', g: 'Execution' },
    { id: 'sandbox', x: 756, y: 408, w: 252, h: 72, c: '#2DD4BF', t: 'sandboxes', s: '', g: 'Execution' },
    { id: 'repls', x: 756, y: 500, w: 252, h: 72, c: '#2DD4BF', t: 'REPL programs', s: '', g: 'Execution' },
    { id: 'workers', x: 1100, y: 680, w: 244, h: 72, c: '#2DD4BF', t: 'workers', s: 'other machines', g: 'Fleet' },
    { id: 'data', x: 1116, y: 168, w: 244, h: 86, c: '#F59E0B', t: 'user and feedback data', s: 'sessions, blog, practice', g: 'Persistence' },
    { id: 'store', x: 1116, y: 278, w: 244, h: 86, c: '#F59E0B', t: 'UnQLite files', s: '/opt/gotty/*.db', g: 'Persistence' },
    { id: 'external', x: 1116, y: 388, w: 244, h: 86, c: '#8C8A98', t: 'MongoDB or Firestore', s: 'optional', g: 'Persistence', dashed: true },
    { id: 'objstore', x: 1116, y: 498, w: 244, h: 86, c: '#F59E0B', t: 'Object storage', s: 'S3, R2, B2: backups, workspaces', g: 'Persistence', dashed: true },
    { id: 'fb-db', x: 80, y: 728, w: 142, h: 66, c: '#D946EF', t: 'Realtime DB', s: '', g: 'Firebase', small: true },
    { id: 'fb-auth', x: 232, y: 728, w: 136, h: 66, c: '#D946EF', t: 'Auth', s: '', g: 'Firebase', small: true },
    { id: 'openai', x: 756, y: 728, w: 132, h: 66, c: '#D946EF', t: 'OpenAI', s: '', g: 'Language models', small: true },
    { id: 'openrouter', x: 896, y: 728, w: 132, h: 66, c: '#D946EF', t: 'OpenRouter', s: '', g: 'Language models', small: true }
  ];
  // a line is one or two straight pieces: [x, y, length, 'h' or 'v']
  var MAP_LINKS = [
    { id: 'page-http', segs: [[320, 267, 112, 'h']], label: ['HTTPS', 340, 244] },
    { id: 'term-ws', segs: [[320, 377, 112, 'h']], label: ['WebSocket', 340, 354] },
    { id: 'genie-chat', segs: [[320, 487, 112, 'h']] },
    { id: 'ws-exec', segs: [[700, 377, 40, 'h']] },
    { id: 'data-persist', segs: [[1024, 211, 92, 'h']] },
    { id: 'persist-store', segs: [[1024, 321, 92, 'h']] },
    { id: 'persist-objstore', segs: [[1024, 541, 92, 'h']] },
    { id: 'chat-llm', segs: [[470, 530, 231, 'v'], [470, 761, 286, 'h']], label: ['API key added here', 486, 650] },
    { id: 'jobs-exec', segs: [[700, 590, 40, 'h']] },
    { id: 'browser-firebase', segs: [[200, 652, 76, 'v']] }
  ];
  var STATUS_WORD = { ok: 'working', warn: 'slow or warning', fail: 'down', off: 'off or not set up' };

  function viewMap() {
    var root = h('div'), paused = false, selected = '', data = null;
    var stage = h('div', { class: 'am-stage' });
    var wrap = h('div', { class: 'am-wrap' }, stage);
    var drawer = h('div', { class: 'am-drawer card', hidden: true });
    var pauseBtn = h('button', { class: 'btn small', type: 'button', text: 'Pause', onclick: function () {
      paused = !paused;
      pauseBtn.textContent = paused ? 'Resume' : 'Pause';
      stage.classList.toggle('am-paused', paused);
    } });
    var live = h('span', { class: 'sub', text: '' });
    root.appendChild(pageHead('Live map', 'What is running, and how busy each part is right now. Dots on a line are traffic: they move faster when it is busier.'));
    root.appendChild(h('div', { class: 'toolbar' },
      h('span', { class: 'am-key' }, h('i', { class: 'am-dot ok' }), 'working'),
      h('span', { class: 'am-key' }, h('i', { class: 'am-dot warn' }), 'slow or warning'),
      h('span', { class: 'am-key' }, h('i', { class: 'am-dot fail' }), 'down'),
      h('span', { class: 'am-key' }, h('i', { class: 'am-dot off' }), 'off or not set up'),
      h('span', { class: 'grow' }), live, pauseBtn));
    root.appendChild(wrap);
    root.appendChild(drawer);

    MAP_PANELS.forEach(function (p) {
      stage.appendChild(h('div', { class: 'am-panel' + (p.inner ? ' inner' : ''), 'aria-hidden': 'true', vars: { left: p.x + 'px', top: p.y + 'px', width: p.w + 'px', height: p.h + 'px', '--c': p.c } }));
      stage.appendChild(h('div', { class: 'am-pname', 'aria-hidden': 'true', text: p.name, vars: { left: p.nx + 'px', top: p.ny + 'px', '--c': p.c } }));
    });
    var linkEls = {};
    MAP_LINKS.forEach(function (l) {
      linkEls[l.id] = l.segs.map(function (s) {
        var horizontal = s[3] === 'h';
        var el = h('div', { class: 'am-link ' + s[3], 'aria-hidden': 'true', vars: { left: s[0] + 'px', top: s[1] + 'px', width: (horizontal ? s[2] : 2) + 'px', height: (horizontal ? 2 : s[2]) + 'px' } });
        stage.appendChild(el);
        return el;
      });
      if (l.label) stage.appendChild(h('div', { class: 'am-linklabel', 'aria-hidden': 'true', text: l.label[0], vars: { left: l.label[1] + 'px', top: l.label[2] + 'px' } }));
    });
    var nodeEls = {};
    MAP_NODES.forEach(function (n) {
      var text = h('span', { class: 'am-text', text: '–' });
      var word = h('span', { class: 'visually-hidden', text: '' });
      var title = h('b', { text: n.t }), sub = n.s ? h('span', { class: 'am-sub', text: n.s }) : null;
      var el = h('button', { type: 'button', class: 'am-node' + (n.dashed ? ' dashed' : '') + (n.small ? ' small' : ''), 'data-id': n.id, 'data-group': n.g,
        hidden: n.id === 'workers', 'aria-pressed': 'false',
        vars: { left: n.x + 'px', top: n.y + 'px', width: n.w + 'px', height: n.h + 'px', '--c': n.c },
        onclick: function () { select(n.id); } },
        title, sub,
        h('span', { class: 'am-val' }, h('i', { class: 'am-dot off' }), word, text));
      nodeEls[n.id] = { el: el, text: text, word: word, title: title, sub: sub, dot: el.querySelector('.am-dot'), def: n };
      stage.appendChild(el);
    });

    function fit() {
      var narrow = root.clientWidth > 0 && root.clientWidth < 900;
      root.classList.toggle('am-narrow', narrow);
      var scale = narrow ? 1 : Math.min(1, (wrap.clientWidth || MAP_W) / MAP_W);
      stage.style.transform = narrow ? '' : 'scale(' + scale + ')';
      wrap.style.height = narrow ? '' : Math.round(MAP_H * scale) + 'px';
    }
    window.addEventListener('resize', fit);

    function setLink(id, lvl, status) {
      (linkEls[id] || []).forEach(function (el, i) {
        if (el.__lvl === lvl + status) return;
        el.__lvl = lvl + status;
        el.className = 'am-link ' + (el.classList.contains('h') ? 'h' : 'v') + ' lv-' + lvl + ' st-' + (status || 'ok');
        el.textContent = '';
        var dots = { none: 0, low: 1, mid: 2, high: 3, state: 0 }[lvl] || 0;
        for (var k = 0; k < dots; k++) el.appendChild(h('span', { class: 'am-fx', vars: { '--delay': (k * 0.55 + i * 0.3) + 's' } }));
      });
    }

    function draw(r) {
      data = r;
      var seen = {};
      r.nodes.forEach(function (n) {
        var ne = nodeEls[n.id];
        if (!ne) return;
        seen[n.id] = true;
        ne.el.hidden = false;
        // a box that is something else for now (the database files, when Firestore is the database) says so
        ne.title.textContent = n.title || ne.def.t;
        if (ne.sub) ne.sub.textContent = n.sub || ne.def.s;
        if (ne.text.textContent !== n.text) ne.text.textContent = n.text;
        ne.dot.className = 'am-dot ' + n.status;
        ne.word.textContent = STATUS_WORD[n.status] + ': ';
        ne.el.classList.toggle('is-off', n.status === 'off');
        ne.el.classList.toggle('is-warn', n.status === 'warn');
        ne.el.classList.toggle('is-fail', n.status === 'fail');
      });
      Object.keys(nodeEls).forEach(function (id) { if (!seen[id]) nodeEls[id].el.hidden = true; });
      r.links.forEach(function (l) { setLink(l.id, l.level, l.status); });
      live.textContent = 'Live · ' + new Date(r.at * 1000).toLocaleTimeString();
      if (selected) showDrawer();
    }

    function select(id) {
      selected = selected === id ? '' : id;
      Object.keys(nodeEls).forEach(function (k) { nodeEls[k].el.setAttribute('aria-pressed', String(k === selected)); });
      showDrawer();
    }

    function showDrawer() {
      if (!selected || !data) { drawer.hidden = true; return; }
      var n = data.nodes.filter(function (x) { return x.id === selected; })[0], def = nodeEls[selected] && nodeEls[selected].def;
      if (!n || !def) { drawer.hidden = true; return; }
      drawer.hidden = false;
      drawer.textContent = '';
      drawer.appendChild(h('div', { class: 'toolbar' }, h('i', { class: 'am-dot ' + n.status }), h('b', { text: n.title || def.t }),
        h('span', { class: 'sub', text: STATUS_WORD[n.status] + ' · ' + n.text }), h('span', { class: 'grow' }),
        n.view && VIEWS[n.view] ? h('a', { class: 'btn small', href: '#' + n.view, text: 'Open ' + VIEWS[n.view].label }) : null,
        h('button', { class: 'btn small', type: 'button', text: 'Close', onclick: function () { select(selected); } })));
      if (n.series && n.series.length) {
        var max = Math.max.apply(null, n.series.concat([1]));
        var bars = h('div', { class: 'am-bars', 'aria-label': 'The last 30 minutes, a bar for each minute', role: 'img' });
        n.series.forEach(function (v) { bars.appendChild(h('span', { class: 'am-bar', title: String(v), vars: { height: Math.max(2, Math.round(v * 100 / max)) + '%' } })); });
        drawer.appendChild(bars);
        drawer.appendChild(h('div', { class: 'sub', text: 'The last 30 minutes, a bar for each minute.' }));
      }
      (n.rows || []).forEach(function (row) {
        drawer.appendChild(h('div', { class: 'am-row' }, row.status ? h('i', { class: 'am-dot ' + row.status }) : null, h('span', { text: row.label }), h('span', { class: 'grow' }), h('span', { class: 'mono', text: row.value })));
      });
      if (['chat', 'openai', 'openrouter'].indexOf(selected) >= 0 && data.problems && data.problems.length) {
        drawer.appendChild(h('div', { class: 'sub', text: 'Recent problems' }));
        data.problems.slice(0, 5).forEach(function (p) {
          drawer.appendChild(h('div', { class: 'am-row' }, h('span', { class: 'mono', text: new Date(p.at * 1000).toLocaleTimeString() }), h('span', { text: p.text })));
        });
      }
    }

    function refresh() {
      fit();
      if (document.hidden) return Promise.resolve(); // nobody is looking: ask for nothing
      return api.get('admin/architecture').then(draw);
    }
    return { root: root, refresh: refresh, poll: true };
  }

  // ---- navigation --------------------------------------------------------------

  // ---- jobs: what runs by itself on this server

  function viewJobs() {
    var root = h('div'), box = h('div');
    root.appendChild(pageHead('Jobs', 'What runs by itself on this server, how often, when it last ran and what it did. A guest\'s folder is removed an hour after their last visit; the jobs below look after the rest. The counts start again when the server restarts.'));
    root.appendChild(box);

    function iso(sec) { return sec ? new Date(sec * 1000).toISOString() : ''; }
    // "every 3s", "every 15m", "every 1h": exact units, not "1m 0s"
    function every(sec) {
      if (!sec) return '–';
      if (sec % 3600 === 0) return 'every ' + sec / 3600 + 'h';
      if (sec % 60 === 0) return 'every ' + sec / 60 + 'm';
      return 'every ' + sec + 's';
    }
    // when something is due: worked out when the view is drawn (it is drawn again every few seconds), and "due" for a job that is about to run
    function dueIn(sec) {
      var left = sec - Math.floor(Date.now() / 1000);
      return h('span', { title: localTime(new Date(sec * 1000).toISOString()), text: left <= 0 ? 'due' : 'in ' + fmtDur(left) });
    }
    var TONE = { ok: 'ok', waiting: 'info', late: 'warn', failing: 'fail', off: '' };
    var WORD = { ok: 'Running', waiting: 'Waiting', late: 'Late', failing: 'Failing', off: 'Off' };

    var cols = [
      { key: 'name', label: 'Job', cell: function (j) {
        return h('div', null, h('div', { text: j.name }), h('div', { class: 'sub', text: j.state === 'off' ? j.what + ' Off here: ' + j.off + '.' : j.what }));
      } },
      { key: 'every', label: 'How often', cell: function (j) { return every(j.everySec); } },
      { key: 'state', label: 'State', cell: function (j) { return pill(WORD[j.state] || j.state, TONE[j.state]); } },
      { key: 'last', label: 'Last run', cell: function (j) { return j.state === 'off' ? '–' : when(iso(j.lastAt)); } },
      { key: 'result', label: 'What it did', cell: function (j) {
        if (j.lastError) return h('span', { class: 'sub', text: 'Failed: ' + j.lastError });
        return h('span', { class: 'sub', text: j.lastResult || '–' });
      } },
      { key: 'runs', label: 'Runs', num: true, cell: function (j) { return j.state === 'off' ? '–' : j.runs + (j.failures ? ' (' + j.failures + ' failed)' : ''); } },
      { key: 'next', label: 'Next run', cell: function (j) { return j.state === 'off' ? '–' : dueIn(j.nextAt); } }
    ];
    var folderCols = [
      { key: 'folder', label: 'Guest folder', cell: function (r) { return mono(r.folder); } },
      { key: 'due', label: 'Removed', cell: function (r) { return dueIn(r.dueAt); } }
    ];

    function draw(r) {
      var rm = r.removals;
      var removals = h('div', { class: 'card' },
        h('div', { class: 'toolbar' }, h('b', { text: 'Guest folder removals' }), h('span', { class: 'grow' }),
          h('span', { text: plural(rm.pending, 'folder waits', 'folders wait') + ' · next ' }), rm.nextAt ? dueIn(rm.nextAt) : h('span', { class: 'sub', text: 'nothing waits' }),
          h('span', { text: ' · ' + rm.fired + ' removed since the start' })),
        h('p', { class: 'sub', text: 'Each guest\'s folder is set to be removed an hour after their last visit; a visit moves it later. The waiting removals are saved every minute, so a crash does not lose them, and the sweep below removes any folder that was lost anyway.' }),
        rm.items.length ? table(folderCols, rm.items, { empty: '' }) : null,
        rm.pending > rm.items.length ? h('p', { class: 'sub', text: 'The first ' + rm.items.length + ' of ' + rm.pending + ' are listed, the soonest first.' }) : null);
      var node = h('div', { class: 'stack' }, table(cols, r.jobs, { empty: 'No jobs have registered yet.' }), removals);
      // drawn again on every refresh: the times left are worked out here and would go stale on the same data
      render(box, Date.now() + JSON.stringify(r.jobs.map(function (j) { return [j.id, j.state, j.runs, j.lastAt, j.lastResult, j.lastError]; }).concat([rm.pending, rm.fired, rm.nextAt, rm.items.map(function (i) { return i.folder; })])), node);
    }

    function refresh() { return api.get('admin/jobs').then(draw); }
    return { root: root, refresh: refresh, poll: true };
  }


  // ---- App Store (LLD 18): requests, failures, installed apps, servers, settings (canvas boards 7 and 8)

  function viewStore() {
    var root = h('div'), box = h('div');
    var tab = 'requests';
    var DAY = 86400;
    root.appendChild(pageHead('App Store', 'Libraries and tools that signed-in users switch on for their terminals. They are installed once on each server. Users install the safe list themselves; everything else is asked for here.'));
    root.appendChild(box);
    function post(body, okText) {
      return api.post('admin/store', body).then(function () { if (okText) toast(okText); return load(); }, fail);
    }
    function reasonDialog(title, confirm, required) {
      var input = h('input', { type: 'text', placeholder: 'A reason the people who asked will see', maxlength: '200' });
      return ask({ title: title, content: input, confirm: confirm, danger: true, validate: required ? function () { return input.value.trim() ? '' : 'A reason is required: the people who asked see it.'; } : null })
        .then(function (ok) { return ok ? input.value : null; });
    }
    function pickVersion(name) {
      return api.get('store/api/info?name=' + encodeURIComponent(name)).then(function (info) {
        var sel = h('select', { 'aria-label': 'Version' });
        (info.versions || []).forEach(function (v) { sel.appendChild(h('option', { value: v, text: v })); });
        if (!sel.options.length) { toast('conda-forge lists no other versions of ' + name, 'error'); return; }
        return ask({ title: 'Install another version of ' + name, body: 'The newest version did not install. Pick one that should; it is installed for everyone.', content: sel, confirm: 'Install it' })
          .then(function (ok) { if (ok) return post({ action: 'pickversion', name: name, version: sel.value }, 'Installing ' + name + ' ' + sel.value + '.'); });
      }, fail);
    }
    // the status of one app on one server: ready, installing (a server that is behind and answered lately), failed, missing
    function status(n, id, now, name) {
      var el = statusRaw(n, id, now, name);
      if (name) linkToLog(el, name, n.node);
      return el;
    }
    function statusRaw(n, id, now, name) {
      if ((n.have || []).indexOf(id) >= 0) return h('span', { class: 'ok', title: n.versions && n.versions[id] ? 'This server installed ' + n.versions[id] + ', the best version that exists for its CPU type' : '', text: n.versions && n.versions[id] ? 'ready (' + n.versions[id] + ', best for this CPU)' : 'ready' });
      if (n.failed && n.failed[id] && n.gaveUp && n.gaveUp[id]) return h('span', { class: 'bad', title: n.failed[id], text: 'not available here, or gave up: Reinstall here tries again' });
      if (n.failed && n.failed[id]) {
        var left = n.retry && n.retry[id] ? Math.max(1, Math.ceil((n.retry[id] - now) / 60)) : 0;
        return h('span', { class: 'bad', title: n.failed[id], text: left ? 'failed, next try in ' + left + ' min' : 'failed' });
      }
      // what the server says it is doing: busy with this app (a long install sends nothing else), or waiting for its turn
      if (name && n.doing === name) return h('span', { class: 'warn', text: 'installing… ' + (n.since ? Math.max(0, Math.floor((now - n.since) / 60)) + ' min' : '') });
      if (name && (n.queued || []).indexOf(name) >= 0) return h('span', { class: 'sub', text: 'waiting for another install' });
      var seen = n.seen ? Date.parse(n.seen) / 1000 : now;
      if (now - seen < 180) return h('span', { class: 'warn', text: 'installing…' });
      return h('span', { class: 'sub', text: 'missing' });
    }
    function tabs(d, counts) {
      var bar = h('div', { class: 'toolbar' });
      [['requests', 'Requests'], ['failures', 'Failures'], ['catalogue', 'Catalogue'], ['installed', 'Installed'], ['servers', 'Servers'], ['settings', 'Settings']].forEach(function (t) {
        var label = t[1] + (counts[t[0]] ? ' (' + counts[t[0]] + ')' : '');
        bar.appendChild(h('button', { class: 'btn' + (tab === t[0] ? ' primary' : ''), type: 'button', text: label, onclick: function () { tab = t[0]; box.__key = null; load(); } }));
      });
      bar.appendChild(h('span', { class: 'grow' }));
      bar.appendChild(refreshButton(load));
      return bar;
    }
    function draw(d) {
      lastStoreData = d;
      var out = h('div');
      if (!d.enabled && d.error) { out.appendChild(h('div', { class: 'card' }, h('p', { text: 'The App Store is off: ' + d.error }))); return out; }
      var waiting = d.requests.filter(function (r) { return r.status === 'waiting'; });
      var open = d.reports.filter(function (r) { return r.status === 'open'; });
      out.appendChild(h('div', { class: 'sub', text: 'Installer: ' + d.installer + ' · folder ' + d.dir + ' · Python ' + (d.python || '?') + ' · channel ' + d.channel + ' · ' + d.sizeMB.toFixed(0) + ' MB of ' + d.limitMB + ' MB' + (d.enabled ? '' : ' · SWITCHED OFF') }));
      out.appendChild(tabs(d, { requests: waiting.length, failures: open.length }));
      if (tab === 'requests') {
        out.appendChild(h('h3', { text: 'Requests: not on the safe list' }));
        out.appendChild(table([
          { key: 'name', label: 'App', cell: function (r) { return h('div', null, h('b', { text: r.name }), h('div', { class: 'sub' }, r.kind + ' · asked ', when(new Date(r.at * 1000).toISOString()))); } },
          { key: 'askers', label: 'Asked by', cell: function (r) { return r.askers.length + (r.askers.length === 1 ? ' user' : ' users'); } },
          { key: 'repl', label: 'For', cell: function (r) { return r.repl || 'any'; } },
          { key: 'look', label: 'Lookup', cell: function (r) {
            var out2 = h('div', { class: 'sub', 'data-lookup': r.name });
            return h('div', null, h('button', { class: 'btn', type: 'button', text: 'Look it up', onclick: function () {
              out2.textContent = 'Looking it up\u2026';
              api.post('admin/store/catalogue', { action: 'lookup', name: r.name, kind: r.kind }).then(function (x) {
                out2.textContent = x.solves ? 'solves: ' + x.version + ', about ' + x.diskMB + ' MB installed' + ((x.left || []).some(function (l) { return l.belowReserve; }) ? ' (a server would drop below the reserve)' : '') : 'does not solve: ' + (x.error || '').slice(0, 120);
              }, function (e) { out2.textContent = e.message; });
            } }), out2);
          } },
          { key: 'reason', label: 'Why', cell: function (r) { return h('span', { class: 'wrap-text', text: r.reason || '' }); } },
          { key: 'status', label: 'Status', cell: function () { return pill('Waiting', 'warn'); } },
          { key: 'act', label: 'Decide', cell: function (r) {
            return h('div', { class: 'row-actions' },
              h('button', { class: 'btn primary', type: 'button', text: 'Approve', title: 'Opens the Add form with everything filled in; you decide and press Add and install', onclick: function () {
                var cat = d.cat || { repls: [], checks: [], places: [] };
                var inCat = (cat.entries || []).filter(function (e) { return (e.package || e.name).toLowerCase() === r.name; })[0];
                if (inCat) { post({ action: 'approve', name: r.name }, r.name + ' is being installed.'); return; }
                tab = 'catalogue'; box.__key = null;
                addAppDialog(cat, { name: r.name, package: r.name, kind: r.kind === 'node' ? 'node' : r.kind === 'tool' ? 'tool' : 'lib', manager: r.kind === 'node' ? 'npm' : 'pixi', repls: [r.repl || 'python'], level: 'optional', install: 'approval', check: r.kind === 'node' ? 'node-require' : r.kind === 'tool' ? 'version-flag' : 'python-import', checkArg: r.name.replace(/-/g, '_') });
              } }),
              h('button', { class: 'btn danger', type: 'button', text: 'Reject', onclick: function () { reasonDialog('Reject ' + r.name + '?', 'Reject with this reason', true).then(function (why) { if (why !== null) post({ action: 'reject', name: r.name, answer: why }); }); } }));
          } }
        ], waiting, { empty: 'Nobody is waiting for an answer.' }));
        out.appendChild(h('p', { class: 'sub', text: 'Approve and install runs the install at once and tells the people who asked. Reject asks for a line of reason, which they see on the app\'s card. Every decision goes to the audit log.' }));
      }
      if (tab === 'failures') {
        out.appendChild(h('h3', { text: 'Failures: an install that stopped, and the report' }));
        out.appendChild(table([
          { key: 'name', label: 'App', cell: function (r) { return h('div', null, h('b', { text: r.name }), h('div', { class: 'sub', text: (r.version ? r.version + ' · ' : '') + 'conda-forge' })); } },
          { key: 'who', label: 'Reported by', cell: function (r) { return r.reporters.length + (r.reporters.length === 1 ? ' user' : ' users'); } },
          { key: 'step', label: 'Where it stopped', cell: function (r) { return h('span', { class: 'wrap-text', text: (r.step || '').split(':')[0] }); } },
          { key: 'note', label: 'What they wanted', cell: function (r) { return h('span', { class: 'wrap-text', text: r.note || '' }); } },
          { key: 'log', label: 'Log', cell: function (r) { return h('pre', { class: 'wrap-text mono', text: r.log || '' }); } },
          { key: 'node', label: 'Server', cell: function (r) { return r.node ? mono(r.node) : ''; } },
          { key: 'status', label: 'Status', cell: function () { return pill('Open', 'bad'); } },
          { key: 'act', label: 'Act', cell: function (r) {
            return h('div', { class: 'row-actions' },
              h('button', { class: 'btn primary', type: 'button', text: 'Try again', onclick: function () { post({ action: 'retry', name: r.name }, 'Trying ' + r.name + ' again.'); } }),
              h('button', { class: 'btn', type: 'button', text: 'Open full log', onclick: function () { openLog(r.name, r.node && r.node !== 'gateway' ? r.node : 'gateway'); } }),
              h('button', { class: 'btn', type: 'button', text: 'Pick another version', onclick: function () { pickVersion(r.name); } }),
              h('button', { class: 'btn', type: 'button', text: 'Mark installed', title: 'You fixed it by hand on the servers', onclick: function () { post({ action: 'installed', name: r.name }); } }),
              h('button', { class: 'btn danger', type: 'button', text: 'Reject', onclick: function () { reasonDialog('Reject the report about ' + r.name + '?', 'Reject').then(function (why) { if (why !== null) post({ action: 'rejectreport', name: r.name, answer: why }); }); } }));
          } }
        ], open, { empty: 'No failures are waiting for you.' }));
        out.appendChild(h('p', { class: 'sub', text: 'Try again, Pick another version, Mark installed and Reject all end the item and send the answer to everyone who reported it.' }));
      }
      if (tab === 'catalogue') catalogueTab(out, d);
      if (tab === 'installed') {
        out.appendChild(h('h3', { text: 'Installed apps, and where' }));
        var cols = [
          { key: 'name', label: 'App', cell: function (a) { return h('div', null, h('b', { text: a.name }), h('div', { class: 'sub', text: a.version + ' · ' + (a.waiting ? 'waiting for another install' : a.state) + (a.state === 'installed' && a.kind !== 'node' && a.kind !== 'plugin' ? (a.locked ? ' · locked' : ' · no lock (made before pixi)') : '') })); } },
          { key: 'size', label: 'Size', cell: function (a) { return a.size ? (a.size / 1048576).toFixed(0) + ' MB' : ''; } },
          { key: 'users', label: 'On for', cell: function (a) {
            var n = d.users[a.id] || 0, idle = d.now - Math.max(a.at || 0, a.lastUsed || 0);
            var everyone = (d.defaults || []).indexOf(a.name) >= 0;
            return h('div', null, n + (n === 1 ? ' user' : ' users'), n === 0 && a.state === 'installed' && idle > 60 * DAY ? h('div', { class: 'warn', text: 'unused for ' + Math.floor(idle / DAY) + ' days' }) : null,
              a.state === 'installed' ? h('div', null, everyone ? h('span', { class: 'ok', text: 'enabled for everyone ' }) : null, h('button', { class: 'btn', type: 'button', text: everyone ? 'Disable for everyone' : 'Enable for everyone',
                title: 'Every signed-in user, the ones who sign in later too, gets it on; each may turn it off for themselves.',
                onclick: function () { post({ action: 'default', name: a.name, on: !everyone }, a.name + (everyone ? ' is no longer on for everyone.' : ' is on for every signed-in user now.')); } })) : null);
          } }
        ];
        d.nodes.forEach(function (n) { cols.push({ key: 'n-' + n.node, label: n.node, cell: function (a) { return a.state === 'installed' ? status(n, a.id, d.now, a.name) : linkToLog(h('span', { class: 'sub', text: a.waiting ? 'waiting for another install' : a.state }), a.name, n.node); } }); });
        cols.push({ key: 'act', label: '', cell: function (a) {
          return h('div', null, h('button', { class: 'btn' + (a.state === 'installing' ? ' primary' : ''), type: 'button', text: a.state === 'installing' ? 'Log (live)' : 'Log', title: 'What the install of this app did, on each server', onclick: function () { openLog(a.name, 'gateway'); } }), ' ', a.state === 'installed' && a.kind !== 'node' && a.kind !== 'plugin' ? h('button', { class: 'btn', type: 'button', text: 'New lock', title: 'Solve it again for every CPU type and make it again on every server: the same version, with the packages that exist today',
            onclick: function () {
              ask({ title: 'Make a new lock of ' + a.name + '?', body: 'It is solved again for every CPU type and installed again on every server. The people who have it on keep their switch; it is missing for a short while.', confirm: 'New lock' })
                .then(function (ok) { if (ok) post({ action: 'relock', name: a.name }, 'Making a new lock of ' + a.name + '.'); });
            } }) : null, ' ', h('button', { class: 'btn danger', type: 'button', text: 'Remove', onclick: function () {
            var n = d.users[a.id] || 0;
            ask({ title: 'Remove ' + a.name + '?', body: n ? n + ' users have it on. It is switched off for them and deleted on every server.' : 'Nobody has it on. It is deleted on every server.', confirm: 'Remove', danger: true })
              .then(function (ok) { if (ok) post({ action: 'remove', name: a.name }, a.name + ' was removed.'); });
          } }));
        } });
        out.appendChild(table(cols, d.apps, { empty: 'Nothing is installed yet.' }));
        out.appendChild(h('p', { class: 'sub', text: 'An app that nobody has had on for 60 days is flagged. Nothing is removed by itself: you decide. A server shows "installing" while it is behind and has answered lately.' }));
        var name = h('input', { type: 'text', placeholder: 'package name, e.g. ffmpeg', 'aria-label': 'Package to install' });
        var kindSel = h('select', { 'aria-label': 'Kind' },
          h('option', { value: 'lib', text: 'Python library (conda-forge)' }), h('option', { value: 'tool', text: 'Tool (conda-forge)' }), h('option', { value: 'node', text: 'Node library (npm)' }),
          h('option', { value: 'lang', text: 'Language (conda-forge)' }), h('option', { value: 'plugin', text: 'Editor plugin (comes with the server: wordcount, clock)' }));
        var everyoneBox = h('input', { type: 'checkbox', id: 'store-install-everyone' });
        out.appendChild(h('div', { class: 'toolbar' }, name, kindSel,
          h('label', { class: 'check' }, everyoneBox, ' Also turn it on for all signed-in users'),
          h('button', { class: 'btn primary', type: 'button', text: 'Install for everyone', onclick: function () { var n = name.value.trim(); if (n) post({ action: 'install', name: n, kind: kindSel.value, everyone: everyoneBox.checked }, 'Installing ' + n + '.'); } })));
      }
      if (tab === 'servers') {
        out.appendChild(h('h3', { text: 'Disk of the store, per server' }));
        var failedAny = false;
        d.nodes.forEach(function (n) {
          var pct = Math.min(100, Math.round(100 * n.sizeMB / d.limitMB));
          var bar = h('div', { class: 'bar', style: 'height:7px;border-radius:4px;background:var(--line,#2b2d26);overflow:hidden;margin-top:4px' }, h('i', { style: 'display:block;height:100%;width:' + pct + '%;background:var(--accent,#b6e03a)' }));
          out.appendChild(h('div', { class: 'card' }, h('b', { text: n.node }), ' ' + n.sizeMB.toFixed(0) + ' MB of ' + d.limitMB, n.seen ? h('span', null, ' · last heard ', when(n.seen)) : '', bar));
          var installedApps = d.apps.filter(function (a) { return a.state === 'installed'; });
          out.appendChild(table([
            { key: 'app', label: 'App on ' + n.node, cell: function (a) { return h('div', null, h('b', { text: a.name }), h('div', { class: 'sub', text: a.version + ' \u00b7 ' + a.kind })); } },
            { key: 'size', label: 'Size here', cell: function (a) { return n.sizes && n.sizes[a.id] ? Math.round(n.sizes[a.id]) + ' MB' : ''; } },
            { key: 'status', label: 'Status', cell: function (a) { return status(n, a.id, d.now, a.name); } },
            { key: 'act', label: '', cell: function (a) {
              var here = (n.have || []).indexOf(a.id) >= 0 || (n.failed && n.failed[a.id]);
              return here ? h('button', { class: 'btn', type: 'button', text: 'Reinstall here', title: 'Delete this server\'s copy and make it again', onclick: function () {
                post({ action: 'reinstall', node: n.node, id: a.id }, 'Asked ' + n.node + ' to make ' + a.name + ' again' + (n.node === 'gateway' ? '.' : ' (within a few seconds).')); } }) : '';
            } }
          ], installedApps, { empty: 'Nothing is installed yet.' }));
          if (n.pending && n.pending.length) out.appendChild(h('p', { class: 'sub', text: 'Sent to ' + n.node + ' with its next heartbeat (a few seconds): ' + n.pending.join(', ') }));
          Object.keys(n.failed || {}).forEach(function (k) {
            failedAny = true;
            out.appendChild(h('div', { class: 'card' }, h('b', { text: n.node + ': ' + k }), h('p', { class: 'sub wrap-text', text: n.failed[k] })));
          });
        });
        if (!failedAny) out.appendChild(h('p', { class: 'sub', text: 'Every server applied the catalog without a failure.' }));
        out.appendChild(h('p', { class: 'sub', text: 'The gateway sends a worker the catalog over the tunnel when it changes; the worker installs what is missing itself, from the internet, and tells the gateway after each app. A failed app is tried again after 1, 2, 5, 15, then 60 minutes.' }));
      }
      if (tab === 'settings') {
        out.appendChild(h('h3', { text: 'Settings' }));
        var safe = h('textarea', { rows: '6', 'aria-label': 'Safe list' }); safe.value = d.safeList.join('\n');
        var limit = h('input', { type: 'number', min: '100', step: '100', value: String(d.limitSet || ''), placeholder: String(d.limitMB), 'aria-label': 'Size limit in MB' });
        var off = h('input', { type: 'checkbox' }); off.checked = !d.enabled;
        var cs = (d.cat && d.cat.settings) || {};
        var reserve = h('input', { type: 'number', min: '0', step: '256', value: '', placeholder: String(cs.reserveMB || 3072), 'aria-label': 'Reserve in MB' });
        var perDay = h('input', { type: 'number', min: '0', value: '', placeholder: String(cs.perDay || 10), 'aria-label': 'Requests a day for each user' });
        var scratchSoft = h('input', { type: 'number', min: '0', step: '64', value: '', placeholder: String(cs.scratchSoftMB || 256), 'aria-label': 'Scratch for each terminal (MB)' });
        var scratchServer = h('input', { type: 'number', min: '0', value: '', placeholder: String(cs.scratchServerMB || 2048), 'aria-label': 'Scratch on a server, all terminals (MB)' });
        out.appendChild(h('p', { class: 'sub', text: 'Safe list: users install these without asking, one name per line: flask (a Python library), tool:jq (a program), npm:express (a Node library). Languages and plugins are always an admin\'s.' }));
        out.appendChild(safe);
        out.appendChild(h('div', { class: 'toolbar' }, h('label', null, 'Most the store may use on a server (MB): ', limit), h('label', null, 'Channels: ', h('input', { type: 'text', value: d.channel, disabled: 'disabled', 'aria-label': 'Channels (not editable)' })),
          h('label', null, off, ' Switch the App Store off for everyone'), h('span', { class: 'grow' }),
          h('button', { class: 'btn primary', type: 'button', text: 'Save settings', onclick: function () {
            post({ action: 'settings', safeList: safe.value.split(/\s+/).filter(Boolean), limitMB: parseInt(limit.value, 10) || 0, off: off.checked,
              reserveMB: parseInt(reserve.value, 10) || 0, perDay: parseInt(perDay.value, 10) || 0, scratchSoftMB: parseInt(scratchSoft.value, 10) || 0, scratchServerMB: parseInt(scratchServer.value, 10) || 0 }, 'Saved.'); } })));
        out.appendChild(h('div', { class: 'toolbar' }, h('label', null, 'Reserve kept free for installs (MB): ', reserve), h('label', null, 'Requests a day for each user: ', perDay),
          h('label', null, 'Scratch for each terminal (MB, a soft cap): ', scratchSoft), h('label', null, 'Scratch on a server, all terminals (MB): ', scratchServer)));
        out.appendChild(h('h3', { text: 'apt packages (installed on every server, for all users)' }));
        var apt = h('textarea', { rows: '3', 'aria-label': 'apt packages, one per line' }); apt.value = (d.apt || []).join('\n');
        out.appendChild(apt);
        out.appendChild(h('div', { class: 'toolbar' }, h('button', { class: 'btn', type: 'button', text: 'Save apt list', onclick: function () {
          post({ action: 'apt', apt: apt.value.split(/\s+/).filter(Boolean) }, 'The apt list was saved. Each server installs it at its next check.'); } })));
      }
      return out;
    }
    // ---- the catalogue (LLD 18): what the store lists. Listing or importing an app never installs it.
    function catPost(body, okText) {
      return api.post('admin/store/catalogue', body).then(function (r) { if (okText) toast(okText); return load().then(function () { return r; }); }, fail);
    }
    function field(label, input, hint) { return h('div', { class: 'field' }, h('label', null, label, input), hint ? h('div', { class: 'sub', text: hint }) : null); }
    function opts(list, cur) { return list.map(function (x) { var o = h('option', { value: x[0], text: x[1] }); if (x[0] === cur) o.selected = true; return o; }); }
    var KINDS = [['lib', 'Library (Python, pixi)'], ['lib27', 'Library (Python 2.7, pixi)'], ['tool', 'Tool (pixi)'], ['clib', 'C/C++/Tcl/Perl/SQLite library (pixi)'], ['node', 'Node library (npm)'], ['weblib', 'Browser library (npm)'], ['gomod', 'Go module (mirror)'], ['jar', 'Maven jar (mirror)'], ['crate', 'Rust crate (mirror)']];
    var MGRS = [['pixi', 'pixi (conda-forge)'], ['npm', 'npm'], ['apt', 'apt'], ['go mirror', 'Go module mirror'], ['maven mirror', 'Maven mirror'], ['cargo mirror', 'Cargo mirror']];
    function addAppDialog(cat, e0, plugin) {
      var e = e0 || {};
      var repl = h('select', { 'aria-label': 'REPL' }, opts(cat.repls.map(function (r) { return [r, r]; }), (e.repls || ['python'])[0]));
      var kind = h('select', { 'aria-label': 'Kind' }, opts(KINDS, e.kind || 'lib'));
      var mgr = h('select', { 'aria-label': 'Manager' }, opts(MGRS, e.manager || 'pixi'));
      var pkg = h('input', { type: 'text', value: e.package || e.name || '', 'aria-label': 'Package name' });
      var ver = h('input', { type: 'text', value: e.version || '', placeholder: 'newest that solves', 'aria-label': 'Version' });
      var level = h('select', { 'aria-label': 'Level' }, opts([['core', 'core'], ['popular', 'popular'], ['optional', 'optional']], e.level || 'optional'));
      var who = h('select', { 'aria-label': 'Who may install' }, opts([['anyone', 'anyone'], ['approval', 'needs approval'], ['admin', 'admin only']], e.install || 'approval'));
      var check = h('select', { 'aria-label': 'Check after install' }, opts(cat.checks.map(function (c) { return [c, c]; }), e.check || 'python-import'));
      var arg = h('input', { type: 'text', value: e.checkArg || '', 'aria-label': 'Check argument' });
      var docs = h('input', { type: 'text', value: e.docs || '', 'aria-label': 'Documentation link' });
      var summary = h('input', { type: 'text', value: e.summary || '', 'aria-label': 'Summary', maxlength: '300' });
      var licence = h('input', { type: 'text', value: e.licence || '', 'aria-label': 'Licence' });
      var result = h('div', { class: 'card', 'data-lookup': '1' });
      var now = false;
      var look = h('button', { class: 'btn', type: 'button', text: 'Look it up', onclick: function () {
        result.textContent = 'Looking it up…';
        api.post('admin/store/catalogue', { action: 'lookup', name: pkg.value.trim().toLowerCase(), kind: kind.value === 'tool' || kind.value === 'clib' ? 'tool' : kind.value === 'node' ? 'node' : 'lib' }).then(function (r) {
          result.textContent = '';
          if (!r.solves) { result.appendChild(h('div', { class: 'bad', text: 'It does not solve: ' + (r.error || '') })); return; }
          result.appendChild(h('div', null, h('b', { text: pkg.value + ' ' + r.version }), ' ', pill('solves', 'ok'), ' ' + r.packages + ' packages, about ' + r.diskMB + ' MB installed'));
          (r.left || []).forEach(function (l) { result.appendChild(h('div', { class: l.belowReserve ? 'warn' : 'sub', text: l.node + ': ' + (l.leftMB / 1024).toFixed(1) + ' GB left' + (l.belowReserve ? ' (below the reserve)' : '') })); });
        }, function (err) { result.textContent = err.message; });
      } });
      var form = h('div', { class: 'form' },
        field('REPL', repl), field('Kind', kind), field('Manager', mgr, 'pixi, npm or apt as today, and the mirrors for Go modules, Maven jars and Rust crates. The check below is one of fixed templates: there is no field that takes a shell command.'),
        field('Package name', pkg), field('Version (optional pin)', ver), field('Level', level), field('Who may install', who),
        field('Check after install', check), field('Check argument', arg), field('Summary', summary), field('Licence', licence),
        field('Documentation link', docs, 'Official page, or /docs/... of this site for an app of ours. Shown on the app page.'),
        h('div', { class: 'toolbar' }, look, h('button', { class: 'btn primary', type: 'button', text: 'Add and install', onclick: function () { now = true; $('dialog').querySelector('.buttons .btn.primary').click(); } })), result);
      return ask({ title: (e0 ? 'Edit ' + (e0.name) : plugin ? 'Add a plugin' : 'Add an app to the catalogue'), content: form, confirm: 'Add to catalogue', wide: true }).then(function (ok) {
        if (!ok && !now) return;
        var entry = { id: e.id || (kind.value === 'node' ? 'npm:' : kind.value === 'weblib' ? 'web:' : '') + pkg.value.trim().toLowerCase(), name: e.name || pkg.value.trim().toLowerCase(), summary: summary.value || 'Added by an admin.',
          kind: kind.value, repls: [repl.value], level: level.value, manager: mgr.value, package: pkg.value.trim().toLowerCase(), version: ver.value.trim(), install: who.value, check: check.value, checkArg: arg.value, docs: docs.value.trim(), licence: licence.value, category: e.category || '' };
        return catPost({ action: 'add', entry: entry, install: now }, entry.name + ' is in the catalogue' + (now ? ' and being installed.' : '. Nothing was installed.'));
      });
    }
    function addPluginDialog(cat) {
      var pkg = h('input', { type: 'text', value: '', placeholder: 'npm package, e.g. eruda', 'aria-label': 'npm package' });
      var ver = h('input', { type: 'text', value: '', placeholder: 'exact version', 'aria-label': 'Exact version' });
      var level = h('select', { 'aria-label': 'Level' }, opts([['popular', 'popular'], ['optional', 'optional'], ['core', 'core']], 'popular'));
      var adapter = h('select', { 'aria-label': 'Adapter type' }, opts([['preview-page-tool', 'Preview-page tool (runs in the previewed page)'], ['preview-toolbar', 'Preview toolbar item'], ['editor-status', 'Editor status item'], ['panel-frame', 'Panel in a sandboxed frame'], ['editor-extension', 'Editor extension (runs in the page)']], 'preview-page-tool'));
      var place = h('select', { 'aria-label': 'Place' }, opts(cat.places.map(function (x) { return [x, x]; }), 'preview'));
      var entry = h('input', { type: 'text', value: '', placeholder: 'entry file, e.g. eruda.js', 'aria-label': 'Entry file' });
      var docs = h('input', { type: 'text', value: '', 'aria-label': 'Documentation link' });
      var trust = h('input', { type: 'checkbox', 'aria-label': 'I checked the publisher and the licence' });
      var ext = h('input', { type: 'checkbox', 'aria-label': 'I understand this plugin runs in the page' });
      var form = h('div', { class: 'form' }, field('npm package', pkg), field('Exact version', ver), field('Level', level),
        field('Adapter type', adapter, 'Fixed types: editor extension (runs in the page), preview-page tool, panel in a sandboxed frame. If none fits, the plugin is not added.'),
        field('Place', place), field('Entry file', entry), field('Documentation link', docs, 'Official page, taken from the package when it has one. For a plugin of ours: its page on this site.'),
        h('label', { class: 'check' }, trust, ' I checked the publisher and the licence, and I want this exact version.'),
        h('label', { class: 'check' }, ext, ' I understand this plugin runs in the page (needed for an editor extension).'));
      return ask({ title: 'Add a plugin from npm', content: form, confirm: 'Add plugin', wide: true, validate: function () {
        if (!pkg.value.trim() || !ver.value.trim() || !entry.value.trim()) return 'The package, the exact version and the entry file are needed.';
        if (!trust.checked) return 'Tick that you checked the publisher and the licence.';
        if (adapter.value === 'editor-extension' && !ext.checked) return 'An editor extension runs in the page: tick that you understand it.';
      } }).then(function (ok) {
        if (!ok) return;
        var id = 'plugin:' + pkg.value.trim().toLowerCase();
        return catPost({ action: 'add', entry: { id: id, name: pkg.value.trim(), summary: 'A plugin from npm (' + pkg.value.trim() + ').', kind: 'plugin', place: place.value, level: level.value, manager: 'npm', package: pkg.value.trim().toLowerCase(),
          version: ver.value.trim(), install: 'admin', adapter: adapter.value, files: [entry.value.trim()], check: 'file-exists', checkArg: entry.value.trim(), docs: docs.value.trim(), licence: 'see the package', publisher: 'npm' } }, 'The plugin is in the catalogue. Nothing was installed.');
      });
    }
    function bulkDialog(cat) {
      var repl = h('select', { 'aria-label': 'REPL' }, opts(cat.repls.map(function (r) { return [r, r]; }), 'python'));
      var kind = h('select', { 'aria-label': 'Kind' }, opts([['lib', 'Library (conda-forge)'], ['tool', 'Tool (conda-forge)'], ['node', 'Node library (npm)']], 'lib'));
      var level = h('select', { 'aria-label': 'Level' }, opts([['optional', 'optional'], ['popular', 'popular'], ['core', 'core']], 'optional'));
      var names = h('textarea', { rows: '3', 'aria-label': 'Names, one for each line or separated by spaces' });
      var table_ = h('div', { 'data-bulk': '1' });
      var did = false, nowFlag = false;
      var run = h('button', { class: 'btn primary', type: 'button', text: 'Dry run', onclick: function () {
        table_.textContent = 'Working…';
        api.post('admin/store/catalogue', { action: 'bulkdry', kind: kind.value, names: names.value.split(/\s+/).filter(Boolean) }).then(function (r) {
          did = true; table_.textContent = '';
          var rows = r.rows || [];
          table_.appendChild(table([
            { key: 'name', label: 'Name', cell: function (x) { return h('b', { text: x.name }); } },
            { key: 'solves', label: 'Solves', cell: function (x) { return pill(x.solves ? 'solves' : 'no', x.solves ? 'ok' : 'bad'); } },
            { key: 'size', label: 'Installed', cell: function (x) { return x.sizeMB ? x.sizeMB + ' MB' : '\u2013'; } },
            { key: 'result', label: 'Result', cell: function (x) { return h('span', { class: 'wrap-text', text: (x.version ? x.version + ': ' : '') + x.result }); } }
          ], rows, { empty: 'No names.' }));
          table_.appendChild(h('p', { class: 'sub', text: rows.filter(function (x) { return x.solves; }).length + ' ready. A name that does not solve never blocks you.' }));
        }, function (err) { table_.textContent = err.message; });
      } });
      var form = h('div', { class: 'form' }, field('REPL', repl), field('Kind', kind), field('Level', level), field('Names', names), h('div', { class: 'toolbar' }, run, h('span', { class: 'sub', text: 'Nothing is added or installed by a dry run.' })), table_);
      return ask({ title: 'Bulk add (dry run first)', content: form, confirm: 'Add what solved', wide: true, validate: function () { if (!did) return 'Run the dry run first.'; } }).then(function (ok) {
        if (!ok) return;
        return catPost({ action: 'bulkadd', kind: kind.value, repls: [repl.value], level: level.value, names: names.value.split(/\s+/).filter(Boolean), install: false }, 'Added to the catalogue. Nothing was installed.');
      });
    }
    function packsDialog(cat) {
      var box = h('div', { 'data-packs': '1' });
      (cat.packs || []).forEach(function (p) {
        var missing = p.entries.filter(function (id) { var e = cat.entries.filter(function (x) { return x.id === id; })[0]; if (!e) return true; var k = e.kind === 'lib' ? 'lib' : e.kind === 'node' ? 'node' : 'tool'; return !d0.apps.some(function (a) { return a.name === (e.package || e.name).toLowerCase() && a.kind === k && a.state === 'installed'; }); });
        box.appendChild(h('div', { class: 'card', 'data-pack': p.id }, h('b', { text: p.name }), ' ', pill(p.entries.length + ' apps'), ' ', pill(missing.length ? missing.length + ' not installed' : 'installed', missing.length ? 'warn' : 'ok'),
          h('div', { class: 'sub wrap-text', text: p.entries.join(', ') }),
          h('div', { class: 'toolbar' }, h('button', { class: 'btn primary', type: 'button', text: missing.length ? 'Install ' + missing.length + ' missing' : 'Install missing', onclick: function () { $('dialog').close(); catPost({ action: 'packinstall', id: p.id }, 'The missing apps of ' + p.name + ' are being installed.'); } }),
            h('button', { class: 'btn danger', type: 'button', text: 'Remove pack', onclick: function () { $('dialog').close(); catPost({ action: 'packremove', id: p.id }, 'Pack removed.'); } }))));
      });
      return ask({ title: 'Packs: a named set, in one action', content: box, confirm: 'Close', cancel: false, wide: true });
    }
    function importDialog() {
      var area = h('textarea', { rows: '8', 'aria-label': 'A catalogue file (JSON)' });
      var rep = h('div');
      var fileIn = h('input', { type: 'file', accept: 'application/json,.json', 'aria-label': 'Choose a catalogue file' });
      fileIn.addEventListener('change', function () { var f = fileIn.files[0]; if (!f) return; var rd = new FileReader(); rd.onload = function () { area.value = String(rd.result).slice(0, 2 << 20); }; rd.readAsText(f); });
      var dry = h('button', { class: 'btn', type: 'button', text: 'Check it (nothing is saved)', onclick: function () {
        var data; try { data = JSON.parse(area.value); } catch (e) { rep.textContent = 'This is not JSON.'; return; }
        api.post('admin/store/catalogue', { action: 'import', data: data, dry: true }).then(function (r) { rep.textContent = r.report.added + ' apps and ' + r.report.packs + ' packs are valid' + (r.report.rejected.length ? '. Refused: ' + r.report.rejected.join('; ') : '.'); }, function (e) { rep.textContent = e.message; });
      } });
      return ask({ title: 'Import a catalogue', body: 'Every entry is checked like one typed in the form. Importing never installs anything.', content: h('div', { class: 'form' }, fileIn, area, dry, rep), confirm: 'Import', wide: true, validate: function () { if (!area.value.trim()) return 'Choose a file or paste one.'; } }).then(function (ok) {
        if (!ok) return;
        var data; try { data = JSON.parse(area.value); } catch (e) { toast('This is not JSON.', 'error'); return; }
        return catPost({ action: 'import', data: data, dry: false }, 'Imported. Nothing was installed.');
      });
    }
    function verifyNow() {
      toast('Checking every installed app on the gateway…');
      return api.post('admin/store/catalogue', { action: 'verify' }).then(function (r) {
        var bad = (r.results || []).filter(function (x) { return x.state !== 'ok'; });
        toast(bad.length ? bad.length + ' failed: ' + bad.map(function (x) { return x.name; }).join(', ') + '. See Failures.' : 'All ' + (r.results || []).length + ' apps work.', bad.length ? 'error' : undefined);
        return load();
      }, fail);
    }
    var d0 = null, filterRepl = '', filterLevel = '', filterText = '';
    function catalogueTab(out, d) {
      d0 = d;
      var cat = d.cat;
      if (!cat) { out.appendChild(h('p', { class: 'sub', text: 'The catalogue could not be loaded.' })); return; }
      cat.packs = cat.packs || [];
      var bar = h('div', { class: 'toolbar' },
        h('button', { class: 'btn primary', type: 'button', text: 'Add app', onclick: function () { addAppDialog(cat); } }),
        h('button', { class: 'btn', type: 'button', text: 'Add plugin', onclick: function () { addPluginDialog(cat); } }),
        h('button', { class: 'btn', type: 'button', text: 'Bulk add', onclick: function () { bulkDialog(cat); } }),
        h('button', { class: 'btn', type: 'button', text: 'Packs', onclick: function () { packsDialog(cat); } }),
        h('a', { class: 'btn', href: '/admin/store/catalogue?export=1', text: 'Export', download: 'openrepl-catalogue.json' }),
        h('button', { class: 'btn', type: 'button', text: 'Import', onclick: function () { importDialog(); } }),
        h('button', { class: 'btn', type: 'button', text: 'Verify now', onclick: verifyNow }),
        h('span', { class: 'grow' }),
        h('input', { type: 'search', class: 'input', placeholder: 'Search the catalogue', 'aria-label': 'Search the catalogue', value: filterText, onchange: function (ev) { filterText = ev.target.value.trim().toLowerCase(); pageState('App|REPL|Kind|Level|Manager, size|Who may install|State on the servers|').page = 1; box.__key = null; load(); } }),
        h('label', null, 'REPL ', h('select', { 'aria-label': 'Filter by REPL', onchange: function (ev) { filterRepl = ev.target.value; box.__key = null; load(); } }, opts([['', 'all']].concat(cat.repls.map(function (r) { return [r, r]; })), filterRepl))),
        h('label', null, 'Level ', h('select', { 'aria-label': 'Filter by level', onchange: function (ev) { filterLevel = ev.target.value; box.__key = null; load(); } }, opts([['', 'all'], ['core', 'core'], ['popular', 'popular'], ['optional', 'optional']], filterLevel))));
      out.appendChild(bar);
      var list = cat.entries.filter(function (e) { return (!filterRepl || (e.repls || []).indexOf(filterRepl) >= 0 || e.kind === 'plugin') && (!filterLevel || e.level === filterLevel) && (!filterText || (e.name + ' ' + (e.summary || '') + ' ' + (e.package || '')).toLowerCase().indexOf(filterText) >= 0); });
      var NAMES = { lib: 'library', lib27: 'library (2.7)', tool: 'tool', clib: 'C library', node: 'library', weblib: 'browser lib', plugin: 'plugin', gomod: 'library', jar: 'library', crate: 'crate' };
      function stateOf(e) {
        var k = e.kind === 'lib' ? 'lib' : (e.kind === 'node' || e.kind === 'weblib' || (e.kind === 'plugin' && !e.internal)) ? 'node' : e.kind === 'tool' || e.kind === 'clib' ? 'tool' : '';
        var a = k && d.apps.filter(function (x) { return x.name === (e.package || e.name).toLowerCase() && x.kind === k; })[0];
        if (!a) return { app: null, node: h('span', { class: 'sub', text: k ? 'listed, not installed' : 'listed' }) };
        if (a.state !== 'installed') return { app: a, node: pill(a.waiting ? 'waiting for another install' : a.state, a.state === 'failed' ? 'bad' : 'warn') };
        var span = h('span');
        d.nodes.forEach(function (n) { var ok = (n.have || []).indexOf(a.id) >= 0; span.appendChild(linkToLog(pill(n.node + (ok ? '' : (n.failed && n.failed[a.id]) ? ' failed' : n.doing === a.name ? ' installing' : (n.queued || []).indexOf(a.name) >= 0 ? ' waiting' : ' missing'), ok ? 'ok' : (n.failed && n.failed[a.id]) ? 'bad' : 'warn'), a.name, n.node)); span.appendChild(document.createTextNode(' ')); });
        return { app: a, node: span };
      }
      out.appendChild(table([
        { key: 'name', label: 'App', cell: function (e) { return h('div', null, h('b', { text: e.name }), e.docs ? h('div', { class: 'sub' }, h('a', { href: e.docs, target: '_blank', rel: 'noopener noreferrer', text: 'docs' })) : null); } },
        { key: 'repl', label: 'REPL', cell: function (e) { return e.kind === 'plugin' ? e.place : (e.repls || []).join(', '); } },
        { key: 'kind', label: 'Kind', cell: function (e) { return NAMES[e.kind] || e.kind; } },
        { key: 'level', label: 'Level', cell: function (e) { return pill(e.level); } },
        { key: 'mgr', label: 'Manager, size', cell: function (e) {
          var m = e.minimal;
          if (!m) return e.manager;
          // the smallest option, its size per CPU type, and the others that were tried (before the trim, where there is one)
          var arch = { 'linux-64': 'amd64', 'linux-aarch64': 'arm64' }, sz = Object.keys(m.size || {}).sort().map(function (k) { return arch[k] + ' ' + m.size[k] + ' MB'; }).join(', ');
          var others = Object.keys(m.others || {}).map(function (k) { return k + ' ' + Object.keys(m.others[k]).sort().map(function (a) { return arch[a] + ' ' + m.others[k][a] + ' MB'; }).join(', '); });
          var before = ((e.options || []).filter(function (o) { return o.manager === m.manager; })[0] || {}).before;
          var bt = before ? 'before the trim: ' + Object.keys(before).sort().map(function (a) { return arch[a] + ' ' + before[a] + ' MB'; }).join(', ') : '';
          return h('div', null, h('b', { text: m.manager + (m.estimate ? ' (estimate)' : '') }), h('div', { class: 'sub', text: sz }), bt ? h('div', { class: 'sub', text: bt }) : null, others.length ? h('div', { class: 'sub', text: 'not chosen: ' + others.join('; ') }) : null);
        } },
        { key: 'who', label: 'Who may install', cell: function (e) { return { anyone: 'anyone', approval: 'needs approval', admin: 'admin only' }[e.install] || e.install; } },
        { key: 'state', label: 'State on the servers', cell: function (e) { return stateOf(e).node; } },
        { key: 'act', label: '', cell: function (e) {
          var st = stateOf(e), canInstall = (['lib', 'tool', 'clib', 'node', 'weblib'].indexOf(e.kind) >= 0 || (e.kind === 'plugin' && !e.internal)) && e.python !== '2.7' && !st.app;
          return h('div', { class: 'row-actions' }, h('button', { class: 'btn', type: 'button', text: 'Edit', onclick: function () { addAppDialog(cat, e, e.kind === 'plugin'); } }),
            e.kind === 'tool' ? h('button', { class: 'btn', type: 'button', text: 'Compare', title: 'The dry runs only (the apt plan and the pixi solve): seconds, nothing is installed. The sizes are estimates until you press Measure.', onclick: function () { api.post('admin/store/catalogue', { action: 'compare', id: e.id }).then(function (r) { toast(r.summary); return load(); }, fail); } }) : null,
            (['tool', 'lib', 'clib', 'node', 'weblib', 'gomod', 'jar', 'crate', 'lib27'].indexOf(e.kind) >= 0) ? h('button', { class: 'btn', type: 'button', text: 'Measure', title: 'Install it with every manager that can on this server, trim it, and keep the sizes: the smallest is chosen. Takes minutes; this server\'s CPU type only.', onclick: function () { catPost({ action: 'measure', id: e.id }, 'Measuring ' + e.name + ' in the background: reload this list in a few minutes.'); } }) : null,
            canInstall ? h('button', { class: 'btn primary', type: 'button', text: 'Install', onclick: function () { catPost({ action: 'install', id: e.id }, 'Installing ' + e.name + '.'); } }) : null,
            h('button', { class: 'btn danger', type: 'button', text: 'Retire', title: 'Take it off the list. You can restore it in Retired apps (an installed app stays installed)', onclick: function () { ask({ title: 'Retire ' + e.name + '?', body: 'It is no longer listed. You can restore it under Retired apps. An installed copy stays installed until you remove it under Installed.', confirm: 'Retire', danger: true }).then(function (ok) { if (ok) catPost({ action: 'remove', id: e.id }, e.name + ' was retired.'); }); } }));
        } }
      ], list, { empty: 'No entry matches.' }));
      // retired entries: cut from the default catalogue, but a record in the database still lists them (LLD 18, section 15)
      if ((cat.retired || []).length) {
        out.appendChild(h('h3', { text: 'Retired apps (' + cat.retired.length + ')' }));
        out.appendChild(h('p', { class: 'sub', text: 'Apps that were cut from the catalogue or that an admin retired. Restore brings back what the default catalogue still has; Delete record removes the database record of the rest. An installed copy stays installed. An installed copy stays installed (remove it under Installed).' }));
        var picks = {}, rbox = h('div', { class: 'card', 'data-retired': '1' }), rpager = h('div'), rst = { page: 1, size: 10 }, picked = h('span', { class: 'sub' });
        function countPicks() { var n = Object.keys(picks).filter(function (k) { return picks[k]; }).length; picked.textContent = n ? n + ' picked in all (the ticks stay when you change page)' : ''; }
        function drawRetired() {
          var pagesN = Math.max(1, Math.ceil(cat.retired.length / rst.size));
          if (rst.page > pagesN) rst.page = pagesN;
          rbox.innerHTML = '';
          cat.retired.slice((rst.page - 1) * rst.size, rst.page * rst.size).forEach(function (r) {
            var cb = h('input', { type: 'checkbox', 'aria-label': 'Pick ' + r.name, checked: !!picks[r.id], onchange: function (ev) { picks[r.id] = ev.target.checked; countPicks(); } });
            rbox.appendChild(h('label', { style: 'display:block;margin:2px 0' }, cb, ' ' + r.name + ' ', h('span', { class: 'sub', text: (NAMES[r.kind] || r.kind) + (r.removed ? ' \u00b7 retired by an admin' : '') + (r.installed ? ' \u00b7 installed' : '') })));
          });
          rpager.innerHTML = '';
          if (cat.retired.length > 10) rpager.appendChild(pager(cat.retired.length, rst, drawRetired, [10, 25, 50]));
        }
        drawRetired();
        out.appendChild(rbox);
        out.appendChild(rpager);
        out.appendChild(picked);
        out.appendChild(h('div', { class: 'toolbar' },
          h('button', { class: 'btn', type: 'button', text: 'Restore selected', title: 'Bring back entries that are in the default catalogue', onclick: function () {
            var ids = Object.keys(picks).filter(function (k) { return picks[k]; });
            if (!ids.length) { toast('Pick at least one app.'); return; }
            catPost({ action: 'restore-removed', names: ids }, 'Restored what the default catalogue still has.');
          } }),
          h('button', { class: 'btn', type: 'button', text: 'Select all', onclick: function () { cat.retired.forEach(function (r) { picks[r.id] = true; }); drawRetired(); countPicks(); } }),
          h('button', { class: 'btn danger', type: 'button', text: 'Delete record', title: 'Delete the database record of the picked apps that are not in the default catalogue', onclick: function () {
            var ids = Object.keys(picks).filter(function (k) { return picks[k]; });
            if (!ids.length) { toast('Pick at least one app.'); return; }
            ask({ title: 'Remove ' + ids.length + ' retired app' + (ids.length === 1 ? '' : 's') + ' from the list?', body: 'Their records are deleted from the database. Installed copies stay installed.', confirm: 'Remove', danger: true }).then(function (ok) { if (ok) catPost({ action: 'purge-retired', names: ids }, 'Deleted the records that could be deleted.'); });
          } })));
      }
      // the disk of every server
      out.appendChild(h('h3', { text: 'Disk of the store, on every server' }));
      out.appendChild(h('p', { class: 'sub', text: 'Physical size (shared files count once). An install is refused when it would leave less than the reserve.' }));
      (cat.disk || []).forEach(function (n) {
        var pct = Math.min(100, Math.round(100 * n.usedMB / n.limitMB));
        out.appendChild(h('div', { class: 'card', 'data-disk': n.node }, h('b', { text: n.node }), ' ' + (n.usedMB / 1024).toFixed(1) + ' GB used \u00b7 ' + (n.limitMB / 1024).toFixed(0) + ' GB \u00b7 ' + (n.reserveMB / 1024).toFixed(0) + ' GB reserve', n.belowReserve ? h('span', { class: 'bad', text: ' \u00b7 free below reserve soon' }) : null,
          h('div', { style: 'height:8px;border-radius:4px;background:var(--line,#2b2d26);overflow:hidden;margin-top:4px' }, h('i', { style: 'display:block;height:100%;width:' + pct + '%;background:var(--accent,#b6e03a)' }))));
      });
      out.appendChild(h('p', { class: 'sub', text: 'The limit is checked on the gateway and on each worker. Admin only: a user never sees node names or disk.' }));
    }
    function load() {
      return Promise.all([api.get('admin/store'), api.get('admin/store/catalogue').catch(function () { return null; })]).then(function (r) {
        var d = r[0]; d.cat = r[1];
        render(box, JSON.stringify([d, tab, filterRepl, filterLevel, filterText]), draw(d));
      });
    }
    return { root: root, refresh: load, poll: true };
  }

  // A page whose code is in files of its own, loaded the first time the page is opened (Site settings: admin-sections.js, admin-site.js). The view
  // is there at once, with "Loading…"; the real one is made when the file has come.
  var scripts = {};
  function loadScript(file) {
    if (!scripts[file]) {
      scripts[file] = new Promise(function (resolve, reject) {
        var el = document.createElement('script');
        el.src = BASE + 'js/' + file;
        el.onload = resolve;
        el.onerror = function () { delete scripts[file]; reject(new Error('The page script ' + file + ' could not be loaded.')); };
        document.head.appendChild(el);
      });
    }
    return scripts[file];
  }
  function lazyView(files, global) {
    return function () {
      var root = h('div', null, h('p', { class: 'loading', text: 'Loading…' }));
      var inner = null;
      return {
        root: root,
        poll: false,
        refresh: function () {
          return files.reduce(function (p, f) { return p.then(function () { return loadScript(f); }); }, Promise.resolve()).then(function () {
            if (!inner) {
              inner = window[global].make();
              root.textContent = '';
              root.appendChild(inner.root);
            }
            return inner.refresh();
          });
        },
        dirty: function () { return !!inner && inner.dirty(); },
        destroy: function () { if (inner && inner.destroy) inner.destroy(); }
      };
    };
  }
  // what the pages that are loaded later need from this file
  window.AdminKit = { api: api, ask: ask, colourOfTheDay: colourOfTheDay, fail: fail, h: h, loadScript: loadScript, pageHead: pageHead, toast: toast };

  var VIEWS = {
    overview: { label: 'Overview', icon: 'overview', make: viewOverview },
    map: { label: 'Live map', icon: 'map', make: viewMap },
    workers: { label: 'Workers', icon: 'workers', make: viewWorkers, gateway: true },
    sessions: { label: 'Sessions', icon: 'sessions', make: viewSessions, gateway: true },
    site: { label: 'Site settings', icon: 'site', make: lazyView(['admin-sections.js', 'admin-site.js'], 'AdminSite') },
    feedback: { label: 'Feedback', icon: 'feedback', make: viewFeedback, badge: true },
    snippets: { label: 'Shared code', icon: 'snippets', make: viewSnippets },
    users: { label: 'Users', icon: 'users', make: viewUsers },
    traffic: { label: 'Site usage', icon: 'traffic', make: viewTraffic },
    usage: { label: 'Genie usage', icon: 'usage', make: viewGenieUsage },
    health: { label: 'Health', icon: 'health', make: viewHealth },
    jobs: { label: 'Jobs', icon: 'jobs', make: viewJobs },
    params: { label: 'Parameters', icon: 'params', make: viewParams },
    logs: { label: 'Log', icon: 'logs', make: viewLogs },
    genielog: { label: 'Genie log', icon: 'logs', make: viewGenieLog },
    geniehealth: { label: 'Genie health', icon: 'health', make: viewGenieHealth },
    store: { label: 'App Store', icon: 'apps', make: viewStore },
    audit: { label: 'Audit log', icon: 'audit', make: viewAudit }
  };

  var GROUPS = [
    ['', ['overview', 'map']],
    ['Fleet', ['workers', 'sessions']],
    ['Site', ['site', 'feedback', 'snippets', 'store', 'users', 'traffic', 'usage']],
    ['System', ['health', 'jobs', 'params', 'logs', 'genielog', 'geniehealth', 'audit']]
  ];

  var unread = 0;

  function setUnread(n) {
    unread = n || 0;
    var b = document.querySelector('#nav [data-badge="feedback"]');
    if (!b) return;
    b.textContent = String(unread);
    b.hidden = !unread;
  }

  function buildNav(mode) {
    var nav = $('nav');
    nav.textContent = '';
    GROUPS.forEach(function (g) {
      var ids = g[1].filter(function (id) { return !VIEWS[id].gateway || mode === 'gateway'; });
      if (!ids.length) return;
      if (g[0]) nav.appendChild(h('div', { class: 'group', text: g[0] }));
      ids.forEach(function (id) {
        var v = VIEWS[id];
        nav.appendChild(h('a', { href: '#' + id, 'data-view': id }, icon(v.icon), h('span', { text: v.label }),
          v.badge ? h('span', { class: 'badge', 'data-badge': id, hidden: !unread, text: String(unread) }) : null));
      });
    });
  }

  var shownId = null;

  function show(id) {
    if (current && current.destroy) current.destroy();
    closeDrawer();
    shownId = id;
    var v = VIEWS[id];
    document.title = v.label + ' · OpenREPL Admin';
    Array.prototype.forEach.call(document.querySelectorAll('#nav a'), function (a) {
      if (a.getAttribute('data-view') === id) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
    });
    current = v.make();
    var main = $('view');
    main.textContent = '';
    main.appendChild(current.root);
    main.focus({ preventScroll: true });
    window.scrollTo(0, 0);
    lastUpdate = 0;
    setLive(paused ? 'paused' : 'live', paused ? 'Paused' : 'Loading…');
    refreshCurrent();
  }

  function route() {
    var id = (location.hash || '#overview').slice(1).split('/')[0];
    if (id === 'integrations') { history.replaceState(null, '', '#site/genie-integrations'); id = 'site'; } // it moved into Site settings, under Genie
    if (!VIEWS[id] || (VIEWS[id].gateway && (!params || params.mode !== 'gateway'))) id = 'overview';
    if (id === shownId) return;
    // leaving a form with unsaved changes asks first
    if (current && current.dirty && current.dirty()) {
      var back = '#' + shownId;
      history.replaceState(null, '', back);
      ask({ title: 'Discard your unsaved changes?', body: 'The settings you changed have not been saved.', confirm: 'Discard', danger: true }).then(function (yes) {
        if (yes) { history.replaceState(null, '', '#' + id); show(id); }
      });
      return;
    }
    show(id);
  }

  window.addEventListener('hashchange', route);
  window.addEventListener('beforeunload', function (e) {
    if (current && current.dirty && current.dirty()) { e.preventDefault(); e.returnValue = ''; }
  });

  // ---- start -------------------------------------------------------------------

  api.get('admin/gateway').then(function (p) {
    params = p;
    buildNav(p.mode);
    route();
    startPolling();
    api.get('admin/feedback').then(function (r) { setUnread(r.unread); }, function () {});
    setInterval(function () {
      if (paused || document.hidden || signedOutShown || (current && VIEWS.feedback.make === current.make)) return;
      api.get('admin/feedback').then(function (r) { setUnread(r.unread); }, function () {});
    }, 60000);
  }, function (err) {
    if (signedOutShown) return;
    var main = $('view');
    main.textContent = '';
    main.appendChild(h('div', { class: 'signed-out' }, h('h1', { text: 'The dashboard could not load' }),
      h('p', { text: err && err.message ? err.message : 'The server did not answer.' }),
      h('button', { class: 'btn primary', type: 'button', text: 'Try again', onclick: function () { location.reload(); } })));
    setLive('error', 'Not connected');
  });
})();
