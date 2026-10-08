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
    params: '<path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/>',
    logs: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6M16 13H8M16 17H8M10 9H8"/>',
    audit: '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>',
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
    var body = rows.map(function (row) {
      var tr = h('tr', { class: o.onRow ? 'click' : '' }, cols.map(function (c) {
        return h('td', { class: c.num ? 'num' : '' }, c.cell(row));
      }));
      if (o.onRow) {
        tr.setAttribute('tabindex', '0');
        tr.addEventListener('click', function (e) { if (!e.target.closest('button, a, input, select')) o.onRow(row); });
        tr.addEventListener('keydown', function (e) { if (e.key === 'Enter' && e.target === tr) o.onRow(row); });
      }
      return tr;
    });
    if (!rows.length) {
      body = [h('tr', null, h('td', { colspan: cols.length }, emptyState(o.empty || 'Nothing here yet.')))];
    }
    return h('div', { class: 'table-wrap' }, h('table', null, h('thead', null, head), h('tbody', null, body)));
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

    var cols = [
      { key: 'id', label: 'Node', sort: function (w) { return w.id; }, cell: function (w) {
        return h('div', null, h('b', { text: w.id === 'local' ? 'gateway (local)' : w.id }),
          h('div', { class: 'sub', text: w.id === 'local' ? 'runs sessions itself' : (w.version || '') }),
          ptraceBad(w.ptrace) ? pill('no ptrace', 'warn') : null); } },
      { key: 'state', label: 'State', sort: function (w) { return w.state; }, cell: function (w) { return pill(w.state.toLowerCase(), stateTone(w.state)); } },
      { key: 'weight', label: 'Weight', num: true, sort: function (w) { return w.weight; }, cell: function (w) { return String(w.weight); } },
      { key: 'mem', label: 'Memory', sort: function (w) { return w.usedMB; }, cell: function (w) { return usageBar(w.usedMB, w.maxMB); } },
      { key: 'terminals', label: 'Terminals', num: true, sort: function (w) { return w.terminals || 0; }, cell: function (w) { return String(w.terminals || 0); } },
      { key: 'sessions', label: 'Sessions', num: true, sort: function (w) { return w.sessions; }, cell: function (w) { return String(w.sessions); } },
      { key: 'picked', label: 'Picked', num: true, sort: function (w) { return w.picked; }, cell: function (w) {
        return h('div', null, h('b', { text: w.picked + ' · ' + w.pickedPercent + '%' }),
          h('div', { class: 'sub', text: w.weightPercent ? 'target ' + w.weightPercent + '%' : 'no new sessions' })); } },
      { key: 'actions', label: '', cell: function (w) {
        if (w.id === 'local') return h('span', { class: 'sub', text: 'set with --local-weight' });
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
        ['Weight', String(w.weight)],
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

  function viewSite() {
    var root = h('div'), body = h('div');
    var saved = null, form = null, languages = [], models = [], saving = false, keysState = null, adminsState = null;
    root.appendChild(pageHead('Site settings', 'Switches that change what every visitor sees. Changes apply to the next page a visitor loads.'));
    root.appendChild(body);

    // ---- the OpenRouter models an admin adds (the three built-in ones stay as they are)
    var CUSTOM_ID = /^[a-z0-9][a-z0-9._-]{0,63}\/[a-z0-9][a-z0-9._:+-]{0,95}$/;
    function hostsFrom(text) {
      var seen = {}, out = [];
      String(text || '').toLowerCase().split(/[\s,]+/).forEach(function (x) { if (x && !seen[x]) { seen[x] = 1; out.push(x); } });
      return out;
    }
    // one shape for a model from the server and from a row of the form, so that they compare
    function cleanCustom(c) {
      var id = String(c.id || '').trim().toLowerCase();
      return { id: id, name: String(c.name || '').trim() || id, enabled: !!c.enabled, free: !!c.free || /:free$/.test(id),
        answerTokens: int(c.answerTokens), thinkingRoom: int(c.thinkingRoom), jsonMode: !!c.jsonMode, hosts: (c.hosts || []).slice() };
    }
    function readCustom(r) {
      return cleanCustom({ id: r.id, name: r.name.value, enabled: r.on.checked, free: r.free.checked, answerTokens: r.answer.value,
        thinkingRoom: r.room.value, jsonMode: r.json.checked, hosts: hostsFrom(r.hosts.value) });
    }

    function snapshot() {
      return {
        colorOfTheDay: form.colour.checked,
        announcement: { text: form.ann.value.trim(), level: form.level.value },
        maintenance: { enabled: form.maint.checked, message: form.maintMsg.value.trim() },
        disabledLanguages: languages.filter(function (l) { return !form.langs[l.value].checked; }).map(function (l) { return l.value; }).sort(),
        genie: { disabled: !form.genie.checked, guestPerMinute: num(form.guest.value), userPerMinute: num(form.user.value),
          disabledModels: models.filter(function (m) { return !form.models[m.id].checked; }).map(function (m) { return m.id; }).sort(),
          customModels: (form.custom || []).map(readCustom),
          defaultModel: form.defModel.value,
          contextEditorChars: int(form.ctxEditor.value), contextTerminalChars: int(form.ctxTermChars.value),
          contextTerminalLines: int(form.ctxTermLines.value), historyMessages: int(form.history.value),
          openRouterTimeoutSec: int(form.orTimeout.value),
          answerCaps: capsOf(models.map(function (m) { return [m.id, int(form.caps[m.id].value)]; })),
          openRouterHosts: hostsOf(form.host1.value, form.host2.value),
          agentDisabled: !form.agentOn.checked, agentOnPractice: form.agentPractice.checked,
          agentTasksPerHour: int(form.agentTasks.value), agentMaxSteps: int(form.agentSteps.value) }
      };
    }
    function int(v) { v = parseInt(v, 10); return isNaN(v) ? 0 : v; }
    // the caps that are set, as an object in the order of the models
    function capsOf(pairs) {
      var o = {};
      pairs.forEach(function (p) { if (p[1] > 0) o[p[0]] = p[1]; });
      return o;
    }
    function hostsOf(first, second) {
      if (!first) return [];
      return second && second !== first ? [first, second] : [first];
    }
    function num(v) { v = parseFloat(v); return isNaN(v) ? 0 : v; }
    function clean(s) {
      return { colorOfTheDay: !!s.colorOfTheDay, announcement: { text: s.announcement.text, level: s.announcement.level || 'info' },
        maintenance: { enabled: !!s.maintenance.enabled, message: s.maintenance.message || '' },
        disabledLanguages: (s.disabledLanguages || []).slice().sort(),
        genie: { disabled: !!s.genie.disabled, guestPerMinute: s.genie.guestPerMinute || 0, userPerMinute: s.genie.userPerMinute || 0,
          disabledModels: (s.genie.disabledModels || []).slice().sort(), customModels: (s.genie.customModels || []).map(cleanCustom),
          defaultModel: s.genie.defaultModel || '',
          contextEditorChars: s.genie.contextEditorChars || 0, contextTerminalChars: s.genie.contextTerminalChars || 0,
          contextTerminalLines: s.genie.contextTerminalLines || 0, historyMessages: s.genie.historyMessages || 0,
          openRouterTimeoutSec: s.genie.openRouterTimeoutSec || 0,
          answerCaps: capsOf(models.map(function (m) { return [m.id, (s.genie.answerCaps || {})[m.id] || 0]; })),
          openRouterHosts: (s.genie.openRouterHosts || []).slice(),
          agentDisabled: !!s.genie.agentDisabled, agentOnPractice: !!s.genie.agentOnPractice,
          agentTasksPerHour: s.genie.agentTasksPerHour || 0, agentMaxSteps: s.genie.agentMaxSteps || 0 } };
    }
    function isDirty() { return !!form && !!form.ready && JSON.stringify(snapshot()) !== JSON.stringify(clean(saved)); }

    function build(settings, keysInfo, adminsInfo) {
      function toggle(id, checked, label) {
        var input = h('input', { type: 'checkbox', id: id, checked: checked, 'aria-label': label, onchange: update });
        return { input: input, node: h('label', { class: 'switch' }, input, h('i')) };
      }

      saved = settings;
      languages = settings.languages || [];
      models = settings.models || [];
      form = { langs: {}, models: {}, caps: {} };
      var defaults = settings.defaults || {};
      var colour = toggle('f-colour', settings.colorOfTheDay, 'Colour of the day');
      var maint = toggle('f-maint', settings.maintenance.enabled, 'Maintenance mode');
      var genie = toggle('f-genie', !settings.genie.disabled, 'Genie available');
      form.colour = colour.input; form.maint = maint.input; form.genie = genie.input;
      // agent mode: on for signed-in users unless switched off here, and off on the practice page unless allowed
      var agentOn = toggle('f-agent', !settings.genie.agentDisabled, 'Agent mode');
      var agentPractice = toggle('f-agent-practice', !!settings.genie.agentOnPractice, 'Agent mode on the practice page');
      form.agentOn = agentOn.input; form.agentPractice = agentPractice.input;

      var swatch = h('span', { class: 'swatch', vars: { '--sw': colourOfTheDay() } });
      var ann = h('textarea', { id: 'f-ann', maxlength: '280', placeholder: 'Shown as a banner on every page. Leave empty for none.', 'aria-label': 'Announcement text', oninput: update });
      ann.value = settings.announcement.text;
      var level = h('select', { class: 'field', id: 'f-level', 'aria-label': 'Announcement level', onchange: update },
        h('option', { value: 'info', text: 'Information' }), h('option', { value: 'warning', text: 'Warning' }));
      level.value = settings.announcement.level || 'info';
      var annCount = h('div', { class: 'counter' });
      var preview = h('div', { class: 'banner-preview' });
      var maintMsg = h('textarea', { id: 'f-maint-msg', maxlength: '280', placeholder: 'The site is down for maintenance. Please try again soon.', 'aria-label': 'Maintenance message', oninput: update });
      maintMsg.value = settings.maintenance.message;
      var maintCount = h('div', { class: 'counter' });
      var guest = h('input', { type: 'number', id: 'f-guest', min: '0', max: '60', step: '0.01', placeholder: '0.33 (built in)', 'aria-label': 'Guest requests per minute', oninput: update });
      var user = h('input', { type: 'number', id: 'f-user', min: '0', max: '60', step: '0.01', placeholder: '1 (built in)', 'aria-label': 'Signed-in requests per minute', oninput: update });
      guest.value = settings.genie.guestPerMinute || '';
      user.value = settings.genie.userPerMinute || '';
      Object.assign(form, { ann: ann, level: level, maintMsg: maintMsg, guest: guest, user: user });

      // one switch for each model, and the model a visitor starts with
      var modelToggles = models.map(function (m) {
        var on = toggle('f-model-' + m.id.replace(/[^a-z0-9]/gi, '-'), (settings.genie.disabledModels || []).indexOf(m.id) < 0, m.name + ' available');
        form.models[m.id] = on.input;
        return { model: m, node: on.node };
      });
      var defModel = h('select', { class: 'field', id: 'f-defmodel', 'aria-label': 'Default model', onchange: update },
        h('option', { value: '', text: 'Built in (Luna for visitors)' }));
      models.forEach(function (m) { defModel.appendChild(h('option', { value: m.id, text: m.name })); });
      defModel.value = settings.genie.defaultModel || '';
      form.defModel = defModel;
      var wantedDefault = settings.genie.defaultModel || '';

      // the models an admin added: one block each, with its own switch
      form.custom = [];
      var builtInIds = models.map(function (m) { return m.id; });
      var openRouterKeySet = models.some(function (m) { return m.provider === 'openrouter' && m.keySet; });
      var customList = h('div', { class: 'custom-models' });
      function addCustomRow(c) {
        var r = { id: c.id };
        var on = toggle('f-cm-' + form.custom.length + '-' + Date.now(), !!c.enabled, (c.name || c.id) + ' available');
        r.on = on.input;
        r.name = h('input', { type: 'text', class: 'field', maxlength: '40', 'aria-label': 'Name of ' + c.id, oninput: update });
        r.name.value = c.name && c.name !== c.id ? c.name : (c.name || '');
        r.free = h('input', { type: 'checkbox', checked: !!c.free || /:free$/.test(c.id), onchange: update, 'aria-label': c.id + ' is on a free tier' });
        r.json = h('input', { type: 'checkbox', checked: !!c.jsonMode, onchange: update, 'aria-label': c.id + ' takes JSON mode' });
        r.answer = h('input', { type: 'number', class: 'field', min: '500', max: '16000', step: '100', placeholder: '4000', 'aria-label': 'Answer size of ' + c.id, oninput: update });
        r.answer.value = c.answerTokens || '';
        r.room = h('input', { type: 'number', class: 'field', min: '0', max: '16000', step: '100', placeholder: '0', 'aria-label': 'Room for thinking of ' + c.id, oninput: update });
        r.room.value = c.thinkingRoom || '';
        r.hosts = h('input', { type: 'text', class: 'field', placeholder: 'OpenRouter chooses', 'aria-label': 'Providers allowed for ' + c.id, oninput: update });
        r.hosts.value = (c.hosts || []).join(', ');
        var remove = h('button', { class: 'btn small', type: 'button', text: 'Remove', 'aria-label': 'Remove ' + c.id, onclick: function () {
          form.custom = form.custom.filter(function (x) { return x !== r; });
          if (r.node.parentNode) r.node.parentNode.removeChild(r.node);
          update();
        } });
        var field = function (label, input, hint) { return h('label', { class: 'cm-field' }, h('span', { text: label }), input, hint ? h('small', { text: hint }) : null); };
        var check = function (input, label) { return h('label', { class: 'cm-check' }, input, h('span', { text: label })); };
        r.node = h('div', { class: 'custom-model' },
          h('div', { class: 'cm-head' }, on.node, h('div', { class: 'cm-title' }, r.name, h('span', { class: 'mono', text: c.id })), remove),
          h('div', { class: 'cm-grid' },
            field('Answer tokens', r.answer, 'Most tokens of an answer. Empty: 4000.'),
            field('Room for thinking', r.room, 'Extra tokens for a model that thinks first.'),
            field('Providers', r.hosts, 'Only these may answer (OpenRouter names, comma separated).')),
          h('div', { class: 'cm-checks' }, check(r.free, 'Free tier (visitors are told)'), check(r.json, 'Its host takes JSON mode (Agent mode)')));
        form.custom.push(r);
        customList.appendChild(r.node);
        return r;
      }
      (settings.genie.customModels || []).forEach(addCustomRow);
      var addId = h('input', { type: 'text', class: 'field', id: 'f-cm-new-id', placeholder: 'author/model-name:free', 'aria-label': 'OpenRouter model id', autocomplete: 'off' });
      var addName = h('input', { type: 'text', class: 'field', id: 'f-cm-new-name', maxlength: '40', placeholder: 'Name visitors see (optional)', 'aria-label': 'Name of the new model', autocomplete: 'off' });
      var addBtn = h('button', { class: 'btn', type: 'button', text: 'Add model', onclick: function () {
        var id = addId.value.trim().toLowerCase();
        if (!CUSTOM_ID.test(id)) { toast('An OpenRouter id looks like author/model-name, for example nvidia/nemotron-3-super-120b-a12b:free.', 'error'); return; }
        if (builtInIds.indexOf(id) >= 0 || form.custom.some(function (r) { return r.id === id; })) { toast(id + ' is in the list already.', 'error'); return; }
        if (form.custom.length >= 20) { toast('At most 20 models can be added. Remove one first.', 'error'); return; }
        var free = /:free$/.test(id);
        addCustomRow({ id: id, name: addName.value.trim(), enabled: false, free: free, jsonMode: false, thinkingRoom: free ? 3000 : 0, hosts: [] });
        addId.value = ''; addName.value = '';
        update();
      } });
      addId.addEventListener('keydown', function (e) { if (e.key === 'Enter') { e.preventDefault(); addBtn.click(); } });

      // the default model can be any model that is on, built in or added
      function isModelOn(id) {
        if (form.models[id]) return form.models[id].checked;
        return form.custom.some(function (r) { return r.id === id && r.on.checked; });
      }
      function syncDefaultModels() {
        var keep = defModel.value || wantedDefault;
        wantedDefault = '';
        Array.prototype.slice.call(defModel.options).forEach(function (o) { if (o.getAttribute('data-custom')) defModel.removeChild(o); });
        form.custom.forEach(function (r) {
          var o = h('option', { value: r.id, text: r.name.value.trim() || r.id });
          o.setAttribute('data-custom', '1');
          defModel.appendChild(o);
        });
        Array.prototype.forEach.call(defModel.options, function (o) { o.disabled = !!o.value && !isModelOn(o.value); });
        defModel.value = keep;
        if (defModel.value !== keep || (keep && !isModelOn(keep))) defModel.value = '';
      }

      // the numbers: empty means the built-in value, shown as the placeholder
      function numberField(id, key, label, builtIn, range) {
        var input = h('input', { type: 'number', class: 'field', id: id, min: String(range[0]), max: String(range[1]), step: '1',
          placeholder: builtIn + ' (built in)', 'aria-label': label, oninput: update });
        input.value = settings.genie[key] || '';
        input.style.maxWidth = '9rem';
        return input;
      }
      var limits = defaults.limits || {};
      form.ctxEditor = numberField('f-ctx-editor', 'contextEditorChars', 'Characters of editor code', defaults.contextEditorChars, limits.contextEditorChars || [1000, 50000]);
      form.ctxTermChars = numberField('f-ctx-term-chars', 'contextTerminalChars', 'Characters of terminal output', defaults.contextTerminalChars, limits.contextTerminalChars || [200, 20000]);
      form.ctxTermLines = numberField('f-ctx-term-lines', 'contextTerminalLines', 'Lines of terminal output', defaults.contextTerminalLines, limits.contextTerminalLines || [1, 200]);
      form.history = numberField('f-history', 'historyMessages', 'Messages kept', defaults.historyMessages, limits.historyMessages || [6, 50]);
      form.agentTasks = numberField('f-agent-tasks', 'agentTasksPerHour', 'Agent tasks per hour for each signed-in user', defaults.agentTasksPerHour || 20, limits.agentTasksPerHour || [1, 200]);
      form.agentSteps = numberField('f-agent-steps', 'agentMaxSteps', 'Steps in an agent task', defaults.agentMaxSteps || 8, limits.agentMaxSteps || [1, 8]);
      form.orTimeout = numberField('f-or-timeout', 'openRouterTimeoutSec', 'OpenRouter time limit in seconds', defaults.openRouterTimeoutSec, limits.openRouterTimeoutSec || [10, 90]);
      var capRows = models.map(function (m) {
        var range = limits.answerCap || [500, 16000];
        var input = h('input', { type: 'number', class: 'field', id: 'f-cap-' + m.id.replace(/[^a-z0-9]/gi, '-'), min: String(range[0]), max: String(range[1]), step: '100',
          placeholder: ((defaults.answerCaps || {})[m.id] || '') + ' (built in)', 'aria-label': 'Answer size of ' + m.name, oninput: update });
        input.value = ((settings.genie.answerCaps || {})[m.id]) || '';
        input.style.maxWidth = '9rem';
        form.caps[m.id] = input;
        return { model: m, input: input };
      });
      var hostList = (defaults.openRouterHosts || []);
      var chosenHosts = settings.genie.openRouterHosts || [];
      function hostSelect(id, label, none, value) {
        var sel = h('select', { class: 'field', id: id, 'aria-label': label, onchange: update }, h('option', { value: '', text: none }));
        hostList.forEach(function (hc) { sel.appendChild(h('option', { value: hc.id, text: hc.name })); });
        sel.value = value || '';
        return sel;
      }
      form.host1 = hostSelect('f-host1', 'First OpenRouter host', 'Built in order', chosenHosts[0]);
      form.host2 = hostSelect('f-host2', 'Second OpenRouter host', 'None', chosenHosts[1]);

      var langGrid = h('div', { class: 'lang-grid' });
      languages.forEach(function (l) {
        var cb = h('input', { type: 'checkbox', checked: settings.disabledLanguages.indexOf(l.value) < 0, onchange: update, 'aria-label': l.name + ' available' });
        form.langs[l.value] = cb;
        langGrid.appendChild(h('label', { 'data-lang': l.value }, cb, h('span', { text: l.name })));
      });

      var bar = h('div', { class: 'save-bar' });
      var status = h('span', { class: 'grow' });
      var revert = h('button', { class: 'btn', type: 'button', text: 'Revert', onclick: function () { body.textContent = ''; body.appendChild(build(saved, keysState, adminsState)); } });
      var save = h('button', { class: 'btn primary', type: 'button', text: 'Save settings', onclick: doSave });
      bar.appendChild(status); bar.appendChild(revert); bar.appendChild(save);

      function update() {
        annCount.textContent = ann.value.length + ' / 280';
        maintCount.textContent = maintMsg.value.length + ' / 280';
        preview.textContent = ann.value.trim() || 'The banner will look like this.';
        preview.className = 'banner-preview' + (level.value === 'warning' ? ' warning' : '');
        preview.style.opacity = ann.value.trim() ? '1' : '0.5';
        maintMsg.disabled = !maint.input.checked;
        guest.disabled = user.disabled = !genie.input.checked;
        // the agent's options only matter while it is on
        agentPractice.input.disabled = form.agentTasks.disabled = form.agentSteps.disabled = !agentOn.input.checked;
        // the second host only matters when a first one is chosen, and is another one
        form.host2.disabled = !form.host1.value;
        Array.prototype.forEach.call(form.host2.options, function (o) { o.disabled = !!o.value && o.value === form.host1.value; });
        if (form.host2.value === form.host1.value) form.host2.value = '';
        // a model that is off cannot be the default
        syncDefaultModels();
        Array.prototype.forEach.call(langGrid.children, function (lab) {
          lab.className = form.langs[lab.getAttribute('data-lang')].checked ? '' : 'off';
        });
        var dirty = isDirty();
        status.textContent = saving ? 'Saving…' : dirty ? 'You have unsaved changes.' : 'All changes are saved.';
        save.disabled = !dirty || saving;
        revert.disabled = !dirty || saving;
      }

      function doSave() {
        var s = snapshot();
        s.version = saved.version || 0; // the stored version this form was filled from (MongoDB refuses a stale one)
        if (s.genie.guestPerMinute < 0 || s.genie.guestPerMinute > 60 || s.genie.userPerMinute < 0 || s.genie.userPerMinute > 60) {
          toast('The Genie rates must be between 0 and 60 requests per minute.', 'error'); return;
        }
        var customOn = s.genie.customModels.some(function (c) { return c.enabled; });
        if (models.length && s.genie.disabledModels.length >= models.length && !customOn) {
          toast('At least one model has to stay on.', 'error'); return;
        }
        var go = Promise.resolve(true);
        var removed = (saved.genie.customModels || []).filter(function (c) {
          return !s.genie.customModels.some(function (n) { return n.id === c.id; });
        });
        if (removed.length) {
          go = ask({ title: 'Remove ' + removed.map(function (c) { return c.name || c.id; }).join(' and ') + '?',
            body: 'It is taken out of the list. Visitors who chose it are asked to pick another model. You can add it again by its id.', confirm: 'Remove', danger: true });
        }
        var newlyOff = models.filter(function (m) {
          return s.genie.disabledModels.indexOf(m.id) >= 0 && (saved.genie.disabledModels || []).indexOf(m.id) < 0;
        });
        if (newlyOff.length) {
          go = go.then(function (ok) { return ok ? ask({ title: 'Switch off ' + newlyOff.map(function (m) { return m.name; }).join(' and ') + '?',
            body: 'Visitors who use it are asked to pick another model. A visitor with a page already open sees that on their next request.', confirm: 'Switch off', danger: true }) : false; });
        }
        if (s.maintenance.enabled && !saved.maintenance.enabled) {
          go = go.then(function (ok) { return ok ? ask({ title: 'Turn on maintenance mode?', body: 'Visitors will not be able to start terminals until you turn it off. Admins are not affected.', confirm: 'Turn on', danger: true }) : false; });
        }
        go.then(function (yes) {
          if (!yes) return;
          saving = true; update();
          return api.post('admin/settings', s).then(function (r) {
            saving = false;
            toast('Settings saved. Visitors see the change on their next page load.');
            body.textContent = ''; body.appendChild(build(r, keysState, adminsState));
          }, function (e) { saving = false; update(); fail(e); });
        });
      }

      var card = function (title, sub, children) {
        return h('div', { class: 'card' }, h('h2', { text: title }), sub ? h('p', { class: 'sub', text: sub }) : null, children);
      };
      var row = function (title, what, control, full) {
        return h('div', { class: 'form-row' }, h('div', { class: 'what' }, h('b', { text: title }), what ? h('span', { text: what }) : null), control ? h('div', null, control) : null, full ? h('div', { class: 'full' }, full) : null);
      };

      var where = settings.store === 'mongodb'
        ? 'Stored in MongoDB (version ' + (settings.version || 0) + '). A change made on another instance appears here within about 10 seconds.'
        : 'Stored in the file settings.json on this server. Set OPENREPL_MONGODB_URI to keep the settings in MongoDB instead.';
      // API keys: saved at once, not with the form's Save button, and never shown
      var keysBox = h('div');
      function drawKeys(info) {
        keysState = info;
        keysBox.textContent = '';
        var rows = (info.keys || []).map(function (k) {
          var what;
          if (k.problem) what = k.problem + ' The server\'s own key is in use' + (k.set ? '.' : ', and there is none.');
          else if (k.source === 'dashboard') what = 'In use: saved here.';
          else if (k.source === 'env') what = 'In use: the server\'s environment key.';
          else if (k.source === 'file') what = 'In use: the server\'s settings file.';
          else what = 'Not set.';
          var buttons = h('span', { class: 'actions' },
            h('button', { class: 'btn', type: 'button', text: k.source === 'dashboard' ? 'Replace…' : 'Set…', disabled: !info.canStore, onclick: function () { setKey(k); } }),
            k.stored ? h('button', { class: 'btn', type: 'button', text: 'Remove', disabled: !info.canStore, onclick: function () { removeKey(k); } }) : null);
          return row(k.name + ' key', what, buttons);
        });
        keysBox.appendChild(card('API keys',
          'A key saved here is kept encrypted on the server and is never shown again. It is used instead of the key in the server\'s environment, and applies at once.' +
            (info.canStore ? '' : ' Set OPENREPL_SECRET on the server (and restart it) to keep keys here.'), rows));
      }
      function setKey(k) {
        var input = h('input', { type: 'password', class: 'field', autocomplete: 'off', spellcheck: 'false', placeholder: 'Paste the new key', 'aria-label': k.name + ' key' });
        input.style.width = '100%';
        ask({ title: (k.stored ? 'Replace the ' : 'Set the ') + k.name + ' key?',
          body: 'The server checks it with ' + k.name + ' first, then keeps it encrypted. It is not shown again. A visitor who has a page open may need to reload it once.',
          content: input, confirm: 'Save key',
          validate: function () { return input.value.trim() ? '' : 'Paste a key first.'; } }).then(function (yes) {
          var key = input.value.trim();
          input.value = '';
          if (!yes) return;
          return api.post('admin/keys', { provider: k.provider, key: key }).then(function (r) {
            toast(k.name + ' key saved.' + (r.warning ? ' ' + r.warning : ''));
            drawKeys(r);
          }, fail);
        });
      }
      function removeKey(k) {
        ask({ title: 'Remove the ' + k.name + ' key saved here?',
          body: 'The key in the server\'s environment, if there is one, is used again.', confirm: 'Remove', danger: true }).then(function (yes) {
          if (!yes) return;
          return api.post('admin/keys', { provider: k.provider, key: '' }).then(function (r) {
            toast(k.name + ' key removed.');
            drawKeys(r);
          }, fail);
        });
      }
      drawKeys(keysInfo || { canStore: false, keys: [] });

      // Admin accounts: owners come from the server's environment; an owner can
      // add and remove the others. Saved at once, not with the form's button.
      var adminsBox = h('div');
      function drawAdmins(info) {
        adminsState = info;
        adminsBox.textContent = '';
        var rows = [];
        (info.owners || []).forEach(function (e) {
          rows.push(row(e, 'Owner. Set in the server\'s environment (OPENREPL_ADMIN_EMAILS); cannot be removed here.', h('span', { class: 'pill', text: 'Owner' })));
        });
        (info.admins || []).forEach(function (e) {
          rows.push(row(e, 'Added in the dashboard. Can use everything here except this list.',
            info.canManage ? h('button', { class: 'btn', type: 'button', text: 'Remove', onclick: function () { removeAdmin(e); } }) : null));
        });
        var add = null;
        if (info.canManage) {
          var email = h('input', { type: 'email', class: 'field', id: 'f-admin-email', autocomplete: 'off', spellcheck: 'false', placeholder: 'name@example.com', 'aria-label': 'Email of the new admin' });
          email.addEventListener('keydown', function (ev) { if (ev.key === 'Enter') { ev.preventDefault(); addAdmin(email); } });
          add = row('Add an admin', 'The account that signs in with this address. It gets in the next time it opens the dashboard.',
            h('span', { class: 'actions' }, email, h('button', { class: 'btn primary', type: 'button', text: 'Add…', onclick: function () { addAdmin(email); } })));
        }
        adminsBox.appendChild(card('Admin accounts',
          info.canManage ? 'Admins can use the whole dashboard. Only owners add or remove them.'
            : 'Admins can use the whole dashboard. Only an owner can add or remove them.', [rows, add]));
      }
      function addAdmin(input) {
        var email = input.value.trim().toLowerCase();
        if (!email) { toast('Type the email address first.', 'error'); return; }
        ask({ title: 'Make ' + email + ' an admin?', body: 'They will be able to change settings, read feedback and logs, and sign people out. They cannot add or remove admins.',
          confirm: 'Make admin', danger: true }).then(function (yes) {
          if (!yes) return;
          return api.post('admin/admins', { action: 'add', email: email }).then(function (r) {
            toast(email + ' is an admin now.');
            drawAdmins(r);
          }, fail);
        });
      }
      function removeAdmin(email) {
        ask({ title: 'Remove ' + email + ' as an admin?', body: 'They lose access to the dashboard at once. Their account stays.', confirm: 'Remove', danger: true }).then(function (yes) {
          if (!yes) return;
          return api.post('admin/admins', { action: 'remove', email: email }).then(function (r) {
            toast(email + ' is not an admin any more.');
            drawAdmins(r);
          }, fail);
        });
      }
      drawAdmins(adminsInfo || { owners: [], admins: [], canManage: false });

      var out = h('div', { class: 'stack' },
        h('p', { class: 'sub', id: 'settings-store', text: where }),
        card('Look', null, row('Colour of the day', 'A different accent colour each day. Off keeps the brand coral.', h('span', { class: 'actions' }, swatch, colour.node),
          h('span', { class: 'sub', text: 'Today\'s colour is the swatch above; each visitor sees a slightly lighter or darker shade of it.' }))),
        card('Announcement', 'A banner on top of every page, for news or a planned stop. Visitors can dismiss it.', [
          row('Text', null, level, h('div', null, ann, annCount)), h('div', { class: 'form-row' }, h('div', { class: 'full' }, preview)) ]),
        card('Maintenance mode', 'Visitors can still read pages and use their files, but cannot start terminals. Admins are not affected.', [
          row('Maintenance mode', 'Shows the message below on every page and in a terminal that is refused.', maint.node),
          h('div', { class: 'form-row' }, h('div', { class: 'full' }, maintMsg, maintCount)) ]),
        card('Languages', 'Untick a language that is broken. The picker greys it out and its terminals refuse to start with a message; terminals that are already open keep running. Admins can still start it, to test a fix.', langGrid),
        card('Genie', 'The AI helper. When it is off, its buttons are hidden and the server refuses its requests, so no OpenAI calls are made for visitors.', [
          row('Genie is available', null, genie.node),
          modelToggles.map(function (t) {
            var what = t.model.provider === 'openrouter' ? 'Through OpenRouter.' : 'Through OpenAI.';
            if (!t.model.keySet) what += ' No key is set on the server, so it cannot answer yet.';
            return row(t.model.name, what, t.node);
          }),
          row('Default model', 'The model visitors start with, and the one that answers a request that names none. Built in is Luna for visitors and GPT-4o mini for older callers.', defModel),
          row('Guests', 'Requests per minute (0 or empty uses the built-in rate).', guest),
          row('Signed-in users', 'Requests per minute (0 or empty uses the built-in rate).', user) ]),
        card('OpenRouter models', 'The three models above are built in and stay. Add any other model of OpenRouter by its id (find them at openrouter.ai/models), switch it on when you want visitors to have it, and remove it when you do not. A model that is off is not offered, and the server refuses it. Save to apply.', [
          openRouterKeySet ? null : h('p', { class: 'callout', text: 'No OpenRouter key is set on the server, so these models cannot answer yet. Add one under API keys below.' }),
          h('p', { class: 'callout info', text: 'A model whose id ends in :free costs nothing, but its provider may keep and use what visitors send (their code and terminal output) and its limits are shared by everyone, so it can be busy. Visitors see it labelled free. Any other model is paid for from your OpenRouter credit. Name the providers that may answer if you want to know where the code goes; empty lets OpenRouter choose. The privacy page names only the hosts of Gemma and says other models are chosen by the site owner.' }),
          customList,
          h('div', { class: 'cm-add' }, addId, addName, addBtn) ]),
        card('Genie limits', 'What Genie is told about the page and how long its answers may be. An empty box means the built-in value. Changes reach a page the next time it loads.', [
          row('Editor code', 'Characters of the editor\'s code sent with each question.', form.ctxEditor),
          row('Terminal output, characters', 'The most recent characters of the active terminal.', form.ctxTermChars),
          row('Terminal output, lines', 'How many recent lines are read from the terminal first.', form.ctxTermLines),
          row('Conversation length', 'Messages Genie remembers (the oldest go first).', form.history),
          capRows.map(function (c) { return row('Answer size: ' + c.model.name, 'The most tokens an answer may have. A visitor asks for less by choosing a lower effort.', c.input); }),
          row('OpenRouter time limit', 'Seconds before Gemma\'s answer is given up on. Keep it under 90: the site\'s proxy gives up at about 100.', form.orTimeout),
          row('OpenRouter first host', 'Gemma is run by this host first.', form.host1),
          row('OpenRouter second host', 'Used only when the first cannot answer. No other host is ever used.', form.host2) ]),
        card('Agent mode', 'Genie works on a task in steps: it writes in the editor and runs the code, after the user allowed each kind of action and reviewed the changes. Only signed-in users have it, and it is on unless you switch it off here. Each task costs a user one request, like a chat message, however many steps it has.', [
          row('Agent mode', 'Shows the Chat | Agent switch in the Genie panel for signed-in users. Chat stays the default.', agentOn.node),
          row('On the practice page', 'Off by default: the practice page is for solving questions with an interviewer.', agentPractice.node),
          row('Tasks per hour', 'How many tasks a signed-in user may start in an hour (admins have no limit). Counted on this server and reset when it restarts.', form.agentTasks),
          row('Steps per task', 'How many times Genie may answer in one task, at most 8.', form.agentSteps) ]),
        keysBox,
        adminsBox,
        bar);
      form.ready = true;
      update();
      return out;
    }

    function refresh() {
      if (form) return Promise.resolve(); // never redraw a form the admin may be editing
      return Promise.all([api.get('admin/settings'), api.get('admin/keys'), api.get('admin/admins')]).then(function (r) { body.textContent = ''; body.appendChild(build(r[0], r[1], r[2])); });
    }
    return { root: root, refresh: refresh, poll: false, dirty: isDirty };
  }

  // ---- feedback

  function viewFeedback() {
    var root = h('div'), box = h('div');
    var data = null, show = 'unread';
    var filter = h('select', { 'aria-label': 'Which messages to show', onchange: function () { show = filter.value; box.__key = null; draw(); } },
      h('option', { value: 'unread', text: 'Unread' }), h('option', { value: 'all', text: 'All messages' }));
    root.appendChild(pageHead('Feedback', 'Messages from the feedback form on the site.'));
    root.appendChild(h('div', { class: 'toolbar' }, filter, h('span', { class: 'grow' }), h('span', { class: 'sub', id: 'fb-count' }),
      h('a', { class: 'btn', href: BASE + 'admin/feedback?format=csv', download: 'openrepl-feedback.csv', text: 'Export CSV' })));
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

    function draw() {
      var rows = data.feedback.filter(function (f) { return show === 'all' || !f.read; });
      $('fb-count').textContent = plural(data.unread, 'unread message', 'unread messages') + ' · ' + data.feedback.length + ' in all';
      var list = h('div', { class: 'stack' });
      rows.forEach(function (f) {
        list.appendChild(h('div', { class: 'card' },
          h('div', { class: 'toolbar' },
            f.read ? null : pill('new', 'info'),
            h('b', { text: f.name || 'Anonymous' }),
            f.email ? h('a', { href: 'mailto:' + f.email, text: f.email }) : null,
            h('span', { class: 'grow' }),
            h('span', { class: 'sub' }, when(f.time)),
            h('button', { class: 'btn small', type: 'button', text: f.read ? 'Mark unread' : 'Mark read', onclick: function () { setRead(f, !f.read); } }),
            h('button', { class: 'btn small danger', type: 'button', text: 'Delete', onclick: function () { remove(f); } })),
          h('div', { class: 'wrap-text', text: f.message || '(empty message)' })));
      });
      render(box, JSON.stringify([rows, show]), rows.length ? list : h('div', { class: 'card' }, emptyState(show === 'unread' ? 'No unread messages. 🎉' : 'No feedback yet.')));
      setUnread(data.unread);
    }

    function refresh() { return api.get('admin/feedback').then(function (r) { data = r; draw(); }); }
    return { root: root, refresh: refresh, poll: true };
  }

  // ---- snippets

  function viewSnippets() {
    var root = h('div'), box = h('div');
    var data = null;
    root.appendChild(pageHead('Shared code', 'Snippets visitors saved with "Share code". Remove one that should not be public.'));
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
      { key: 'bytes', label: 'Size', num: true, cell: function (s) { return s.bytes + ' B'; } },
      { key: 'preview', label: 'Code', cell: function (s) { return h('div', { class: 'clip mono', text: s.preview.replace(/\s+/g, ' '), title: s.preview.slice(0, 200) }); } },
      { key: 'actions', label: '', cell: function (s) {
        return h('div', { class: 'actions' }, h('button', { class: 'btn small', type: 'button', text: 'View', onclick: function () { view(s); } }),
          h('button', { class: 'btn small danger', type: 'button', text: 'Delete', onclick: function () { remove(s); } })); } }
    ];

    function refresh() {
      return api.get('admin/snippets').then(function (r) {
        data = r;
        if (r.off) { render(box, 'off', h('div', { class: 'callout', text: 'The snippet database is not open on this server, so sharing is off.' })); return; }
        render(box, JSON.stringify(r.snippets), table(cols, r.snippets, { empty: 'Nobody has shared code yet.' }));
      });
    }
    return { root: root, refresh: refresh, poll: true };
  }

  // ---- users

  function viewUsers() {
    var root = h('div'), box = h('div');
    var data = null, query = '';
    var search = h('input', { type: 'search', placeholder: 'Search by name or email', 'aria-label': 'Search accounts',
      oninput: function () { query = search.value.trim().toLowerCase(); box.__key = null; draw(); } });
    root.appendChild(pageHead('Users', 'Accounts that have signed in. Signing a user out ends their sessions; blocking also stops them signing in again.'));
    root.appendChild(h('div', { class: 'toolbar' }, search, h('span', { class: 'grow' }), h('span', { class: 'sub', id: 'users-count' })));
    root.appendChild(box);

    function act(u, action, title, body, confirm, done, danger) {
      return ask({ title: title, body: body, confirm: confirm, danger: danger }).then(function (yes) {
        if (!yes) return;
        return api.post('admin/users/' + encodeURIComponent(u.uid) + '/' + action).then(function () { toast(done); }, fail).then(refreshCurrent);
      });
    }

    var cols = [
      { key: 'email', label: 'Account', cell: function (u) { return h('div', null, h('b', { text: u.email || '(no email)' }), h('div', { class: 'sub', text: u.name && u.name !== u.email ? u.name : '' })); } },
      { key: 'sessions', label: 'Signed in', num: true, cell: function (u) { return u.sessions ? plural(u.sessions, 'session', 'sessions') : h('span', { class: 'sub', text: 'no' }); } },
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
      var rows = data.users.filter(function (u) { return !query || (u.email + ' ' + u.name).toLowerCase().indexOf(query) >= 0; });
      $('users-count').textContent = plural(rows.length, 'account', 'accounts') + (rows.length !== data.users.length ? ' of ' + data.users.length : '');
      render(box, JSON.stringify(rows), table(cols, rows, { empty: data.users.length ? 'No account matches.' : 'Nobody has signed in yet.' }));
    }
    function refresh() { return api.get('admin/users').then(function (r) { data = r; draw(); }); }
    return { root: root, refresh: refresh, poll: true };
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
      h('span', { class: 'grow' }), h('button', { class: 'btn', type: 'button', text: 'Refresh', onclick: function () { load(); } })));
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

  // ---- navigation --------------------------------------------------------------

  var VIEWS = {
    overview: { label: 'Overview', icon: 'overview', make: viewOverview },
    workers: { label: 'Workers', icon: 'workers', make: viewWorkers, gateway: true },
    sessions: { label: 'Sessions', icon: 'sessions', make: viewSessions, gateway: true },
    site: { label: 'Site settings', icon: 'site', make: viewSite },
    feedback: { label: 'Feedback', icon: 'feedback', make: viewFeedback, badge: true },
    snippets: { label: 'Shared code', icon: 'snippets', make: viewSnippets },
    users: { label: 'Users', icon: 'users', make: viewUsers },
    health: { label: 'Health', icon: 'health', make: viewHealth },
    params: { label: 'Parameters', icon: 'params', make: viewParams },
    logs: { label: 'Log', icon: 'logs', make: viewLogs },
    audit: { label: 'Audit log', icon: 'audit', make: viewAudit }
  };

  var GROUPS = [
    ['', ['overview']],
    ['Fleet', ['workers', 'sessions']],
    ['Site', ['site', 'feedback', 'snippets', 'users']],
    ['System', ['health', 'params', 'logs', 'audit']]
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
