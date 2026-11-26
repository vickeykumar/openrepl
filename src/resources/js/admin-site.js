/*
 * Site settings of the admin dashboard (admin.html). admin.js loads this file when the Site settings page is opened, so that the other
 * pages do not carry it: it is a third of the dashboard's code. It puts its view on window.AdminSite; the helpers it needs from admin.js
 * are on window.AdminKit. Same rules as admin.js: everything from the server is put on the page with textContent only.
 */
(function () {
  'use strict';

  var K = window.AdminKit;
  var api = K.api, ask = K.ask, colourOfTheDay = K.colourOfTheDay, fail = K.fail, h = K.h, pageHead = K.pageHead, toast = K.toast;

  // ---- the colour card: colour of the day, the palette and a picker ---------------------------------------------------
  //
  // The palette is the one of admin.js (K.colours, the same 62 colours the visitors' page draws from). State: `daily` is the
  // form's switch, `accent` the colour chosen by hand and saved ('' = none), `skip` the palette entries the colour of the
  // day never picks, `sel` the colour being looked at (not saved until "Apply colour").
  var PRESETS = ['#ffffff', '#b0b0b0', '#666666', '#000000', '#ff0000', '#ff8a00', '#ffee00', '#00e000', '#00ffff', '#0033ff', '#7a1cff', '#ff6fa5'];
  var HEX6 = /^#[0-9a-f]{6}$/;

  function hexToRgb(hex) { return [parseInt(hex.slice(1, 3), 16), parseInt(hex.slice(3, 5), 16), parseInt(hex.slice(5, 7), 16)]; }
  function rgbToHex(r, g, b) { return '#' + [r, g, b].map(function (v) { v = Math.max(0, Math.min(255, Math.round(v))); return (v < 16 ? '0' : '') + v.toString(16); }).join(''); }
  function hsvToRgb(h, s, v) {
    var c = v * s, x = c * (1 - Math.abs((h / 60) % 2 - 1)), m = v - c, r = 0, g = 0, b = 0;
    if (h < 60) { r = c; g = x; } else if (h < 120) { r = x; g = c; } else if (h < 180) { g = c; b = x; }
    else if (h < 240) { g = x; b = c; } else if (h < 300) { r = x; b = c; } else { r = c; b = x; }
    return [(r + m) * 255, (g + m) * 255, (b + m) * 255];
  }
  function rgbToHsv(r, g, b) {
    r /= 255; g /= 255; b /= 255;
    var max = Math.max(r, g, b), min = Math.min(r, g, b), d = max - min, hue = 0;
    if (d) {
      if (max === r) hue = ((g - b) / d) % 6; else if (max === g) hue = (b - r) / d + 2; else hue = (r - g) / d + 4;
      hue *= 60; if (hue < 0) hue += 360;
    }
    return { h: hue, s: max ? d / max : 0, v: max };
  }
  // "#abc", "abc", "#aabbcc" and "AABBCC" are read; anything else gives ''
  function readHex(text) {
    var t = String(text || '').trim().toLowerCase();
    if (t.charAt(0) !== '#') t = '#' + t;
    if (/^#[0-9a-f]{3}$/.test(t)) t = '#' + t.charAt(1) + t.charAt(1) + t.charAt(2) + t.charAt(2) + t.charAt(3) + t.charAt(3);
    return HEX6.test(t) ? t : '';
  }

  function colourCard(settings, hooks) {
    var palette = K.colours.map(function (c) { return c.toLowerCase(); });
    var daily = !!settings.colorOfTheDay;
    var accent = readHex(settings.accentColor);
    var skip = {};
    (settings.disabledColours || []).forEach(function (c) { c = readHex(c); if (c) skip[c] = true; });
    var sel = accent || colourOfTheDay(Object.keys(skip)).toLowerCase();
    var hsv = rgbToHsv.apply(null, hexToRgb(sel));
    var tab = 'palette';

    function skipped() { return Object.keys(skip).sort(); }
    function dayColour() { return colourOfTheDay(skipped()).toLowerCase(); }
    function pick(hex, from) { sel = hex; if (from !== 'hsv') hsv = (function (n) { if (!n.s || !n.v) n.h = hsv.h; return n; })(rgbToHsv.apply(null, hexToRgb(hex))); render(from); }

    // -- the current colour
    var curSwatch = h('span', { class: 'cp-swatch' }), curHex = h('b', { class: 'cp-hex' }), curId = h('span', { class: 'cp-pill' }), curSrc = h('span', { class: 'cp-src' });
    var stopBtn = h('button', { type: 'button', class: 'btn', text: 'Use the colour of the day again', onclick: function () { accent = ''; render(); hooks.change(); } });
    var current = h('div', { class: 'cp-current' }, curSwatch, h('div', { class: 'cp-vr' }),
      h('div', { class: 'cp-cur-text' }, h('span', { class: 'sub', text: 'Current colour' }), h('div', { class: 'cp-cur-row' }, curHex, curId)),
      h('div', { class: 'cp-grow' }), curSrc);

    // -- the palette
    var grid = h('div', { class: 'cp-grid', role: 'listbox', 'aria-label': 'Palette' }), cells = {};
    var skipNote = h('p', { class: 'sub cp-skipnote' });
    palette.forEach(function (c, i) {
      var main = h('button', { type: 'button', class: 'cp-cell', role: 'option', 'aria-label': c + ', ' + (i + 1) + ' of ' + palette.length, title: c, vars: { '--sw': c },
        onclick: function () { pick(c); } });
      var off = h('button', { type: 'button', class: 'cp-skip', title: 'Keep ' + c + ' out of the colour of the day', 'aria-label': 'Keep ' + c + ' out of the colour of the day', text: '×',
        onclick: function (e) {
          e.stopPropagation();
          if (skip[c]) delete skip[c];
          else if (Object.keys(skip).length >= palette.length - 1) { toast('At least one colour has to stay in the colour of the day.', 'error'); return; }
          else skip[c] = true;
          render(); hooks.change();
        } });
      cells[c] = { wrap: h('span', { class: 'cp-cellwrap' }, main, off), main: main, off: off };
      grid.appendChild(cells[c].wrap);
    });
    var palettePanel = h('div', { class: 'cp-panel' }, grid, skipNote);

    // -- the picker: a hue ring with a saturation/brightness square inside
    var ring = h('div', { class: 'cp-ring', tabindex: '0', role: 'slider', 'aria-label': 'Hue', 'aria-valuemin': '0', 'aria-valuemax': '360' });
    var ringKnob = h('span', { class: 'cp-knob' }), sqKnob = h('span', { class: 'cp-knob' });
    var square = h('div', { class: 'cp-square', tabindex: '0', role: 'group', 'aria-label': 'Saturation and brightness (arrow keys)' }, sqKnob);
    var wheel = h('div', { class: 'cp-wheel' }, ring, ringKnob, square);

    function drag(el, onMove) {
      el.addEventListener('pointerdown', function (e) {
        if (e.button !== 0) return;
        var box = (el === ring ? wheel : el).getBoundingClientRect();
        if (el === ring) { // only the ring itself, not the square that sits over its middle
          var dx = e.clientX - (box.left + box.width / 2), dy = e.clientY - (box.top + box.height / 2), r = Math.sqrt(dx * dx + dy * dy) / (box.width / 2);
          if (r < 0.7 || r > 1.02) return;
        }
        e.preventDefault();
        el.setPointerCapture(e.pointerId);
        el.__drag = true; el.focus({ preventScroll: true });
        onMove(e, box);
      });
      el.addEventListener('pointermove', function (e) { if (el.__drag) onMove(e, (el === ring ? wheel : el).getBoundingClientRect()); });
      function end(e) { el.__drag = false; try { el.releasePointerCapture(e.pointerId); } catch (x) { /* already released */ } }
      el.addEventListener('pointerup', end); el.addEventListener('pointercancel', end);
    }
    function setHSV(nh, ns, nv) {
      hsv = { h: ((nh % 360) + 360) % 360, s: Math.max(0, Math.min(1, ns)), v: Math.max(0, Math.min(1, nv)) };
      sel = rgbToHex.apply(null, hsvToRgb(hsv.h, hsv.s, hsv.v));
      render('hsv');
    }
    drag(ring, function (e, box) {
      var a = Math.atan2(e.clientY - (box.top + box.height / 2), e.clientX - (box.left + box.width / 2)) * 180 / Math.PI + 90;
      setHSV(a, hsv.s, hsv.v);
    });
    drag(square, function (e, box) { setHSV(hsv.h, (e.clientX - box.left) / box.width, 1 - (e.clientY - box.top) / box.height); });
    ring.addEventListener('keydown', function (e) {
      var d = { ArrowRight: 1, ArrowUp: 1, ArrowLeft: -1, ArrowDown: -1 }[e.key];
      if (d) { e.preventDefault(); setHSV(hsv.h + d * (e.shiftKey ? 10 : 1), hsv.s, hsv.v); }
    });
    square.addEventListener('keydown', function (e) {
      var st = e.shiftKey ? 0.1 : 0.01, m = { ArrowRight: [st, 0], ArrowLeft: [-st, 0], ArrowUp: [0, st], ArrowDown: [0, -st] }[e.key];
      if (m) { e.preventDefault(); setHSV(hsv.h, hsv.s + m[0], hsv.v + m[1]); }
    });

    // -- the selected colour
    var preview = h('span', { class: 'cp-swatch cp-big' }), bigHex = h('b', { class: 'cp-hex' }), rgbText = h('span', { class: 'sub' }), kind = h('span', { class: 'cp-pill' });
    function num(label) {
      return h('input', { type: 'number', class: 'field cp-num', min: '0', max: '255', step: '1', inputmode: 'numeric', 'aria-label': label + ' (0 to 255)' });
    }
    var rIn = num('Red'), gIn = num('Green'), bIn = num('Blue');
    [rIn, gIn, bIn].forEach(function (el) {
      el.addEventListener('input', function () {
        if (el.value === '') return;
        var v = [rIn, gIn, bIn].map(function (x) { return parseInt(x.value, 10); });
        if (v.some(function (n) { return isNaN(n) || n < 0 || n > 255; })) { el.classList.add('invalid'); return; }
        el.classList.remove('invalid');
        pick(rgbToHex(v[0], v[1], v[2]), 'rgb');
      });
      el.addEventListener('blur', function () { el.classList.remove('invalid'); render(); });
    });
    var hexIn = h('input', { type: 'text', class: 'field cp-hexin', maxlength: '7', spellcheck: 'false', autocomplete: 'off', 'aria-label': 'HEX colour' });
    hexIn.addEventListener('input', function () {
      var v = readHex(hexIn.value);
      if (!v) { hexIn.classList.add('invalid'); return; }
      hexIn.classList.remove('invalid');
      pick(v, 'hex');
    });
    hexIn.addEventListener('blur', function () { hexIn.classList.remove('invalid'); render(); });
    var copy = h('button', { type: 'button', class: 'icon-btn cp-copy', title: 'Copy HEX', 'aria-label': 'Copy HEX', text: '⧉', onclick: function () {
      var text = sel.toUpperCase();
      var done = function () { toast(text + ' copied.'); }, failed = function () { toast('Could not copy. Select the HEX and copy it by hand.', 'error'); };
      if (navigator.clipboard && navigator.clipboard.writeText) { navigator.clipboard.writeText(text).then(done, failed); return; }
      var ta = h('textarea', { 'aria-hidden': 'true', style: 'position:fixed;opacity:0' }); ta.value = text; document.body.appendChild(ta); ta.select();
      try { document.execCommand('copy') ? done() : failed(); } catch (x) { failed(); }
      ta.remove();
    } });
    var presets = h('div', { class: 'cp-presets' }, PRESETS.map(function (c) {
      return h('button', { type: 'button', class: 'cp-preset', title: c, 'aria-label': 'Preset ' + c, vars: { '--sw': c }, onclick: function () { pick(c); } });
    }));
    var info = h('div', { class: 'cp-info' },
      h('b', { text: 'Selected colour' }),
      h('div', { class: 'cp-sel' }, preview, h('div', null, bigHex, rgbText, h('div', null, kind))),
      h('div', { class: 'cp-inputs' },
        h('label', null, h('span', { text: 'R' }), rIn), h('label', null, h('span', { text: 'G' }), gIn), h('label', null, h('span', { text: 'B' }), bIn),
        h('label', { class: 'cp-hexlab' }, h('span', { text: 'HEX' }), h('span', { class: 'cp-hexwrap' }, hexIn, copy))),
      h('span', { class: 'sub', text: 'Quick presets' }), presets);
    var pickerPanel = h('div', { class: 'cp-panel cp-picker' }, wheel, h('div', { class: 'cp-vr' }), info);

    // -- the tabs
    var tabPal = h('button', { type: 'button', class: 'cp-tab', role: 'tab', text: 'Palette (' + palette.length + ')', onclick: function () { show('palette'); } });
    var tabPick = h('button', { type: 'button', class: 'cp-tab', role: 'tab', text: 'Picker (Custom)', onclick: function () { show('picker'); } });
    function show(t) { tab = t; render('tab'); }

    // -- the actions
    var randomBtn = h('button', { type: 'button', class: 'btn cp-btn cp-pink', text: '⤮  Generate random theme', onclick: function () {
      var pool = palette.filter(function (c) { return !skip[c] && c !== sel; });
      if (!pool.length) { toast('No other colour is left in the colour of the day.', 'error'); return; }
      var c = pool[Math.floor(Math.random() * pool.length)];
      pick(c);
      toast(c.toUpperCase() + ' is selected. Press Apply colour to use it on the site.');
    } });
    var resetBtn = h('button', { type: 'button', class: 'btn cp-btn cp-outline', text: '↺  Reset to original theme', onclick: function () {
      ask({ title: 'Go back to the original theme?', body: 'The site returns to its brand coral: the colour chosen by hand is removed and the colour of the day is switched off. The colours you skipped stay skipped. This saves the settings.', confirm: 'Reset theme' }).then(function (yes) {
        if (!yes) return;
        accent = ''; hooks.setDaily(false);
        sel = '#fe6a6b'; hsv = rgbToHsv.apply(null, hexToRgb(sel)); render();
        hooks.save('The original theme is back. Visitors see it on their next page load.');
      });
    } });
    var applyBtn = h('button', { type: 'button', class: 'btn cp-btn cp-pink', text: '✓  Apply colour', onclick: function () {
      accent = sel; render();
      hooks.save(sel.toUpperCase() + ' is now the site colour. Visitors see it on their next page load.');
    } });

    // -- drawing
    function render(from) {
      var shown = accent || (daily ? dayColour() : '#fe6a6b');
      curSwatch.style.setProperty('--sw', shown);
      curHex.textContent = shown.toUpperCase();
      var at = palette.indexOf(shown);
      curId.textContent = at >= 0 ? 'ID: ' + (at + 1) + ' / ' + palette.length : 'Custom';
      curSrc.textContent = accent ? 'Chosen by hand' : daily ? 'From PopularColors palette' : 'Brand coral (colour of the day is off)';
      curSrc.title = accent ? 'This colour is used until you pick the colour of the day again.' : '';
      if (accent && !current.contains(stopBtn)) current.appendChild(stopBtn);
      if (!accent && current.contains(stopBtn)) stopBtn.remove();

      tabPal.className = 'cp-tab' + (tab === 'palette' ? ' on' : ''); tabPal.setAttribute('aria-selected', String(tab === 'palette'));
      tabPick.className = 'cp-tab' + (tab === 'picker' ? ' on' : ''); tabPick.setAttribute('aria-selected', String(tab === 'picker'));
      palettePanel.hidden = tab !== 'palette'; pickerPanel.hidden = tab !== 'picker';

      var n = skipped().length;
      skipNote.textContent = 'Click a colour to select it. The small × on a colour keeps it out of the colour of the day (' + (n ? n + ' skipped, ' : 'none skipped, ') + (palette.length - n) + ' can be picked). It does not change the colour already in use.';
      palette.forEach(function (c) {
        var cell = cells[c];
        cell.main.className = 'cp-cell' + (c === sel ? ' on' : '') + (skip[c] ? ' off' : '');
        cell.main.setAttribute('aria-selected', String(c === sel));
        cell.off.className = 'cp-skip' + (skip[c] ? ' on' : '');
        cell.off.textContent = skip[c] ? '+' : '×';
        cell.off.title = skip[c] ? 'Allow ' + c + ' in the colour of the day' : 'Keep ' + c + ' out of the colour of the day';
        cell.off.setAttribute('aria-label', cell.off.title);
      });

      var rgb = hexToRgb(sel);
      preview.style.setProperty('--sw', sel);
      bigHex.textContent = sel.toUpperCase();
      rgbText.textContent = 'RGB(' + rgb.join(', ') + ')';
      var pi = palette.indexOf(sel);
      kind.textContent = pi >= 0 ? 'Palette · ID ' + (pi + 1) : 'Custom';
      if (from !== 'rgb') { rIn.value = rgb[0]; gIn.value = rgb[1]; bIn.value = rgb[2]; }
      if (from !== 'hex') { hexIn.value = sel.toUpperCase(); hexIn.classList.remove('invalid'); }

      wheel.style.setProperty('--hue', String(Math.round(hsv.h)));
      var a = (hsv.h - 90) * Math.PI / 180;
      ringKnob.style.left = (50 + 43.5 * Math.cos(a)) + '%'; ringKnob.style.top = (50 + 43.5 * Math.sin(a)) + '%';
      ringKnob.style.setProperty('--sw', rgbToHex.apply(null, hsvToRgb(hsv.h, 1, 1)));
      ring.setAttribute('aria-valuenow', String(Math.round(hsv.h)));
      sqKnob.style.left = (hsv.s * 100) + '%'; sqKnob.style.top = ((1 - hsv.v) * 100) + '%';
      sqKnob.style.setProperty('--sw', sel);
    }

    var daySwitch = hooks.switchNode;
    var node = h('div', { class: 'colour-picker' },
      h('div', { class: 'cp-head' }, h('div', null, h('h3', { text: 'Colour of the day' }), h('p', { class: 'sub', text: 'A different accent colour every day.' })), daySwitch),
      current,
      h('p', { class: 'sub', text: 'Today\'s colour is the swatch above; each visitor sees a slightly lighter or darker shade of it.' }),
      h('hr', { class: 'cp-rule' }),
      h('h3', { text: 'Choose a colour' }), h('p', { class: 'sub', text: 'Set the theme colour from the palette or pick any custom colour.' }),
      h('div', { class: 'cp-tabs', role: 'tablist' }, tabPal, tabPick),
      palettePanel, pickerPanel,
      h('hr', { class: 'cp-rule' }),
      h('h3', { text: 'Theme actions' }), h('p', { class: 'sub', text: 'Change the theme manually or restore the original theme. Apply colour and Reset save the settings.' }),
      h('div', { class: 'cp-actions' }, randomBtn, resetBtn, applyBtn));
    render();
    return {
      node: node,
      refresh: function (on) { daily = on; render('tab'); },
      accent: function () { return accent; },
      skipped: skipped
    };
  }

  function viewSite() {
    var root = h('div'), body = h('div');
    var saved = null, form = null, languages = [], models = [], pluginChoices = [], saving = false, keysState = null, adminsState = null, plugsState = null, connState = null;
    root.appendChild(pageHead('Site settings', 'Switches that change what every visitor sees. Changes apply to the next page a visitor loads.'));
    root.appendChild(body);

    // ---- the models an admin adds, from any provider (the built-in ones stay as they are)
    var CUSTOM_ID = /^[a-z0-9][a-z0-9._-]{0,63}\/[a-z0-9][a-z0-9._:+-]{0,95}$/;
    var CF_ID = /^@cf\/[a-z0-9][a-z0-9._-]{0,63}\/[a-z0-9][a-z0-9._:+-]{0,95}$/;
    var OPENAI_ID = /^[a-z0-9][a-z0-9._:-]{0,79}$/;
    var PROVIDER_NAMES = { '': 'OpenRouter', openrouter: 'OpenRouter', openai: 'OpenAI', cloudflare: 'Cloudflare Workers AI' };
    function providerOf(c) { return c.provider === 'openai' || c.provider === 'cloudflare' ? c.provider : ''; }
    function idOk(provider, id) {
      if (provider === 'openai') return OPENAI_ID.test(id);
      if (provider === 'cloudflare') return CF_ID.test(id);
      return CUSTOM_ID.test(id);
    }
    var ID_HELP = {
      '': 'An OpenRouter id looks like author/model-name, for example nvidia/nemotron-3-super-120b-a12b:free.',
      openai: 'An OpenAI model name looks like gpt-4.1-mini.',
      cloudflare: 'A Cloudflare id starts with @cf/, for example @cf/qwen/qwen3-30b-a3b-fp8. Keep the @cf/: without it Cloudflare bills your AI Gateway credit instead of the free allowance.'
    };
    function hostsFrom(text) {
      var seen = {}, out = [];
      String(text || '').toLowerCase().split(/[\s,]+/).forEach(function (x) { if (x && !seen[x]) { seen[x] = 1; out.push(x); } });
      return out;
    }
    // one shape for a model from the server and from a row of the form, so that they compare
    function cleanCustom(c) {
      var id = String(c.id || '').trim().toLowerCase();
      var provider = providerOf(c);
      return { provider: provider, id: id, name: String(c.name || '').trim() || id, enabled: !!c.enabled, free: !!c.free || /:free$/.test(id),
        answerTokens: int(c.answerTokens), thinkingRoom: int(c.thinkingRoom), jsonMode: !!c.jsonMode,
        reasoning: provider === 'openai' && !!c.reasoning, small: c.small === true || c.small === false ? c.small : null, noGuests: !!c.noGuests, hosts: provider === '' ? (c.hosts || []).slice() : [] };
    }
    // the Small tick: left as the id says (null) until the admin changes it
    function smallNow(r) {
      var byId = window.AdminSections.isSmallModel(r.id);
      return r.small.checked === byId ? (r.smallSet ? r.small.checked : null) : r.small.checked;
    }
    function readCustom(r) {
      return cleanCustom({ provider: r.provider, id: r.id, name: r.name.value, enabled: r.on.checked, free: r.free.checked, answerTokens: r.answer.value,
        thinkingRoom: r.room.value, jsonMode: r.json.checked, reasoning: r.reasoning ? r.reasoning.checked : false, small: smallNow(r), noGuests: !r.guests.checked, hosts: r.hosts ? hostsFrom(r.hosts.value) : [] });
    }
    // the models that start in the short list: the built-in ones that are ticked, then the added ones
    // the built-in models kept for signed-in users: the guest ticks that are off
    function guestBlockedNow() {
      return models.filter(function (m) { return form.guestBoxes && form.guestBoxes[m.id] && !form.guestBoxes[m.id].checked; }).map(function (m) { return m.id; }).sort();
    }
    function featuredNow() {
      var ids = models.filter(function (m) { return form.featured && form.featured[m.id] && form.featured[m.id].checked; }).map(function (m) { return m.id; });
      (form.custom || []).forEach(function (r) { if (r.featured && r.featured.checked) ids.push(r.id); });
      return ids;
    }

    // the hosts of the list of servers: one for each line or separated by commas, lower case, no repeats
    function hostsFromText(text) {
      var seen = {}, out = [];
      String(text || '').split(/[\s,]+/).forEach(function (h) { h = h.trim().toLowerCase(); if (h && !seen[h]) { seen[h] = true; out.push(h); } });
      return out;
    }
    function sitePluginsNow() {
      return pluginChoices.filter(function (p) { return form.sitePlug && form.sitePlug[p.id] && form.sitePlug[p.id].checked; }).map(function (p) { return p.id; }).sort();
    }

    function snapshot() {
      return {
        colorOfTheDay: form.colour.checked,
        accentColor: form.colours.accent(), disabledColours: form.colours.skipped(),
        announcement: { text: form.ann.value.trim(), level: form.level.value },
        maintenance: { enabled: form.maint.checked, message: form.maintMsg.value.trim() },
        disabledLanguages: languages.filter(function (l) { return !form.langs[l.value].checked; }).map(function (l) { return l.value; }).sort(),
        dbCapacityMB: int(form.dbCap.value),
        maxEditorFileMB: int(form.maxFile.value),
        snapshotIntervalMinutes: int(form.snapEvery.value),
        previewLinkGuestMinutes: int(form.previewLifeGuest.value),
        previewLinkUserMinutes: int(form.previewLifeUser.value),
        previewBaseURL: form.previewBaseURL.value.trim(),
        shareLinkGuestMinutes: int(form.shareLifeGuest.value),
        shareLinkUserMinutes: int(form.shareLifeUser.value),
        sessionCleanupHours: int(form.sessionCleanup.value),
        snippetMaxDays: int(form.snippetMax.value),
        previewOff: !form.previewOn.checked, practiceWorkspaceOff: !form.practiceWs.checked,
        previewGuestsOff: !form.previewGuests.checked, previewAgentOff: !form.previewAgent.checked, canvasOff: !form.canvasOn.checked,
        egressOn: form.egress.checked,
        ideUsageOff: !form.ideOn.checked, ideAdminsCounted: !form.ideAdmins.checked, idePartsOff: idePartsOffNow(),
        ideTermMB: int(form.ideTermMB.value), ideTermWindowSecs: int(form.ideTermWindow.value), ideTermGraceSecs: int(form.ideTermGrace.value), ideTermPauseOnly: form.ideTermAction.value === 'pause',
        ideGuestMB: int(form.ideGuestMB.value), ideUserMB: int(form.ideUserMB.value), ideWarnPct: int(form.ideWarnPct.value), ideReserveMB: int(form.ideReserveMB.value), ideSmallKB: int(form.ideSmallKB.value),
        ideResetMinutes: ideResetNow(),
        genieEventsOff: !form.genieEvents.checked, genieEventDays: int(form.genieDays.value),
        genieHealthErrPct: int(form.healthErr.value), genieHealthSlowMs: int(form.healthSlow.value),
        genie: { disabled: !form.genie.checked, guestPerMinute: num(form.guest.value), userPerMinute: num(form.user.value),
          disabledModels: models.filter(function (m) { return !form.models[m.id].checked; }).map(function (m) { return m.id; }).sort(),
          customModels: (form.custom || []).map(readCustom),
          featuredModels: featuredNow(),
          defaultModel: form.defModel.value, agentModel: form.agentModel.value, agentEffort: form.agentEffort.value, agentModels: form.agentList(), guestBlockedModels: guestBlockedNow(),
          contextEditorChars: int(form.ctxEditor.value), contextTerminalChars: int(form.ctxTermChars.value),
          contextTerminalLines: int(form.ctxTermLines.value), historyMessages: int(form.history.value),
          contextAttachItems: int(form.attachItems.value), contextAttachChars: int(form.attachChars.value), contextAttachTotal: int(form.attachTotal.value),
          openRouterTimeoutSec: int(form.orTimeout.value),
          answerCaps: capsOf(models.map(function (m) { return [m.id, int(form.caps[m.id].value)]; })),
          openRouterHosts: hostsOf(form.host1.value, form.host2.value),
          modelPrices: pricesOf(), showCostToUsers: form.showCost.checked,
          agentNativeTools: form.agentTools.checked, streamAnswers: form.streamOn.checked,
          nativeToolModels: models.filter(function (m) { return form.toolModels[m.id] && form.toolModels[m.id].checked; }).map(function (m) { return m.id; }),
          chatHistoryOff: !form.chatOn.checked, chatHistoryNoGuests: !form.chatGuests.checked,
          summaryOff: !form.sumOn.checked, summaryTokens: int(form.sumChars.value),
          chatHistoryChats: int(form.chatMax.value), chatHistoryTabs: int(form.chatTabs.value), chatHistoryKB: int(form.chatKB.value), chatHistoryDays: int(form.chatDays.value),
          meterOff: !form.meterOn.checked, creditsOn: form.creditsOn.checked, planName: form.planName.value.trim(),
          planCredits: num(form.planCredits.value), planBudget: num(form.planBudget.value), creditWeights: weightsOf(),
          agentDisabled: !form.agentOn.checked, agentOnPractice: form.agentPractice.checked,
          agentAutoOff: !form.agentAuto.checked, agentRiskyOff: !form.agentRisky.checked, agentShellOff: !form.agentShell.checked,
          extensionsOff: !form.extOn.checked, pluginsFromLinks: form.plgLinks.checked, sitePlugins: sitePluginsNow(),
          connectionsOff: !form.connOn.checked, connectionsNoApproval: form.connNoApproval.checked, connectionHosts: hostsFromText(form.connHosts.value),
          agentTasksPerHour: int(form.agentTasks.value), agentMaxSteps: int(form.agentSteps.value),
          escalationModels: form.escalation.value.split(',').map(function (x) { return x.trim(); }).filter(Boolean),
          autoOn: form.autoOn.checked, autoSmall: form.autoSmall.value, autoStrong: form.autoStrong.value, autoDefault: form.autoDefault.value }
      };
    }
    function int(v) { v = parseInt(v, 10); return isNaN(v) ? 0 : v; }
    // the IDE data usage (LLD 20): the parts that are switched off, and the minute of the day (UTC) a day starts at
    var IDE_PARTS = [['terminal', 'Terminal'], ['editor', 'Editor'], ['files', 'File browser'], ['genie', 'Genie'], ['preview', 'Preview']];
    function idePartsOffNow() {
      return IDE_PARTS.filter(function (p) { return form.ideParts && form.ideParts[p[0]] && !form.ideParts[p[0]].checked; }).map(function (p) { return p[0]; }).sort();
    }
    function ideResetNow() {
      var m = /^(\d{1,2}):(\d{2})$/.exec(form.ideReset ? form.ideReset.value : '');
      return m ? Math.min(1439, parseInt(m[1], 10) * 60 + parseInt(m[2], 10)) : 0;
    }
    function ideTimeText(minutes) { return ('0' + Math.floor(minutes / 60)).slice(-2) + ':' + ('0' + (minutes % 60)).slice(-2); }
    // the time of a day start in the viewer's own time zone: "5:30 AM"
    function ideLocalTime(minutes) {
      var d = new Date(Date.UTC(2026, 0, 1, Math.floor(minutes / 60), minutes % 60));
      return d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
    }
    // the models that take the native tool: the admin's list, or, while there is none, the OpenAI models
    function toolModelsOf(list) {
      return models.filter(function (m) { return list ? list.indexOf(m.id) >= 0 : m.provider === 'openai'; }).map(function (m) { return m.id; });
    }
    // the prices that are set, as {model: {in, out}} (dollars for a million tokens)
    function pricesOf() {
      var o = {};
      models.forEach(function (m) {
        var p = form.price && form.price[m.id];
        if (!p) return;
        var i = parseFloat(p.in.value), u = parseFloat(p.out.value);
        i = isNaN(i) ? 0 : i; u = isNaN(u) ? 0 : u;
        if (i > 0 || u > 0) o[m.id] = { in: i, out: u };
      });
      return o;
    }
    // the credit weights that are set, as {model: {in, out}} (credits for a thousand tokens)
    function weightsOf() {
      var o = {};
      models.forEach(function (m) {
        var p = form.weight && form.weight[m.id];
        if (!p) return;
        var i = parseFloat(p.in.value), u = parseFloat(p.out.value);
        i = isNaN(i) ? 0 : i; u = isNaN(u) ? 0 : u;
        if (i > 0 || u > 0) o[m.id] = { in: i, out: u };
      });
      return o;
    }
    function pricesClean(src) {
      var o = {};
      models.forEach(function (m) {
        var p = (src || {})[m.id];
        if (p && (p.in > 0 || p.out > 0)) o[m.id] = { in: p.in || 0, out: p.out || 0 };
      });
      return o;
    }
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
    // today's IDE data usage of all users: the totals, and the accounts that sent the most
    function ideMB(n) { n = n / 1048576; return (n >= 1024 ? (n / 1024).toFixed(2) + ' GB' : n >= 10 ? Math.round(n) + ' MB' : n.toFixed(1) + ' MB'); }
    function drawIDEToday(todayBox, topBox, info) {
      todayBox.textContent = '';
      topBox.textContent = '';
      todayBox.appendChild(h('div', { class: 'big', style: 'font-size:1.8rem;font-weight:700', text: ideMB(info.total || 0) }));
      todayBox.appendChild(h('p', { class: 'sub', text: 'Guests (combined): ' + ideMB(info.guestBytes || 0) + ' in ' + (info.guestsNow || 0) + ' sessions now. Signed in: ' + (info.signedCount || 0) + ' accounts.' }));
      todayBox.appendChild(h('p', { class: 'sub', text: 'At the limit now: ' + (info.atLimit || 0) }));
      var top = info.top || [];
      if (!top.length) { topBox.appendChild(h('p', { class: 'sub', text: 'Nothing counted yet today.' })); }
      top.forEach(function (u) {
        topBox.appendChild(h('div', { class: 'ide-top-row', style: 'display:flex;justify-content:space-between;gap:8px;font-size:.9rem' },
          h('span', { text: u.name || u.uid || u.key }), h('span', { class: 'mono', text: ideMB(u.total) })));
      });
      topBox.appendChild(h('p', { class: 'sub', text: 'Full list in Usage.' }));
    }
    function num(v) { v = parseFloat(v); return isNaN(v) ? 0 : v; }
    function clean(s) {
      return { colorOfTheDay: !!s.colorOfTheDay, accentColor: s.accentColor || '', disabledColours: (s.disabledColours || []).slice().sort(), announcement: { text: s.announcement.text, level: s.announcement.level || 'info' },
        maintenance: { enabled: !!s.maintenance.enabled, message: s.maintenance.message || '' },
        disabledLanguages: (s.disabledLanguages || []).slice().sort(),
        dbCapacityMB: s.dbCapacityMB || 0,
        maxEditorFileMB: s.maxEditorFileMB || 0,
        snapshotIntervalMinutes: s.snapshotIntervalMinutes || 0,
        previewLinkGuestMinutes: s.previewLinkGuestMinutes || 0,
        previewLinkUserMinutes: s.previewLinkUserMinutes || 0,
        previewBaseURL: s.previewBaseURL || '',
        shareLinkGuestMinutes: s.shareLinkGuestMinutes || 0,
        shareLinkUserMinutes: s.shareLinkUserMinutes || 0,
        sessionCleanupHours: s.sessionCleanupHours || 0,
        snippetMaxDays: s.snippetMaxDays || 0,
        previewOff: !!s.previewOff, practiceWorkspaceOff: !!s.practiceWorkspaceOff,
        previewGuestsOff: !!s.previewGuestsOff, previewAgentOff: !!s.previewAgentOff, canvasOff: !!s.canvasOff,
        egressOn: !!s.egressOn,
        ideUsageOff: !!s.ideUsageOff, ideAdminsCounted: !!s.ideAdminsCounted, idePartsOff: (s.idePartsOff || []).slice().sort(),
        ideTermMB: s.ideTermMB || 0, ideTermWindowSecs: s.ideTermWindowSecs || 0, ideTermGraceSecs: s.ideTermGraceSecs || 0, ideTermPauseOnly: !!s.ideTermPauseOnly,
        ideGuestMB: s.ideGuestMB || 0, ideUserMB: s.ideUserMB || 0, ideWarnPct: s.ideWarnPct || 0, ideReserveMB: s.ideReserveMB || 0, ideSmallKB: s.ideSmallKB || 0,
        ideResetMinutes: s.ideResetMinutes || 0,
        genieEventsOff: !!s.genieEventsOff, genieEventDays: s.genieEventDays || 0,
        genieHealthErrPct: s.genieHealthErrPct || 0, genieHealthSlowMs: s.genieHealthSlowMs || 0,
        genie: { disabled: !!s.genie.disabled, guestPerMinute: s.genie.guestPerMinute || 0, userPerMinute: s.genie.userPerMinute || 0,
          disabledModels: (s.genie.disabledModels || []).slice().sort(), customModels: (s.genie.customModels || []).map(cleanCustom),
          featuredModels: (s.genie.featuredModels || []).slice(),
          defaultModel: s.genie.defaultModel || '', agentModel: s.genie.agentModel || '', agentEffort: s.genie.agentEffort || '',
          agentModels: s.genie.agentModels ? s.genie.agentModels.slice() : null, guestBlockedModels: (s.genie.guestBlockedModels || []).slice().sort(),
          contextEditorChars: s.genie.contextEditorChars || 0, contextTerminalChars: s.genie.contextTerminalChars || 0,
          contextTerminalLines: s.genie.contextTerminalLines || 0, historyMessages: s.genie.historyMessages || 0,
          contextAttachItems: s.genie.contextAttachItems || 0, contextAttachChars: s.genie.contextAttachChars || 0, contextAttachTotal: s.genie.contextAttachTotal || 0,
          openRouterTimeoutSec: s.genie.openRouterTimeoutSec || 0,
          answerCaps: capsOf(models.map(function (m) { return [m.id, (s.genie.answerCaps || {})[m.id] || 0]; })),
          openRouterHosts: (s.genie.openRouterHosts || []).slice(),
          modelPrices: pricesClean(s.genie.modelPrices), showCostToUsers: !!s.genie.showCostToUsers,
          agentNativeTools: !!s.genie.agentNativeTools, streamAnswers: !!s.genie.streamAnswers,
          nativeToolModels: toolModelsOf(s.genie.nativeToolModels),
          chatHistoryOff: !!s.genie.chatHistoryOff, chatHistoryNoGuests: !!s.genie.chatHistoryNoGuests,
          summaryOff: !!s.genie.summaryOff, summaryTokens: s.genie.summaryTokens || 0,
          chatHistoryChats: s.genie.chatHistoryChats || 0, chatHistoryTabs: s.genie.chatHistoryTabs || 0, chatHistoryKB: s.genie.chatHistoryKB || 0, chatHistoryDays: s.genie.chatHistoryDays || 0,
          meterOff: !!s.genie.meterOff, creditsOn: !!s.genie.creditsOn, planName: s.genie.planName || '',
          planCredits: s.genie.planCredits || 0, planBudget: s.genie.planBudget || 0, creditWeights: pricesClean(s.genie.creditWeights),
          agentDisabled: !!s.genie.agentDisabled, agentOnPractice: !!s.genie.agentOnPractice,
          agentAutoOff: !!s.genie.agentAutoOff, agentRiskyOff: !!s.genie.agentRiskyOff, agentShellOff: !!s.genie.agentShellOff,
          extensionsOff: !!s.genie.extensionsOff, pluginsFromLinks: !!s.genie.pluginsFromLinks, sitePlugins: (s.genie.sitePlugins || []).slice().sort(),
          connectionsOff: !!s.genie.connectionsOff, connectionsNoApproval: !!s.genie.connectionsNoApproval, connectionHosts: (s.genie.connectionHosts || []).slice(),
          agentTasksPerHour: s.genie.agentTasksPerHour || 0, agentMaxSteps: s.genie.agentMaxSteps || 0,
          escalationModels: (s.genie.escalationModels || []).slice(),
          autoOn: !!s.genie.autoOn, autoSmall: s.genie.autoSmall || '', autoStrong: s.genie.autoStrong || '', autoDefault: s.genie.autoDefault || 'new' } };
    }
    function isDirty() { return !!form && !!form.ready && JSON.stringify(snapshot()) !== JSON.stringify(clean(saved)); }

    // ---- the sections (admin-sections.js says which card is where) ------------------------------------------------------
    //
    // The form is built whole, as before: snapshot() and Save read every control, and the controls of a section that is not
    // shown are in the page's memory, not in the page. Only the open section is in the page.
    var late = {}; // draw functions of the boxes that are filled when their data arrives: {keys, admins}
    var ideState = null; // today's IDE data usage of all users (admin/ide-usage)
    var layout = null; // {nav, main, sections, show(), refresh()} of the form that is drawn
    var SECTION_KEY = 'admin-site-section';

    // the controls of the settings: not those of the Integrations card, which saves its own records, one server at a time
    function controlsOf(node) { return Array.prototype.slice.call(node.querySelectorAll('input, select, textarea')).filter(function (el) { return !el.closest('[data-card="Integrations"]') && (!el.closest('.colour-picker') || !!el.closest('.switch')); }); }
    function valueOf(el) { return el.type === 'checkbox' || el.type === 'radio' ? (el.checked ? '1' : '0') : String(el.value); }
    // a setting that has a value of its own: a number or a text that is not empty, a list with something chosen
    function isSetControl(el) {
      if (el.type === 'checkbox' || el.type === 'radio' || el.type === 'button') return false;
      return String(el.value) !== '';
    }

    function sectioned(out, bar) {
      var AS = window.AdminSections;
      var intro = out.querySelector('#settings-store');
      var nodes = {}; // card title -> its node
      Array.prototype.forEach.call(out.children, function (k) {
        if (k === bar || k === intro) return;
        var title = k.getAttribute('data-card');
        if (!title) { var hd = k.querySelector('h2'); title = hd ? hd.textContent : ''; }
        if (title) { k.setAttribute('data-card', title); nodes[title] = k; }
      });
      var sections = {};
      AS.SECTIONS.forEach(function (sec) {
        var node = h('div', { class: 'site-section', 'data-section': sec.id });
        var info = { def: sec, node: node, tabs: {}, tabBar: null, init: {}, initEls: [], active: sec.tabs ? sec.tabs[0].id : '' };
        if (sec.tabs) {
          info.tabBar = h('div', { class: 'site-tabs', role: 'tablist', 'aria-label': sec.title });
          sec.tabs.forEach(function (t) {
            var panel = h('div', { class: 'site-tabpanel', role: 'tabpanel', 'data-tab': t.id });
            t.cards.forEach(function (c) { if (nodes[c]) panel.appendChild(nodes[c]); });
            info.tabs[t.id] = panel;
            info.tabBar.appendChild(h('button', { type: 'button', class: 'site-tab', role: 'tab', 'data-tab': t.id, text: t.title,
              onclick: function () { layout.openTab(sec.id, t.id); } }));
          });
          node.appendChild(info.tabBar);
          sec.tabs.forEach(function (t) { node.appendChild(info.tabs[t.id]); });
        } else {
          sec.cards.forEach(function (c) { if (nodes[c]) node.appendChild(nodes[c]); });
        }
        sections[sec.id] = info;
      });

      // the values the controls have when the form is drawn, to count what was changed in a section
      var cid = 0;
      AS.SECTIONS.forEach(function (sec) {
        var info = sections[sec.id];
        controlsOf(info.node).forEach(function (el) { el.__cid = ++cid; info.init[el.__cid] = valueOf(el); info.initEls.push(el); });
      });

      var main = h('div', { class: 'site-main' });
      var search = h('input', { type: 'search', class: 'field site-search', id: 'site-search', placeholder: 'Search settings', 'aria-label': 'Search settings',
        autocomplete: 'off', role: 'combobox', 'aria-expanded': 'false', 'aria-controls': 'site-results' });
      var results = h('div', { class: 'site-results', id: 'site-results', role: 'listbox', hidden: true });
      var nav = h('nav', { class: 'site-nav', 'aria-label': 'Site settings sections' },
        h('div', { class: 'site-find' }, search, h('span', { class: 'site-key', text: '/', 'aria-hidden': 'true' }), results));
      AS.GROUPS.forEach(function (g) {
        nav.appendChild(h('div', { class: 'site-group', text: g.title }));
        AS.SECTIONS.filter(function (sec) { return sec.group === g.id; }).forEach(function (sec) {
          var a = h('a', { href: AS.hashFor(sec.id), class: 'site-link', 'data-section': sec.id },
            h('span', { class: 'site-name', text: sec.title }),
            h('span', { class: 'site-set', 'data-set': sec.id, hidden: true }),
            h('span', { class: 'site-dot', 'data-dot': sec.id, hidden: true, title: 'Unsaved changes', 'aria-label': 'Unsaved changes' }));
          nav.appendChild(a);
        });
      });

      var current = '';
      var onlySet = false;

      function currentSection() {
        var hs = AS.parseHash(location.hash);
        if (hs.view === 'site' && hs.section) return hs;
        var last = '';
        try { last = localStorage.getItem(SECTION_KEY) || ''; } catch (e) { /* storage off */ }
        return { view: 'site', section: AS.sectionById(last) ? last : AS.DEFAULT_SECTION, anchor: '' };
      }

      // the settings of the whole form, for the search: one entry for each row and each card
      function indexEntries() {
        var out2 = [];
        AS.SECTIONS.forEach(function (sec) {
          var info = sections[sec.id];
          var seen = {};
          Array.prototype.forEach.call(info.node.querySelectorAll('.card'), function (cardNode) {
            var title = cardNode.getAttribute('data-card') || '';
            var panel = cardNode.closest('.site-tabpanel');
            var tab = panel ? panel.getAttribute('data-tab') : '';
            var cardAnchor = AS.slug(title);
            out2.push({ section: sec.id, sectionTitle: sec.title, tab: tab, card: title, label: title, help: (cardNode.querySelector('.sub') || {}).textContent || '', anchor: cardAnchor });
            Array.prototype.forEach.call(cardNode.querySelectorAll('.form-row'), function (r) {
              var b = r.querySelector('.what b');
              var label = b ? b.textContent : '';
              var sp = r.querySelector('.what span');
              var ctl = r.querySelector('input[id], select[id], textarea[id]');
              var anchor = ctl ? AS.slug(ctl.id.replace(/^f-/, '')) : AS.slug(label);
              if (!anchor) return;
              while (seen[anchor]) anchor += '-2';
              seen[anchor] = true;
              r.setAttribute('data-anchor', anchor);
              out2.push({ section: sec.id, sectionTitle: sec.title, tab: tab, card: title, label: label, help: sp ? sp.textContent : '', anchor: anchor });
            });
            cardNode.setAttribute('data-anchor', cardAnchor);
          });
        });
        return out2;
      }
      var entries = null;

      function ringRow(el) {
        el.classList.remove('site-ring');
        void el.offsetWidth;
        el.classList.add('site-ring');
        window.setTimeout(function () { el.classList.remove('site-ring'); }, 1700);
      }

      function drawSection(id, anchor) {
        var info = sections[id] || sections[AS.DEFAULT_SECTION];
        id = info.def.id;
        current = id;
        if (layout) layout.currentId = id;
        main.textContent = '';
        main.classList.toggle('only-set', onlySet);
        main.appendChild(h('div', { class: 'site-head' },
          h('div', null, h('h2', { class: 'site-title', text: (AS.GROUPS.filter(function (g) { return g.id === info.def.group; })[0] || {}).title + ' · ' + info.def.title }),
            h('p', { class: 'sub', text: info.def.blurb })),
          h('label', { class: 'site-only', title: 'Hide the settings that use the built-in value' },
            h('input', { type: 'checkbox', checked: onlySet, 'aria-label': 'Show only the settings that have a value of their own',
              onchange: function (e) { onlySet = e.target.checked; main.classList.toggle('only-set', onlySet); refreshMarks(); } }),
            h('span', { text: 'Only settings with a value' }))));
        main.appendChild(info.node);
        if (anchor) {
          if (!entries) entries = indexEntries();
          var hit = entries.filter(function (e) { return e.section === id && e.anchor === anchor; })[0];
          if (hit && hit.tab && info.def.tabs) openTab(id, hit.tab, true);
          var target = info.node.querySelector('[data-anchor="' + anchor + '"]');
          if (target) {
            // when the form is in the page (a first drawing is attached just after this returns)
            window.setTimeout(function () {
              target.scrollIntoView({ block: 'center' });
              ringRow(target);
              var focusable = target.querySelector('input, select, textarea');
              if (focusable) focusable.focus({ preventScroll: true });
            }, 0);
          }
        } else {
          window.scrollTo(0, 0);
        }
        if (info.def.tabs) openTab(id, info.active, true);
        if (id === 'genie-integrations' && late.integ) late.integ();
        try { localStorage.setItem(SECTION_KEY, id); } catch (e) { /* storage off */ }
        refreshMarks();
        document.title = info.def.title + ' · Site settings · OpenREPL Admin';
      }

      function openTab(id, tab, quiet) {
        var info = sections[id];
        if (!info || !info.def.tabs) return;
        info.active = tab;
        info.def.tabs.forEach(function (t) {
          info.tabs[t.id].hidden = t.id !== tab;
          var btn = info.tabBar.querySelector('[data-tab="' + t.id + '"]');
          btn.setAttribute('aria-selected', t.id === tab ? 'true' : 'false');
          btn.classList.toggle('on', t.id === tab);
        });
        if (!quiet) refreshMarks();
      }

      // the marks of the list: a dot for a section with an unsaved change, and how many settings have a value of their own
      function counts() {
        var c = {};
        AS.SECTIONS.forEach(function (sec) {
          var info = sections[sec.id];
          var now = {};
          var n = 0;
          controlsOf(info.node).forEach(function (el) { if (el.__cid) now[el.__cid] = valueOf(el); else now['new' + (n++)] = valueOf(el); });
          info.initEls.forEach(function (el) { if (!info.node.contains(el)) now['gone' + el.__cid] = ''; });
          c[sec.id] = AS.changeCount(info.init, now);
        });
        return c;
      }

      function refreshMarks() {
        var c = counts();
        AS.SECTIONS.forEach(function (sec) {
          var info = sections[sec.id];
          var dot = nav.querySelector('[data-dot="' + sec.id + '"]');
          var set = nav.querySelector('[data-set="' + sec.id + '"]');
          if (dot) dot.hidden = !c[sec.id];
          var nSet = 0;
          Array.prototype.forEach.call(info.node.querySelectorAll('.form-row'), function (r) {
            var any = controlsOf(r).some(isSetControl);
            r.classList.toggle('is-set', any);
            if (any) nSet++;
          });
          Array.prototype.forEach.call(info.node.querySelectorAll('.card'), function (cardNode) {
            cardNode.classList.toggle('has-set', !!cardNode.querySelector('.form-row.is-set'));
          });
          if (set) {
            set.hidden = !nSet;
            set.textContent = String(nSet);
            set.title = nSet + (nSet === 1 ? ' setting has' : ' settings have') + ' a value of their own; the others use the built-in value';
          }
          var link = nav.querySelector('[data-section="' + sec.id + '"].site-link');
          if (link) { if (sec.id === current) link.setAttribute('aria-current', 'page'); else link.removeAttribute('aria-current'); }
        });
        layout.counts = c;
      }

      // ---- the search
      var picked = -1;
      var shown = [];
      function closeResults() { results.hidden = true; search.setAttribute('aria-expanded', 'false'); picked = -1; shown = []; }
      function drawResults() {
        if (!entries) entries = indexEntries();
        shown = AS.search(entries, search.value, 10);
        results.textContent = '';
        if (!search.value.trim()) { closeResults(); return; }
        if (!shown.length) results.appendChild(h('div', { class: 'site-none', text: 'No setting has that in its name.' }));
        shown.forEach(function (e, i) {
          results.appendChild(h('button', { type: 'button', class: 'site-hit' + (i === picked ? ' on' : ''), role: 'option', 'aria-selected': i === picked ? 'true' : 'false',
            onmousedown: function (ev) { ev.preventDefault(); },
            onclick: function () { goTo(e); } },
            h('span', { class: 'site-path', text: (AS.GROUPS.filter(function (g) { return g.id === (AS.sectionById(e.section) || {}).group; })[0] || {}).title + ' · ' + e.sectionTitle }),
            h('b', { text: e.label })));
        });
        results.hidden = false;
        search.setAttribute('aria-expanded', 'true');
      }
      function goTo(e) {
        closeResults();
        search.value = '';
        entries = null; // the rows may have changed (a model added): look again next time
        var addr = AS.hashFor(e.section, e.anchor);
        if (location.hash === addr) onHash(); else location.hash = addr;
      }
      search.addEventListener('input', function () { picked = -1; drawResults(); });
      search.addEventListener('keydown', function (ev) {
        if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
          if (!shown.length) return;
          ev.preventDefault();
          picked = (picked + (ev.key === 'ArrowDown' ? 1 : -1) + shown.length) % shown.length;
          drawResults();
        } else if (ev.key === 'Enter') {
          ev.preventDefault();
          var e = shown[picked >= 0 ? picked : 0];
          if (e) goTo(e);
        } else if (ev.key === 'Escape') {
          if (search.value) { search.value = ''; closeResults(); ev.stopPropagation(); } else search.blur();
        }
      });
      search.addEventListener('blur', function () { window.setTimeout(closeResults, 120); });

      layout = { nav: nav, main: main, sections: sections, counts: {}, openTab: function (id, tab) { openTab(id, tab); }, refresh: refreshMarks, search: search,
        show: function (hs) { drawSection(hs.section, hs.anchor); } };

      var wrap = h('div', { class: 'site-layout' }, nav, h('div', { class: 'site-body' }, intro, main, bar));
      var first = currentSection();
      if (location.hash.replace(/^#/, '').split('/')[0] === 'site' && !location.hash.split('/')[1]) history.replaceState(null, '', AS.hashFor(first.section));
      drawSection(first.section, first.anchor);
      return wrap;
    }

    // a change of the address inside the page: another section, or a setting of one
    function onHash() {
      if (!layout) return;
      var hs = AS.parseHash(location.hash);
      if (hs.view !== 'site') return;
      layout.show(hs.section ? hs : { section: layout.currentId || AS.DEFAULT_SECTION, anchor: '' });
    }
    // "/" goes to the search box, as in many tools, unless something is being typed
    function onSlash(ev) {
      if (ev.key !== '/' || ev.ctrlKey || ev.metaKey || ev.altKey || !layout) return;
      var t = ev.target;
      if (t && (/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) || t.isContentEditable)) return;
      ev.preventDefault();
      layout.search.focus();
    }
    var AS = window.AdminSections;
    window.addEventListener('hashchange', onHash);
    document.addEventListener('keydown', onSlash);

    function build(settings, keysInfo, adminsInfo) {
      function toggle(id, checked, label) {
        var input = h('input', { type: 'checkbox', id: id, checked: checked, 'aria-label': label, onchange: update });
        return { input: input, node: h('label', { class: 'switch' }, input, h('i')) };
      }

      saved = settings;
      languages = settings.languages || [];
      models = settings.models || [];
      pluginChoices = settings.pluginCatalogue || [];
      form = { langs: {}, models: {}, caps: {} };
      layout = null; // the form of an earlier drawing is gone
      var defaults = settings.defaults || {};
      var colour = toggle('f-colour', settings.colorOfTheDay, 'Colour of the day');
      // Apply and Reset save the whole form (the server takes the settings as one document), with their own message
      form.colours = colourCard(settings, {
        switchNode: colour.node,
        change: function () { update(); },
        setDaily: function (on) { colour.input.checked = on; update(); },
        save: function (msg) { doSave(msg); }
      });
      // the picker's state as a value of a control, so that the section marks show that it changed
      form.coloursState = h('input', { type: 'hidden', oninput: update });
      form.coloursState.value = JSON.stringify([form.colours.accent(), form.colours.skipped()]);
      var maint = toggle('f-maint', settings.maintenance.enabled, 'Maintenance mode');
      var genie = toggle('f-genie', !settings.genie.disabled, 'Genie available');
      // how much room MongoDB has, for the live map (an Atlas free cluster has 512 MB)
      form.dbCap = h('input', { type: 'number', class: 'field', id: 'f-db-cap', min: '10', max: '100000000', step: '1', placeholder: 'not set', 'aria-label': 'Database capacity in megabytes', oninput: update });
      form.dbCap.value = settings.dbCapacityMB || '';
      form.dbCap.style.maxWidth = '9rem';
      // the largest file the editor opens (16 is the most: the editor is shared through Firebase, which takes no more in a write)
      form.maxFile = h('input', { type: 'number', class: 'field', id: 'f-max-file', min: '1', max: '16', step: '1', placeholder: '16', 'aria-label': 'Largest file the editor opens, in megabytes', oninput: update });
      form.maxFile.value = settings.maxEditorFileMB || '';
      form.maxFile.style.maxWidth = '9rem';
      form.snapEvery = h('input', { type: 'number', class: 'field', id: 'f-snap-every', min: '1', max: '60', step: '1', placeholder: '5', 'aria-label': 'Minutes between workspace snapshots', oninput: update });
      form.snapEvery.value = settings.snapshotIntervalMinutes || '';
      form.snapEvery.style.maxWidth = '9rem';
      form.previewLifeGuest = h('input', { type: 'number', class: 'field', id: 'f-preview-life-guest', min: '10', max: '1440', step: '1', placeholder: '60', 'aria-label': 'Minutes a preview link works for guests', oninput: update });
      form.previewLifeGuest.value = settings.previewLinkGuestMinutes || '';
      form.previewLifeGuest.style.maxWidth = '9rem';
      form.previewLifeUser = h('input', { type: 'number', class: 'field', id: 'f-preview-life-user', min: '10', max: '1440', step: '1', placeholder: '180', 'aria-label': 'Minutes a preview link works for signed-in users', oninput: update });
      form.previewLifeUser.value = settings.previewLinkUserMinutes || '';
      form.previewLifeUser.style.maxWidth = '9rem';
      form.previewBaseURL = h('input', { type: 'text', class: 'field', id: 'f-preview-base-url', placeholder: 'preview.openrepl.com', 'aria-label': 'Preview base URL', oninput: update });
      form.previewBaseURL.value = settings.previewBaseURL || '';
      form.shareLifeGuest = h('input', { type: 'number', class: 'field', id: 'f-share-life-guest', min: '10', max: '10080', step: '1', placeholder: '60', 'aria-label': 'Most minutes a share link works for guests', oninput: update });
      form.shareLifeGuest.value = settings.shareLinkGuestMinutes || '';
      form.shareLifeGuest.style.maxWidth = '9rem';
      form.shareLifeUser = h('input', { type: 'number', class: 'field', id: 'f-share-life-user', min: '10', max: '10080', step: '1', placeholder: '10080', 'aria-label': 'Most minutes a share link works for signed-in users', oninput: update });
      form.shareLifeUser.value = settings.shareLinkUserMinutes || '';
      form.shareLifeUser.style.maxWidth = '9rem';
      form.sessionCleanup = h('input', { type: 'number', class: 'field', id: 'f-session-cleanup', min: '1', max: '720', step: '1', placeholder: '24', 'aria-label': 'Hours between two cleanups of shared sessions', oninput: update });
      form.sessionCleanup.value = settings.sessionCleanupHours || '';
      form.sessionCleanup.style.maxWidth = '9rem';
      form.snippetMax = h('input', { type: 'number', class: 'field', id: 'f-snippet-max', min: '1', max: '365', step: '1', placeholder: '30', 'aria-label': 'Most days a code link works', oninput: update });
      form.snippetMax.value = settings.snippetMaxDays || '';
      form.snippetMax.style.maxWidth = '9rem';
      form.colour = colour.input; form.maint = maint.input; form.genie = genie.input;
      // the Preview pane (pages written by visitors, shown on this site's domain): off for all, or for guests only
      var previewOn = toggle('f-preview', !settings.previewOff, 'Preview available');
      var practiceWs = toggle('f-practice-ws', !settings.practiceWorkspaceOff, 'Practice saved in the workspace');
      form.practiceWs = practiceWs.input;
      var previewGuests = toggle('f-preview-guests', !settings.previewGuestsOff, 'Preview for guests');
      var previewAgent = toggle('f-preview-agent', !settings.previewAgentOff, 'Genie may act in the Preview');
      var canvasOn = toggle('f-canvas', !settings.canvasOff, 'Genie may draw on the canvas');
      var egressOn = toggle('f-egress', !!settings.egressOn, 'Internet for signed-in users');
      form.egress = egressOn.input;
      // the IDE data usage (LLD 20): what the IDE sends to a user is counted, a terminal that is too fast is stopped, a user at the limit loses the heavy parts
      var ideOn = toggle('f-ide-on', !settings.ideUsageOff, 'Metering');
      form.ideOn = ideOn.input;
      var ideAdmins = toggle('f-ide-admins', !settings.ideAdminsCounted, 'Admins are exempt');
      form.ideAdmins = ideAdmins.input;
      form.ideParts = {};
      var ideOff = settings.idePartsOff || [];
      IDE_PARTS.forEach(function (p) { form.ideParts[p[0]] = h('input', { type: 'checkbox', id: 'f-ide-part-' + p[0], checked: ideOff.indexOf(p[0]) < 0, 'aria-label': p[1] + ' is counted', onchange: update }); });
      var ideNum = function (id, key, min, max, ph, label) {
        var el = h('input', { type: 'number', class: 'field', id: id, min: String(min), max: String(max), step: '1', placeholder: String(ph), 'aria-label': label, oninput: update });
        el.value = settings[key] || ''; el.style.maxWidth = '9rem'; return el;
      };
      form.ideTermMB = ideNum('f-ide-term-mb', 'ideTermMB', 1, 1024, 20, 'MB a terminal may send in one window');
      form.ideTermWindow = ideNum('f-ide-term-window', 'ideTermWindowSecs', 1, 600, 10, 'Window length in seconds');
      form.ideTermGrace = ideNum('f-ide-term-grace', 'ideTermGraceSecs', 1, 600, 5, 'Seconds over the cap before the run is ended');
      form.ideTermAction = h('select', { class: 'field', id: 'f-ide-term-action', 'aria-label': 'When a terminal is over the cap', onchange: update },
        h('option', { value: 'end', text: 'Pause, then end the run' }), h('option', { value: 'pause', text: 'Pause only' }));
      form.ideTermAction.value = settings.ideTermPauseOnly ? 'pause' : 'end';
      form.ideGuestMB = ideNum('f-ide-guest-mb', 'ideGuestMB', 1, 1048576, 50, 'MB a guest may use in a session');
      form.ideUserMB = ideNum('f-ide-user-mb', 'ideUserMB', 1, 1048576, 200, 'MB a signed-in user may use in a day');
      form.ideWarnPct = ideNum('f-ide-warn-pct', 'ideWarnPct', 1, 99, 80, 'Warn at, in percent');
      form.ideReserveMB = ideNum('f-ide-reserve-mb', 'ideReserveMB', 1, 1024, 2, 'MB kept open at the limit');
      form.ideSmallKB = ideNum('f-ide-small-kb', 'ideSmallKB', 1, 16384, 256, 'KB of the small files that still open at the limit');
      form.ideAtLimit = h('select', { class: 'field', id: 'f-ide-at-limit', 'aria-label': 'At the limit' }, h('option', { value: 'stop', text: 'Stop heavy parts, keep reserve' }));
      form.ideAtLimit.disabled = true;
      form.ideReset = h('input', { type: 'time', class: 'field', id: 'f-ide-reset', 'aria-label': 'The day resets at (UTC)', oninput: update });
      form.ideReset.value = ideTimeText(settings.ideResetMinutes || 0);
      form.ideReset.style.maxWidth = '9rem';
      var ideLocal = h('span', { class: 'sub' });
      var showIdeLocal = function () { ideLocal.textContent = ' UTC, in your time ' + ideLocalTime(ideResetNow()); };
      form.ideReset.addEventListener('input', showIdeLocal); showIdeLocal();
      // the Free plan row follows the numbers above, as they are typed
      var idePlanText = h('span', { class: 'mono' });
      var showIdePlan = function () {
        var g = int(form.ideGuestMB.value) || 50, u = int(form.ideUserMB.value) || 200;
        idePlanText.textContent = 'guest ' + g + ' MB per session, signed in ' + u + ' MB a day';
      };
      form.ideGuestMB.addEventListener('input', showIdePlan); form.ideUserMB.addEventListener('input', showIdePlan); showIdePlan();
      var ideToday = h('div', { 'data-ide': 'today' }, h('p', { class: 'loading', text: 'Loading…' }));
      var ideTop = h('div', { 'data-ide': 'top' }, h('p', { class: 'loading', text: 'Loading…' }));
      late.ide = function (info) { drawIDEToday(ideToday, ideTop, info); };
      if (ideState) late.ide(ideState);
      form.previewOn = previewOn.input; form.previewGuests = previewGuests.input; form.previewAgent = previewAgent.input; form.canvasOn = canvasOn.input;
      // agent mode: on for signed-in users unless switched off here, and off on the practice page unless allowed
      var agentOn = toggle('f-agent', !settings.genie.agentDisabled, 'Agent mode');
      var agentPractice = toggle('f-agent-practice', !!settings.genie.agentOnPractice, 'Agent mode on the practice page');
      var agentAuto = toggle('f-agent-auto', !settings.genie.agentAutoOff, 'Allow everything for a session');
      var agentRisky = toggle('f-agent-risky', !settings.genie.agentRiskyOff, 'Allow riskier things for a while');
      // skills and plugins (server/genie_ext.go)
      var extOn = toggle('f-ext-on', !settings.genie.extensionsOff, 'Skills and plugins');
      var plgLinks = toggle('f-plg-links', !!settings.genie.pluginsFromLinks, 'Plugins from a link');
      form.extOn = extOn.input; form.plgLinks = plgLinks.input;
      // connections to other services (MCP, server/genie_conn.go)
      var connOn = toggle('f-conn-on', !settings.genie.connectionsOff, 'Connections to other services');
      var connNoApproval = toggle('f-conn-noapproval', !!settings.genie.connectionsNoApproval, 'Allow connections without approval');
      form.connOn = connOn.input; form.connNoApproval = connNoApproval.input;
      form.connHosts = h('textarea', { class: 'field', id: 'f-conn-hosts', rows: '3', placeholder: 'api.githubcopilot.com\nmcp.example.com', 'aria-label': 'Only these servers', oninput: update });
      form.connHosts.value = (settings.genie.connectionHosts || []).join('\n');
      form.connHosts.style.width = '100%';
      form.sitePlug = {};
      var sitePicked = settings.genie.sitePlugins || [];
      var sitePlugBoxes = h('div', { class: 'toolbar', style: 'flex-wrap:wrap;gap:6px 16px' }, (settings.pluginCatalogue || []).map(function (p) {
        var box = h('input', { type: 'checkbox', id: 'f-site-plugin-' + p.id, checked: sitePicked.indexOf(p.id) >= 0, 'aria-label': p.title + ' is on for everyone', onchange: update });
        form.sitePlug[p.id] = box;
        return h('label', { class: 'sub', style: 'display:flex;gap:6px;align-items:center' }, box, h('span', { text: p.title }));
      }));
      var agentShell = toggle('f-agent-shell', !settings.genie.agentShellOff, 'Agent: run commands');
      form.agentShell = agentShell.input;
      var genieEvents = toggle('f-genie-events', !settings.genieEventsOff, 'Genie log');
      form.genieEvents = genieEvents.input;
      form.genieDays = h('input', { type: 'number', class: 'field', id: 'f-genie-days', min: '1', max: '365', step: '1', placeholder: '30', 'aria-label': 'Days the Genie log is kept', oninput: update });
      form.genieDays.value = settings.genieEventDays || '';
      form.genieDays.style.maxWidth = '9rem';
      form.healthErr = h('input', { type: 'number', class: 'field', id: 'f-health-err', min: '1', max: '100', step: '1', placeholder: '20', 'aria-label': 'Percent of failed calls that raises an alert', oninput: update });
      form.healthErr.value = settings.genieHealthErrPct || '';
      form.healthErr.style.maxWidth = '9rem';
      form.healthSlow = h('input', { type: 'number', class: 'field', id: 'f-health-slow', min: '500', max: '120000', step: '100', placeholder: '8000', 'aria-label': 'Milliseconds to start answering that raise an alert', oninput: update });
      form.healthSlow.value = settings.genieHealthSlowMs || '';
      form.healthSlow.style.maxWidth = '9rem';
      form.escalation = h('input', { type: 'text', class: 'field', id: 'f-escalation', placeholder: 'model ids, comma separated', 'aria-label': 'Models for a stuck task', oninput: update });
      form.escalation.value = (settings.genie.escalationModels || []).join(', ');
      // Auto model choice: the two models the page picks between, and who starts on it
      var autoOn = toggle('f-auto-on', !!settings.genie.autoOn, 'Auto model choice');
      form.autoOn = autoOn.input;
      function autoSelect(id, label, value) {
        var sel = h('select', { class: 'field', id: id, 'aria-label': label, onchange: update }, h('option', { value: '', text: 'Choose a model' }));
        models.forEach(function (m) { sel.appendChild(h('option', { value: m.id, text: m.name })); });
        sel.value = value || '';
        return sel;
      }
      form.autoSmall = autoSelect('f-auto-small', 'Auto: small model', settings.genie.autoSmall);
      form.autoStrong = autoSelect('f-auto-strong', 'Auto: strong model', settings.genie.autoStrong);
      form.autoDefault = h('select', { class: 'field', id: 'f-auto-default', 'aria-label': 'Auto is the default for', onchange: update },
        [['new', 'New users only'], ['all', 'Everybody who has not chosen a model'], ['none', 'Nobody (they choose Auto themselves)']].map(function (o) { return h('option', { value: o[0], text: o[1] }); }));
      form.autoDefault.value = settings.genie.autoDefault || 'new';
      form.agentOn = agentOn.input; form.agentPractice = agentPractice.input; form.agentAuto = agentAuto.input; form.agentRisky = agentRisky.input;
      // what the models cost: dollars for a million tokens sent and written, by model
      var showCost = toggle('f-show-cost', !!settings.genie.showCostToUsers, 'Show the cost to users');
      form.showCost = showCost.input;
      form.price = {};
      var priceInputs = models.map(function (m) {
        var cur = (settings.genie.modelPrices || {})[m.id] || {};
        function box(which, label) {
          var input = h('input', { type: 'number', class: 'field', id: 'f-price-' + which + '-' + m.id.replace(/[^a-z0-9]/gi, '-'), min: '0', max: '10000', step: '0.01',
            placeholder: 'not set', 'aria-label': label + ' for ' + m.name, oninput: update });
          input.value = cur[which] || '';
          input.style.maxWidth = '7rem';
          return input;
        }
        var inBox = box('in', 'Price per million tokens sent'), outBox = box('out', 'Price per million tokens written');
        form.price[m.id] = { in: inBox, out: outBox };
        return { model: m, inBox: inBox, outBox: outBox };
      });

      // the usage record and credits (server/meter.go): the record is on unless switched off, credits limit nothing until switched on
      var chatOn = toggle('f-chat-on', !settings.genie.chatHistoryOff, 'Save chat history in workspaces');
      var chatGuests = toggle('f-chat-guests', !settings.genie.chatHistoryNoGuests, 'Also for guests');
      form.chatOn = chatOn.input; form.chatGuests = chatGuests.input;
      function chatBox(id, label, value, min, max, hint) {
        var input = h('input', { type: 'number', class: 'field', id: id, min: String(min), max: String(max), step: '1', placeholder: hint, 'aria-label': label, oninput: update });
        input.value = value || '';
        input.style.maxWidth = '9rem';
        return input;
      }
      form.chatMax = chatBox('f-chat-max', 'Most chats a workspace keeps', settings.genie.chatHistoryChats, 1, 200, '50');
      form.chatTabs = chatBox('f-chat-tabs', 'Most open chat tabs', settings.genie.chatHistoryTabs, 1, 20, '8');
      form.chatKB = chatBox('f-chat-kb', 'Most size of one chat in KB', settings.genie.chatHistoryKB, 20, 2000, '200');
      var sumOn = toggle('f-sum-on', !settings.genie.summaryOff, 'Summarize long chats');
      form.sumOn = sumOn.input;
      form.sumChars = chatBox('f-sum-tokens', 'Tokens of older turns before a chat is summarized', settings.genie.summaryTokens, 1000, 40000, '6000');
      form.chatDays = chatBox('f-chat-days', 'Days a chat that is not open is kept', settings.genie.chatHistoryDays, 0, 3650, 'for ever');
      var agentTools = toggle('f-agent-tools', !!settings.genie.agentNativeTools, 'Agent: native tool calling');
      var streamOn = toggle('f-stream-on', !!settings.genie.streamAnswers, 'Stream answers');
      form.agentTools = agentTools.input; form.streamOn = streamOn.input;
      form.toolModels = {};
      var toolWanted = toolModelsOf(settings.genie.nativeToolModels);
      var toolBoxes = h('div', { class: 'toolbar', style: 'flex-wrap:wrap;gap:6px 16px' }, models.map(function (m) {
        var box = h('input', { type: 'checkbox', id: 'f-tool-' + m.id.replace(/[^a-z0-9]/gi, '-'), checked: toolWanted.indexOf(m.id) >= 0, 'aria-label': 'Native tool calling for ' + m.name, onchange: update });
        form.toolModels[m.id] = box;
        return h('label', { class: 'sub', style: 'display:flex;gap:6px;align-items:center' }, box, h('span', { text: m.name }));
      }));
      var meterOn = toggle('f-meter-on', !settings.genie.meterOff, 'Record every Genie answer');
      var creditsOn = toggle('f-credits-on', !!settings.genie.creditsOn, 'Use credits for limits');
      form.meterOn = meterOn.input; form.creditsOn = creditsOn.input;
      form.planName = h('input', { type: 'text', class: 'field', id: 'f-plan-name', maxlength: '40', placeholder: 'Free', 'aria-label': 'Name of the default plan', oninput: update });
      form.planName.value = settings.genie.planName || '';
      form.planName.style.maxWidth = '12rem';
      function creditBox(id, label, value, max) {
        var input = h('input', { type: 'number', class: 'field', id: id, min: '0', max: String(max), step: '1', placeholder: 'none', 'aria-label': label, oninput: update });
        input.value = value || '';
        input.style.maxWidth = '9rem';
        return input;
      }
      form.planCredits = creditBox('f-plan-credits', 'Credits of a month in the default plan', settings.genie.planCredits, 100000000);
      form.planBudget = creditBox('f-plan-budget', 'Credits one task may use in the default plan', settings.genie.planBudget, 100000000);
      form.weight = {};
      var weightInputs = models.map(function (m) {
        var cur = (settings.genie.creditWeights || {})[m.id] || {};
        function box(which, label) {
          var input = h('input', { type: 'number', class: 'field', id: 'f-weight-' + which + '-' + m.id.replace(/[^a-z0-9]/gi, '-'), min: '0', max: '100000000', step: '0.01',
            placeholder: 'from the price', 'aria-label': label + ' for ' + m.name, oninput: update });
          input.value = cur[which] || '';
          input.style.maxWidth = '8rem';
          return input;
        }
        var inBox = box('in', 'Credits per thousand tokens sent'), outBox = box('out', 'Credits per thousand tokens written');
        form.weight[m.id] = { in: inBox, out: outBox };
        return { model: m, inBox: inBox, outBox: outBox };
      });

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

      // one switch for each model, a tick for the ones that start in the picker's short list, and the model a visitor starts with
      form.featured = {};
      form.guestBoxes = {};
      var wantedBlocked = settings.genie.guestBlockedModels || [];
      var wantedFeatured = settings.genie.featuredModels || [];
      var modelToggles = models.map(function (m) {
        var on = toggle('f-model-' + m.id.replace(/[^a-z0-9]/gi, '-'), (settings.genie.disabledModels || []).indexOf(m.id) < 0, m.name + ' available');
        form.models[m.id] = on.input;
        form.featured[m.id] = h('input', { type: 'checkbox', checked: wantedFeatured.indexOf(m.id) >= 0, onchange: update, 'aria-label': m.name + ' starts in the short list' });
        form.guestBoxes[m.id] = h('input', { type: 'checkbox', checked: wantedBlocked.indexOf(m.id) < 0, onchange: update, 'aria-label': m.name + ' is available for guest users' });
        return { model: m, node: on.node };
      });
      var defModel = h('select', { class: 'field', id: 'f-defmodel', 'aria-label': 'Chat model', onchange: update },
        h('option', { value: '', text: 'Built in (Luna for visitors)' }));
      models.forEach(function (m) { defModel.appendChild(h('option', { value: m.id, text: m.name })); });
      defModel.value = settings.genie.defaultModel || '';
      form.defModel = defModel;
      var wantedDefault = settings.genie.defaultModel || '';
      // The model Agent mode starts on (and that writes the practice questions): only a model that may be used in Agent mode, which is the
      // list of the next card. Empty is the built-in choice.
      var agentModel = h('select', { class: 'field', id: 'f-agentmodel', 'aria-label': 'Agent model', onchange: update },
        h('option', { value: '', text: 'Built in (the first model that is not small)' }));
      form.agentModel = agentModel;
      var wantedAgent = settings.genie.agentModel || '';
      form.agentEffort = h('select', { class: 'field', id: 'f-agenteffort', 'aria-label': 'Effort for the Agent model', onchange: update },
        h('option', { value: '', text: 'Low (built in)' }), h('option', { value: 'none', text: 'Off' }), h('option', { value: 'low', text: 'Low' }),
        h('option', { value: 'medium', text: 'Medium' }), h('option', { value: 'high', text: 'High' }));
      form.agentEffort.value = settings.genie.agentEffort || '';

      // the models an admin added: one block each, with its own switch
      form.custom = [];
      var builtInIds = models.map(function (m) { return m.id; });
      var keySetFor = function (p) { return models.some(function (m) { return m.provider === p && m.keySet; }); };
      var openRouterKeySet = keySetFor('openrouter'), cloudflareKeySet = keySetFor('cloudflare');

      var AS_ = window.AdminSections;
      var agentBoxes = {}; // model id -> its tick
      var agentInitial = {}; // model id -> ticked when the form was drawn
      var agentSaved = settings.genie.agentModels || null; // the list that was saved; null: none, every model that is on may be used
      var customList = h('div', { class: 'custom-models' });
      function addCustomRow(c) {
        var r = { id: c.id, provider: providerOf(c) };
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
        if (r.provider === '') {
          r.hosts = h('input', { type: 'text', class: 'field', placeholder: 'OpenRouter chooses', 'aria-label': 'Providers allowed for ' + c.id, oninput: update });
          r.hosts.value = (c.hosts || []).join(', ');
        }
        if (r.provider === 'openai') r.reasoning = h('input', { type: 'checkbox', checked: !!c.reasoning, onchange: update, 'aria-label': c.id + ' thinks first and takes an effort' });
        r.featured = h('input', { type: 'checkbox', checked: wantedFeatured.indexOf(c.id) >= 0, onchange: update, 'aria-label': c.id + ' starts in the short list' });
        r.smallSet = c.small === true || c.small === false;
        r.small = h('input', { type: 'checkbox', checked: r.smallSet ? c.small : window.AdminSections.isSmallModel(c.id), onchange: function () { r.smallSet = true; update(); }, 'aria-label': c.id + ' is a small model' });
        r.guests = h('input', { type: 'checkbox', checked: !c.noGuests, onchange: update, 'aria-label': c.id + ' is available for guest users' });
        r.agent = agentTick({ id: c.id, name: c.name || c.id });
        var remove = h('button', { class: 'btn small', type: 'button', text: 'Remove', 'aria-label': 'Remove ' + c.id, onclick: function () {
          form.custom = form.custom.filter(function (x) { return x !== r; });
          if (r.node.parentNode) r.node.parentNode.removeChild(r.node);
          update();
        } });
        var field = function (label, input, hint) { return h('label', { class: 'cm-field' }, h('span', { text: label }), input, hint ? h('small', { text: hint }) : null); };
        var check = function (input, label) { return h('label', { class: 'cm-check' }, input, h('span', { text: label })); };
        r.node = h('div', { class: 'custom-model' },
          h('div', { class: 'cm-head' }, on.node, h('div', { class: 'cm-title' }, r.name, h('span', { class: 'mono', text: PROVIDER_NAMES[r.provider] + ' · ' + c.id })), remove),
          h('div', { class: 'cm-grid' },
            field('Answer tokens', r.answer, 'Most tokens of an answer. Empty: 4000.'),
            field('Room for thinking', r.room, 'Extra tokens for a model that thinks first.'),
            r.hosts ? field('Providers', r.hosts, 'Only these may answer (OpenRouter names, comma separated).') : null),
          h('div', { class: 'cm-checks' }, check(r.free, r.provider === 'cloudflare' ? 'Free plan (visitors are told)' : 'Free tier (visitors are told)'), check(r.json, 'Its host takes JSON mode (Agent mode)'),
            r.reasoning ? check(r.reasoning, 'Thinks first (takes an effort, like Luna)') : null, check(r.featured, 'Starts in the short list'),
            check(r.small, 'Small model'), check(r.agent, 'Agent mode'), check(r.guests, 'Available for guest users')));
        form.custom.push(r);
        customList.appendChild(r.node);
        return r;
      }
      (settings.genie.customModels || []).forEach(addCustomRow);
      var addProvider = h('select', { class: 'field', id: 'f-cm-new-provider', 'aria-label': 'Provider of the new model' },
        h('option', { value: '', text: 'OpenRouter' }), h('option', { value: 'openai', text: 'OpenAI' }), h('option', { value: 'cloudflare', text: 'Cloudflare Workers AI' }));
      var cfList = h('datalist', { id: 'f-cm-cf-list' });
      var cfNote = h('p', { class: 'sub', id: 'f-cm-help', text: ID_HELP[''] });
      var addId = h('input', { type: 'text', class: 'field', id: 'f-cm-new-id', placeholder: 'author/model-name:free', 'aria-label': 'Model id', autocomplete: 'off', list: 'f-cm-cf-list' });
      var cfPrices = {};
      var cfLoaded = false;
      addProvider.addEventListener('change', function () {
        var p = addProvider.value;
        addId.placeholder = p === 'cloudflare' ? '@cf/qwen/qwen3-30b-a3b-fp8' : p === 'openai' ? 'gpt-4.1-mini' : 'author/model-name:free';
        cfNote.textContent = ID_HELP[p];
        // Cloudflare's own list of its models (exact ids and prices), once
        if (p === 'cloudflare' && !cfLoaded) {
          cfLoaded = true;
          api.get('admin/cloudflare-models').then(function (r) {
            (r.models || []).forEach(function (m) {
              cfPrices[m.id] = m;
              cfList.appendChild(h('option', { value: m.id, label: '$' + (m.inputPerM || 0) + ' in / $' + (m.outputPerM || 0) + ' out per million tokens' }));
            });
            if (r.warning) cfNote.textContent = r.warning + ' ' + ID_HELP.cloudflare;
            else if (!r.configured) cfNote.textContent = 'Set the Cloudflare token and account id under API keys to pick from Cloudflare\'s list. ' + ID_HELP.cloudflare;
            else cfNote.textContent = (r.models || []).length + ' models in your account (start typing to choose). ' + ID_HELP.cloudflare;
          }, function () { cfNote.textContent = ID_HELP.cloudflare; });
        }
      });
      var addName = h('input', { type: 'text', class: 'field', id: 'f-cm-new-name', maxlength: '40', placeholder: 'Name visitors see (optional)', 'aria-label': 'Name of the new model', autocomplete: 'off' });
      var addBtn = h('button', { class: 'btn', type: 'button', text: 'Add model', onclick: function () {
        var id = addId.value.trim().toLowerCase();
        var provider = addProvider.value;
        if (!idOk(provider, id)) { toast(ID_HELP[provider], 'error'); return; }
        if (builtInIds.indexOf(id) >= 0 || form.custom.some(function (r) { return r.id === id; })) { toast(id + ' is in the list already.', 'error'); return; }
        if (form.custom.length >= 20) { toast('At most 20 models can be added. Remove one first.', 'error'); return; }
        var free = /:free$/.test(id);
        addCustomRow({ provider: provider, id: id, name: addName.value.trim(), enabled: false, free: free, jsonMode: false, thinkingRoom: free ? 3000 : 0, hosts: [], reasoning: false });
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

      // ---- the models that may be used in Agent mode (the ticks in the rows of the Genie card; agent_models.go on the server)
      var agentNote = h('p', { class: 'callout', hidden: true, text: 'Tick Agent mode on at least one model: Agent mode needs one. Models you added are ticked in their own row (Added models).' });
      var agentSig = '';
      function agentCandidates() {
        var out = models.map(function (m) { return { id: m.id, name: m.name, on: isModelOn(m.id) }; });
        (form.custom || []).forEach(function (r) { if (r.id) out.push({ id: r.id, name: r.name.value.trim() || r.id, on: r.on.checked }); });
        return out;
      }
      function agentTick(c) {
        if (!agentBoxes[c.id]) {
          // until a list is saved, the models that are not small start ticked
          var on = agentSaved ? agentSaved.indexOf(c.id) >= 0 : !AS_.isSmallModel(c.id);
          agentBoxes[c.id] = h('input', { type: 'checkbox', checked: on, 'aria-label': 'Agent mode: ' + c.name, onchange: update });
          agentInitial[c.id] = on;
        }
        return agentBoxes[c.id];
      }
      function agentTicked() {
        return agentCandidates().filter(function (c) { return c.on && agentBoxes[c.id] && agentBoxes[c.id].checked; });
      }
      // what is sent: the list; or null while none was saved and the ticks are as they started (an unrelated save does not start a list)
      function agentListNow() {
        var ids = agentTicked().map(function (c) { return c.id; });
        if (!agentSaved) {
          var initial = agentCandidates().filter(function (c) { return c.on && agentInitial[c.id]; }).map(function (c) { return c.id; });
          if (JSON.stringify(ids) === JSON.stringify(initial)) return null;
        }
        return ids;
      }
      form.agentList = agentListNow;
      function syncAgent() {
        var cands = agentCandidates();
        var sig = cands.map(function (c) { return c.id + '|' + c.name + '|' + (c.on ? 1 : 0); }).join(',');
        if (sig !== agentSig) {
          agentSig = sig;
          cands.forEach(function (c) { agentTick(c); }); // the ticks are in the rows of the models
        }
        cands.forEach(function (c) { agentBoxes[c.id].disabled = !c.on; });
        // the Agent model is one of the ticked ones
        var ticked = agentTicked();
        var keep = agentModel.value || wantedAgent;
        wantedAgent = '';
        while (agentModel.options.length > 1) agentModel.remove(1);
        ticked.forEach(function (c) { agentModel.appendChild(h('option', { value: c.id, text: c.name })); });
        agentModel.value = keep;
        if (agentModel.value !== keep) agentModel.value = '';
        agentNote.hidden = ticked.length > 0;
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
      form.attachItems = numberField('f-attach-items', 'contextAttachItems', 'Items added to one message', defaults.contextAttachItems || 8, limits.contextAttachItems || [1, 20]);
      form.attachChars = numberField('f-attach-chars', 'contextAttachChars', 'Characters of one added item', defaults.contextAttachChars || 8000, limits.contextAttachChars || [500, 50000]);
      form.attachTotal = numberField('f-attach-total', 'contextAttachTotal', 'Characters of all the added items', defaults.contextAttachTotal || 24000, limits.contextAttachTotal || [2000, 100000]);
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
        agentAuto.input.disabled = agentRisky.input.disabled = agentShell.input.disabled = agentPractice.input.disabled = form.agentTasks.disabled = form.agentSteps.disabled = !agentOn.input.checked;
        // the second host only matters when a first one is chosen, and is another one
        form.host2.disabled = !form.host1.value;
        Array.prototype.forEach.call(form.host2.options, function (o) { o.disabled = !!o.value && o.value === form.host1.value; });
        if (form.host2.value === form.host1.value) form.host2.value = '';
        // a model that is off cannot be the default
        syncDefaultModels();
        syncAgent();
        Array.prototype.forEach.call(langGrid.children, function (lab) {
          lab.className = form.langs[lab.getAttribute('data-lang')].checked ? '' : 'off';
        });
        form.colours.refresh(colour.input.checked);
        form.coloursState.value = JSON.stringify([form.colours.accent(), form.colours.skipped()]);
        if (layout) layout.refresh();
        var dirty = isDirty();
        status.textContent = saving ? 'Saving…' : dirty ? ((layout && window.AdminSections.changeSummary(layout.counts)) || 'You have unsaved changes.') : 'All changes are saved.';
        save.disabled = !dirty || saving;
        revert.disabled = !dirty || saving;
      }

      function doSave(message) {
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
            toast(typeof message === 'string' ? message : 'Settings saved. Visitors see the change on their next page load.');
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
      // who installed which plugin, from where (server/genie_ext_log.go): read-only, drawn when it arrives
      var plugBox = h('div', { 'data-card': 'Plugin installs' });
      function drawPlugs(info) {
        plugBox.textContent = '';
        var entries = (info && info.entries) || [];
        var counts = (info && info.counts) || [];
        var head = h('tr', null, ['When', 'Who', 'What', 'Plugin', 'Where from'].map(function (t) { return h('th', { scope: 'col', text: t }); }));
        var rows = entries.slice(0, 50).map(function (e) {
          var src = String(e.source || '');
          var from = src.indexOf('link:') === 0 ? 'link ' + src.slice(5) : src.indexOf('catalogue:') === 0 ? 'catalogue' : src;
          return h('tr', null, h('td', { text: String(e.t || '').replace('T', ' ').replace('Z', '') }), h('td', { text: e.user || '' }),
            h('td', { text: e.op || '' }), h('td', { text: (e.plugin || '') + (e.version ? ' v' + e.version : '') }), h('td', { text: from }));
        });
        var table = entries.length
          ? h('div', { class: 'table-wrap' }, h('table', null, h('thead', null, head), h('tbody', null, rows)))
          : h('p', { class: 'sub', text: 'Nothing has been installed yet.' });
        var summary = counts.length
          ? h('p', { class: 'sub', text: 'Now installed: ' + counts.filter(function (c) { return c.users > 0; }).map(function (c) { return c.plugin + ' (' + c.users + (c.users === 1 ? ' person' : ' people') + ')'; }).join(', ') + '.' })
          : null;
        plugBox.appendChild(card('Plugin installs', 'Each install, update and removal of a plugin, newest first, for the last ' + ((info && info.days) || 90) + ' days. Who is the account id (the Users view has the email). A link is shown in full. The text of a skill is never recorded.', [summary, table]));
      }
      // the requests for a connection a plugin needs, and who is connected to what (server/genie_conn_requests.go): drawn when they arrive, answered at once
      // Integrations: the servers of the registry (admin-integrations.js, loaded when the section is opened)
      var intBox = h('div', { 'data-card': 'Integrations' }), intView = null;
      function mountIntegrations() {
        if (intView) return intView.refresh();
        intBox.appendChild(h('p', { class: 'loading', text: 'Loading…' }));
        return AdminKit.loadScript('admin-integrations.js').then(function () {
          intView = window.AdminIntegrations.make({ switchesHref: AS.hashFor('genie-agent') });
          intBox.textContent = '';
          intBox.appendChild(card('Integrations', 'Servers Genie can connect to. Only a published server is shown to people; a draft or a tested one is seen by admins alone.', intView.root));
          return intView.refresh();
        }, function (e) { intBox.textContent = ''; intBox.appendChild(h('p', { class: 'sub', text: e.message })); });
      }
      late.integ = mountIntegrations;
      var connBox = h('div', { 'data-card': 'Connection requests' });
      function drawConn(info) {
        connState = info;
        connBox.textContent = '';
        var reqs = ((info && info.requests) || []).filter(function (r) { return r.kind !== 'server'; }); // the requests for a server are on the Integrations page
        var who = (info && info.connected) || [];
        var calls = (info && info.calls) || [];
        function answer(r, action) {
          var reason = h('input', { type: 'text', class: 'field', maxlength: '200', placeholder: 'Why (shown to the user)', 'aria-label': 'Reason' });
          reason.style.width = '100%';
          var decline = action === 'decline';
          ask({ title: (decline ? 'Decline ' : 'Approve ') + r.plugin + (action === 'approve_user' ? ' for this user only' : decline ? '' : ' for everyone') + '?',
            body: decline ? 'The user sees the reason.' : 'The connection can then be connected by ' + (action === 'approve_user' ? 'this user' : 'every user') + ', each with their own token. The approval holds for this exact version of the plugin: a change sends it back to review.',
            content: decline ? reason : undefined, confirm: decline ? 'Decline' : 'Approve', danger: decline }).then(function (yes) {
            if (!yes) return;
            return api.post('admin/connection-requests', { id: r.id, action: action, reason: decline ? reason.value.trim() : '' }).then(function () {
              toast(decline ? 'Declined.' : 'Approved.');
              return refreshConn();
            }, fail);
          });
        }
        var cards = reqs.map(function (r) {
          var conns = (r.connections || []).map(function (c) { return c.title + ' (' + String(c.url || '').replace(/^https:\/\//, '') + ')'; }).join(', ');
          var buttons = r.status === 'waiting'
            ? h('span', { class: 'actions' },
                h('button', { class: 'btn', type: 'button', text: 'Approve for everyone', onclick: function () { answer(r, 'approve'); } }),
                h('button', { class: 'btn', type: 'button', text: 'Approve for this user', onclick: function () { answer(r, 'approve_user'); } }),
                h('button', { class: 'btn', type: 'button', text: 'Decline…', onclick: function () { answer(r, 'decline'); } }))
            : h('span', { class: 'sub', text: r.status + (r.scope ? ' (' + r.scope + ')' : '') + (r.reason ? ': ' + r.reason : '') });
          return row(r.plugin + (r.version ? ' v' + r.version : '') + ' · ' + r.user,
            'From ' + r.source + '. Needs: ' + conns + '. Skills: ' + (r.skills || []).join(', ') + '.' + (r.note ? ' Note: "' + r.note + '"' : '') + ' (' + String(r.at || '').replace('T', ' ').replace('Z', '') + ')', buttons);
        });
        var people = who.length
          ? h('div', { class: 'table-wrap' }, h('table', null, h('thead', null, h('tr', null, ['Who', 'Connected to', 'Since'].map(function (t) { return h('th', { scope: 'col', text: t }); }))),
              h('tbody', null, who.slice(0, 50).map(function (w) { return h('tr', null, h('td', { text: w.user }), h('td', { text: w.conn }), h('td', { text: String(w.at || '').replace('T', ' ').replace('Z', '') })); }))))
          : h('p', { class: 'sub', text: 'Nobody has connected a service yet.' });
        var recent = calls.length
          ? h('p', { class: 'sub', text: 'Last calls: ' + calls.slice(0, 5).map(function (c) { return c.conn + '.' + (c.tool || c.op) + (c.ok ? '' : ' (failed)'); }).join(', ') + '. Only the connection, the tool and the outcome are recorded, never the arguments or the result.' })
          : null;
        connBox.appendChild(card('Connection requests',
          'A plugin from a link that names a connection waits here, with the files and the server address the server fetched. Approve it for everyone or for one user, or decline with a reason. ' + (reqs.filter(function (r) { return r.status === 'waiting'; }).length || 'No') + ' waiting. Below: who is connected to what (never the tokens).',
          [cards.length ? cards : h('p', { class: 'sub', text: 'No requests yet.' }), h('h3', { text: 'Connected' }), people, recent]));
      }
      function refreshConn() {
        return Promise.all([api.get('admin/connection-requests'), api.get('admin/connections')]).then(function (r) {
          drawConn({ requests: r[0].requests, connected: r[1].connected, calls: r[1].calls });
        }, function () { /* not essential */ });
      }
      var keysBox = h('div', { 'data-card': 'API keys' });
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
      drawKeys(keysInfo || { canStore: false, keys: [] }); // while they are still on their way: an empty list, drawn again when they arrive

      // Admin accounts: owners come from the server's environment; an owner can
      // add and remove the others. Saved at once, not with the form's button.
      var adminsBox = h('div', { 'data-card': 'Admin accounts' });
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
        card('Look', null, [form.colours.node, form.coloursState]),
        card('Announcement', 'A banner on top of every page, for news or a planned stop. Visitors can dismiss it.', [
          row('Text', null, level, h('div', null, ann, annCount)), h('div', { class: 'form-row' }, h('div', { class: 'full' }, preview)) ]),
        card('Maintenance mode', 'Visitors can still read pages and use their files, but cannot start terminals. Admins are not affected.', [
          row('Maintenance mode', 'Shows the message below on every page and in a terminal that is refused.', maint.node),
          h('div', { class: 'form-row' }, h('div', { class: 'full' }, maintMsg, maintCount)) ]),
        card('Database capacity', 'How much room MongoDB has, so that the Live map can show used of total (a free Atlas cluster has 512 MB). Leave it empty to use the disk size MongoDB reports, if it reports one. Firestore needs no number: the map shows its daily operations against the free allowance.', [
          row('Capacity in MB', 'Used only with MongoDB. Amber from 80% used, red from 95%.', form.dbCap) ]),
        card('Files', 'Text files up to this size open in the editor. A bigger text file opens read-only in a viewer, in pieces, that only the person who chose it sees. A binary file always opens as a hex dump in that viewer.', [
          row('Largest file the editor opens, in MB', 'From 1 to 16. 16 is the most: the editor is shared through Firebase, which refuses a write over 16 MB. Leave it empty for 16. Visitors get the new limit when they reload the page.', form.maxFile) ]),
        card('Workspace snapshots', 'Signed-in users\' workspaces that changed are copied to the object store (S3, R2, B2) this often, once nothing in them has changed for 30 seconds. Only the newest 3 snapshots of each workspace are kept, so a shorter time means older snapshots go sooner. Without an object store set up, nothing is copied.', [
          row('Minutes between snapshots', 'From 1 to 60. Leave it empty for 5. The change is used at once by the running job.', form.snapEvery) ]),
        card('Practice', 'A signed-in user\'s practice list and the code they write for each question are kept in the hidden .practice folder of their workspace, so they follow the workspace (snapshots, other browsers). Off keeps them in the server\'s practice database, as before. A user who has no .practice folder yet starts from what the database holds.', [
          row('Practice is saved in the workspace', null, practiceWs.node) ]),
        card('Internet for REPLs', 'Lets the REPLs of signed-in users reach public internet addresses through a proxy of the server, so pip, npm, curl and git over https work, on the gateway and on every worker. Guests and admins are not affected: guests get no network, and an admin\'s REPL already has its own. Private addresses, the cloud metadata address and mail (port 25) are always refused. On Render, outbound traffic counts against the plan\'s bandwidth. The OPENREPL_EGRESS=public environment variable does the same.', [
          row('Internet for signed-in users', 'Off by default. Terminals that are already open get it when they are opened again.', egressOn.node) ]),
        card('Metering', 'What the IDE sends to each user is counted, one total for a signed-in user\'s day and for a guest\'s session (a guest folder lasts an hour). Only the size of each message is kept, never the content, and only what goes out to the user: what the user sends in is not counted. The website itself (pages, scripts, images, the profile and this dashboard) is not counted.', [
          row('Metering', 'Off: nothing is counted and nothing is limited.', ideOn.node),
          row('Admins are exempt', 'On by default: an admin account is not counted and never limited.', ideAdmins.node),
          row('Counted parts', 'Untick a part to leave it out of the count and the limits.', null, h('div', { class: 'cm-checks' }, IDE_PARTS.map(function (p) {
            return h('label', { class: 'cm-check' }, form.ideParts[p[0]], h('span', { text: p[1] }));
          }))) ]),
        card('Terminal speed', 'A terminal that prints faster than this is paused (the program waits, nothing more is sent), then the run is ended with a notice, like pressing Stop. The terminal can be started again at once; files are never touched.', [
          row('Allowed per window (MB)', 'From 1 to 1024. Empty: 20.', form.ideTermMB),
          row('Window length (seconds)', 'From 1 to 600. Empty: 10.', form.ideTermWindow),
          row('Seconds over the cap before the run is ended', 'From 1 to 600. Empty: 5. A burst that settles down inside it carries on.', form.ideTermGrace),
          row('When over the cap', 'Pause only never ends a run: the program just waits until the window has room.', form.ideTermAction) ]),
        card('Daily total, Free plan', 'One number for everything the IDE sends. A guest gets it for the session (the count ends with the guest folder), a signed-in user for a day. At 100% the heavy parts stop: terminal output (the run is ended), Genie questions, downloads and the Preview. Saving, the file list and small files for the editor keep working from the reserve.', [
          row('Guest, per session (1 hour) (MB)', 'From 1. Empty: 50.', form.ideGuestMB),
          row('Signed in, per day (MB)', 'From 1. Empty: 200.', form.ideUserMB),
          row('Warn at (%)', 'From 1 to 99. Empty: 80. The IDE shows a chip and a note once.', form.ideWarnPct),
          row('Reserve kept open at the limit (MB)', 'From 1 to 1024. Empty: 2.', form.ideReserveMB),
          row('Small files still open at the limit, up to (KB)', 'From 1 to 16384. Empty: 256.', form.ideSmallKB),
          row('At the limit', 'The way this proposal handles it (canvas option A); the other ways are not built.', form.ideAtLimit),
          row('Resets at', 'The time a day starts, in UTC (empty: 00:00). Users see it in their own time zone.', h('span', null, form.ideReset, ideLocal)) ]),
        card('Plans', 'Ready for paid and credit plans: the Free plan is the numbers above.', [
          row('Free (active)', 'The numbers above.', idePlanText),
          row('Paid plans and credits', 'Defined later.', h('span', { class: 'sub', text: 'defined later' })) ]),
        card('Today, all users', 'Counted since the day started.', ideToday),
        card('Top today', 'The signed-in accounts that sent the most. Guests are only counted, never listed.', ideTop),
        card('Per user', 'The Users view has a "Data today" line in each user\'s drawer, and a button to reset today\'s count by hand.', null),
        card('Preview', 'The Preview pane shows a web page or program that a visitor wrote, in a sandbox on this site. Switch it off here if it is abused: the button disappears and the server refuses new previews.', [
          row('Preview is available', null, previewOn.node),
          row('Preview for guests', 'Visitors who are not signed in. Off keeps it for signed-in users only.', previewGuests.node),
          row('Genie may act in the Preview', 'Genie\'s agent can click, type, press keys, scroll, choose, go to other pages, run an expression and change the storage of the page the Preview shows. Off: it can still read the page, its console and its calls. It asks the user first in any case.', previewAgent.node),
          row('Genie may draw on the canvas', 'The Canvas tab of the Preview pane: boards of plain HTML that Genie draws and the user moves, retypes and pins notes on, saved in the workspace (.genie/canvas). Off: the tab disappears, the server refuses canvas calls and Genie has no canvas actions.', canvasOn.node),
          row('Minutes a preview link works for guests', 'From 10 to 1440 (a day). Leave it empty for 60 (1 hour). A pane gets a new link by itself before the old one ends; a link opened in a tab of its own, or copied, stops at its end. A change applies to links made after it.', form.previewLifeGuest),
          row('Minutes a preview link works for signed-in users', 'From 10 to 1440. Leave it empty for 180 (3 hours).', form.previewLifeUser),
          row('Preview base URL', 'Optional. The host the preview frames open on, for example preview.openrepl.com (a host name alone takes the scheme and the port of the site the page is opened from, for example http://preview.localhost:8080 when the site is http://localhost:8080). It must not have the same host name as this site. No path. Frames then open as <host>/&lt;token&gt;/…; leave it empty to keep this site\'s /preview/&lt;token&gt;/… addresses. Point that host at this OpenREPL server and keep its Host header.', form.previewBaseURL),
          row('Longest a share link works for guests (minutes)', 'From 10 to 10080 (7 days). Leave it empty for 60 (1 hour). The owner chooses the time in the Share popover and may add time up to this: guests 30 minutes or 1 hour; signed-in users also 4 hours, 24 hours and 7 days, as far as this allows.', form.shareLifeGuest),
          row('Longest a share link works for signed-in users (minutes)', 'From 10 to 10080 (7 days, as long as a sign-in lasts). Leave it empty for 10080.', form.shareLifeUser),
          row('Longest a code link works (days)', 'From 1 to 365. Leave it empty for 30 (a month). It is the latest day the calendar offers when someone makes a code link (Share, Share code), and the day it starts on when that is shorter than a month. The server removes ended links every week. A change applies to links made after it.', form.snippetMax),
          row('Hours between two cleanups of shared sessions', 'From 1 to 720. Leave it empty for 24 (daily). The server removes, from Firebase, the sessions whose link ended or ran out, and sessions that have no end and no owner online for a whole interval (the terminal, the code, the chat and the files records). It needs the service account key of the project (OPENREPL_FIREBASE_ADMIN, or OPENREPL_FIRESTORE_CREDENTIALS); the Health page says if it is missing.', form.sessionCleanup) ]),
        card('Languages', 'Untick a language that is broken. The picker greys it out and its terminals refuse to start with a message; terminals that are already open keep running. Admins can still start it, to test a fix.', langGrid),
        card('Default models', 'What a visitor starts with. They can pick another model; their own pick is kept for each mode.', [
          row('Chat model', 'Starts in Chat, and answers a request that names no model. Built in is Luna for visitors and GPT-4o mini for older callers.', defModel),
          row('Agent model', 'Starts in Agent mode, is what the Switch button of the small-model note offers, and writes the practice questions (the New question dialog and the practice pages that make questions). Only a model ticked for Agent mode (in its row of the Genie card) can be chosen.', agentModel),
          row('Effort for the Agent model', 'How long it thinks first. Empty: Low.', form.agentEffort),
          agentNote ]),
        card('Genie', 'The AI helper. When it is off, its buttons are hidden and the server refuses its requests, so no OpenAI calls are made for visitors.', [
          row('Genie is available', null, genie.node),
          modelToggles.map(function (t) {
            var what = t.model.provider === 'openrouter' ? 'Through OpenRouter.' : t.model.provider === 'cloudflare' ? 'Through Cloudflare Workers AI (free plan: a daily allowance the whole site shares).' : 'Through OpenAI.';
            if (!t.model.keySet) what += ' No key is set on the server, so it cannot answer yet.';
            var check = function (input, label) { return h('label', { class: 'cm-check' }, input, h('span', { text: label })); };
            return row(t.model.name, what, t.node, h('div', { class: 'cm-checks' },
              check(form.featured[t.model.id], 'Starts in the short list'), check(agentTick({ id: t.model.id, name: t.model.name }), 'Agent mode'),
              check(form.guestBoxes[t.model.id], 'Available for guest users')));
          }),
          row('Guests', 'Requests per minute (0 or empty uses the built-in rate).', guest),
          row('Signed-in users', 'Requests per minute (0 or empty uses the built-in rate).', user) ]),
        card('Added models', 'The models above are built in and stay. Add any other model of OpenRouter (find them at openrouter.ai/models), OpenAI or Cloudflare Workers AI by its id, switch it on when you want visitors to have it, and remove it when you do not. A model that is off is not offered, and the server refuses it. Save to apply.', [
          openRouterKeySet ? null : h('p', { class: 'callout', text: 'No OpenRouter key is set on the server, so OpenRouter models cannot answer yet. Add one under API keys below.' }),
          cloudflareKeySet ? null : h('p', { class: 'callout', text: 'The Cloudflare token and account id are not both set on the server, so Cloudflare models cannot answer yet. Add them under API keys below.' }),
          h('p', { class: 'callout info', text: 'A model whose id ends in :free costs nothing, but its provider may keep and use what visitors send (their code and terminal output) and its limits are shared by everyone, so it can be busy. Visitors see it labelled free. Any other model is paid for from your OpenRouter credit. Name the providers that may answer if you want to know where the code goes; empty lets OpenRouter choose. The privacy page names only the hosts of Gemma and says other models are chosen by the site owner.' }),
          customList,
          h('div', { class: 'cm-add' }, addProvider, addId, addName, addBtn), cfList, cfNote ]),
        card('Genie limits', 'What Genie is told about the page and how long its answers may be. An empty box means the built-in value. Changes reach a page the next time it loads.', [
          row('Editor code', 'Characters of the editor\'s code sent with each question.', form.ctxEditor),
          row('Terminal output, characters', 'The most recent characters of the active terminal.', form.ctxTermChars),
          row('Terminal output, lines', 'How many recent lines are read from the terminal first.', form.ctxTermLines),
          row('Conversation length', 'Messages Genie remembers (the oldest go first).', form.history),
          row('Added to a message: items', 'Files, folders and selections a user may add to one message with Add to Genie chat.', form.attachItems),
          row('Added to a message: one item', 'Characters read of one added file or selection; a longer one is cut.', form.attachChars),
          row('Added to a message: all items', 'Characters of everything added to one message; what does not fit is left out.', form.attachTotal),
          capRows.map(function (c) { return row('Answer size: ' + c.model.name, 'The most tokens an answer may have. A visitor asks for less by choosing a lower effort.', c.input); }),
          row('OpenRouter time limit', 'Seconds before Gemma\'s answer is given up on. Keep it under 90: the site\'s proxy gives up at about 100.', form.orTimeout),
          row('OpenRouter first host', 'Gemma is run by this host first.', form.host1),
          row('OpenRouter second host', 'Used only when the first cannot answer. No other host is ever used.', form.host2) ]),
        card('Genie prices and cost', 'What each model costs, to work out what users spend (Genie usage). Cost is worked out when it is shown, so changing a price changes every total. Nothing is charged by this.', [
          priceInputs.map(function (p) {
            return row('Price: ' + p.model.name, 'Dollars for a million tokens, sent then written. A model that reports its own cost (OpenRouter) uses that instead when it does.',
              h('div', { class: 'toolbar' }, h('span', { class: 'sub', text: 'Sent' }), p.inBox, h('span', { class: 'sub', text: 'Written' }), p.outBox));
          }),
          row('Show the cost to users', 'Off by default: users see the tokens they used. On adds their cost to the usage details.', showCost.node) ]),
        card('Skills and plugins', 'A skill is a short text file that tells Genie how to do a kind of task. Users keep their own in the hidden folder .genie/skills of their workspace, type / to use one, and install plugins (folders of skills, with no code) with /add-plugin. Nothing in a skill or a plugin runs by itself: whatever a skill asks Genie to do goes through the same cards, permissions and limits as any task, and a skill can only narrow the actions of a task, never add to them.', [
          row('Skills and plugins', 'On by default. Off: the / menu has no skills and /add-plugin does nothing; files already in workspaces stay where they are.', extOn.node),
          row('Plugins from a link', 'Off by default: a user can install only the plugins of the catalogue below. On: a signed-in user can also paste a GitHub repository, a folder of one (a branch with a slash in its name works) or a .zip link. The server fetches it: a GitHub link file by file through GitHub\'s API (save a GitHub token in API keys, or set OPENREPL_GITHUB_TOKEN on the server, so that GitHub\'s limit of 60 requests an hour without a token does not stop it), a .zip at most 2 MB, public https addresses only, 12 links an hour for each user. It keeps only text: plugin.json, skills and notes. The user reads what it brings, and ticks a box, before it is installed. A plugin that names a connection waits for an admin unless \'Allow connections without approval\' is on (card Connections). Each link fetched is written to the server log with the user and the address.', plgLinks.node),
          row('On for everyone', 'Plugins of the catalogue that every user has without installing them (their skills are in the / menu). Their text lives in the server, not in the workspace.', sitePlugBoxes) ]),
        card('Connections', 'A connection lets Genie use a service the user connected (GitHub ...) through MCP. The server makes every call with the user\'s own token, kept encrypted (it needs OPENREPL_SECRET), never shown and never put in a prompt, a chat or a snapshot. Reading asks once, writing asks each time, and what cannot be undone asks every time. What a service returns is treated as outside text: after it, anything risky asks the user again. A task may make 20 calls; a result sent to the model is cut at 6,000 characters; a call stops after 30 seconds.', [
          row('Connections to other services', 'On by default. Off: nobody can connect a service or use a connection, and a plugin that needs one is not installed. Tokens already saved stay where they are.', connOn.node),
          row('Allow connections without approval', 'Off by default: a plugin from a link that names a connection waits for an admin (see Connection requests below). On: the user reads the card and installs it, and connects with their own token. Connections of the site\'s own catalogue (GitHub) never need approval when a catalogue plugin names them.', connNoApproval.node),
          row('Only these servers', 'One host for each line, like api.githubcopilot.com (no https:// and no port). Empty: any public https server. When it is filled, a plugin from a link may name only these, with or without approval, and a connection to another host is refused at Connect.', form.connHosts) ]),
        connBox,
        plugBox,
        intBox,
        card('Agent mode', 'Genie works on a task in steps: it writes in the editor and runs the code, after the user allowed each kind of action and reviewed the changes. Only signed-in users have it, and it is on unless you switch it off here. Each task costs a user one request, like a chat message, however many steps it has.', [
          row('Agent mode', 'Shows the Chat | Agent switch in the Genie panel for signed-in users. Chat stays the default.', agentOn.node),
          row('Allow everything for a session', 'On: a user can answer one card with "Allow everything for this session", and the editor\'s changes then go in without a review (each can be undone). Off: a card for every kind of action, and every change is reviewed. Allowing one kind for the session stays.', agentAuto.node),
          row('Agent: run commands', 'On: Genie can run a command (bash -c) in a signed-in owner\'s workspace, after the user allowed it, in the same sandbox as the terminal, for at most 120 seconds, with no input and no outside network. Each command is logged (workspace, exit code), not its output. Off: the server refuses every command.', agentShell.node),
          row('Allow riskier things for a while', 'On: a user can, on purpose (typing a phrase), let Genie do risky terminal lines, delete to a Trash folder, rename and move, and close terminal tabs for 30 minutes or an hour. Hidden files, anything outside the workspace and sudo are never allowed. Off: those are asked about every time.', agentRisky.node),
          row('On the practice page', 'Off by default: the practice page is for solving questions with an interviewer.', agentPractice.node),
          row('Stream answers', 'Off by default. On: a chat answer and the words of an agent step reach the page while the model writes them, instead of all at once. Needs a site that does not hold back a response (Cloudflare and other proxies in front must pass it on). Models of Cloudflare Workers AI always answer whole, because they do not say what a streamed answer used.', streamOn.node),
          row('Agent: native tool calling', 'Off by default. On: a model ticked below answers each agent step by calling one function (its host checks the shape) instead of writing JSON as text. The agent works the same. The OpenAI models are ticked until you choose; tick another model only if its host takes forced tool calls. If a host refuses the call, the same step is sent again as text, nobody sees an error, the refused call is not counted, and that model is left without the tool for an hour. Refusals are counted in Genie usage.', agentTools.node),
          row('Models that take it', 'One box for each model. Needs the switch above.', toolBoxes),
          row('Tasks per hour', 'How many tasks a signed-in user may start in an hour (admins have no limit). Counted on this server and reset when it restarts.', form.agentTasks),
          row('Steps per task', 'How many times Genie may answer in one task, at most 8.', form.agentSteps),
          row('Auto model choice', 'On: the model list has an Auto entry. The page picks by rules, with no model asked: the small model answers a plain question in Chat, the strong one answers a message that asks for a change, one with files attached, one with code in it, and every Agent task. The chat says which one answered and offers a stronger one. Both models must be on in Models.', autoOn.node),
          row('Auto: small model', 'Answers plain questions. A model that is off in Models cannot be chosen.', form.autoSmall),
          row('Auto: strong model', 'Answers edits, tasks and anything with files attached.', form.autoStrong),
          row('Auto is the default for', 'New users: people who never picked a model. A person who picks Auto or a model themselves keeps that.', form.autoDefault),
          row('Models for a stuck task', 'When the same step fails three times, Genie stops and offers to try it again with the first of these models that the user may use. Model ids, in order, separated by commas, at most 5. Empty: the card offers only a hint and Stop.', form.escalation),
          row('Genie log', 'On: the agent of a signed-in user reports each failed step, each stuck task and how each task ended (the model, the action, the file extension, a short reason with paths and quoted text taken out, and counts; never a name, a message, a file or the terminal text). You read it under System, Genie log. Switch it off for users on a paid plan once there is one, and say so in the privacy notes.', genieEvents.node),
          row('Days the Genie log is kept', 'From 1 to 365. Leave it empty for 30. Older days are deleted.', form.genieDays),
          row('Health alert: failed calls (%)', 'The Genie health card flags a model when this share of its calls failed (1 to 100). Leave it empty for 20.', form.healthErr),
          row('Health alert: time to start answering (ms)', 'The Genie health card flags a model when its slowest twentieth takes this long to start answering (500 to 120000). Leave it empty for 8000.', form.healthSlow) ]),
        card('Genie chat history', 'Each chat of the Genie panel is saved in its owner\'s own workspace, in the hidden folder .genie/chats, one JSON Lines file for each chat, and comes back as a tab after a reload. It goes when the workspace goes: a guest workspace that the server removes takes its chats with it. Nobody, an admin included, can read a chat from here; this page only sets whether and how much is kept.', [
          row('Save chat history in workspaces', 'On by default. Off: nothing is saved or listed, as before; chats already saved stay in the workspaces.', chatOn.node),
          row('Also for guests', 'Off: only signed-in users have their chats saved.', chatGuests.node),
          row('Most chats a workspace keeps', 'The oldest chat that is not open goes first. Empty: 50.', form.chatMax),
          row('Most open tabs', 'Empty: 8.', form.chatTabs),
          row('Most size of one chat (KB)', 'The oldest messages of a chat that grows past this are left out of its file. Empty: 200.', form.chatKB),
          row('Summarize long chats', 'When the older turns of a chat pass the number below, the model in use writes a short summary that is kept in the chat and sent with the last turns. It is one more request of that model, counted like any other (guests too).', sumOn.node),
          row('Tokens before a chat is summarized', 'Empty: 6000. Between 1000 and 40000. Only the turns of the chat count; the editor and terminal text does not and is sent as it is.', form.sumChars),
          row('Days a chat is kept', 'A chat that is not open and was not used for this many days is removed. Empty: for ever.', form.chatDays) ]),
        card('Usage record and credits', 'Every answer of Genie is recorded with its credits (counts only, never what was asked or answered), so you can see what credits would have been. A credit is a tenth of a cent: a model costs the credits you set here, else its price above (a dollar for a million tokens is a credit for a thousand), else the cost its provider reports. Nothing is charged or limited until you switch credits on.', [
          row('Record every answer', 'On by default. Off stops the record; the totals so far stay.', meterOn.node),
          row('Use credits for limits', 'Off: nothing is charged or limited, as before. On: the default plan below counts for signed-in users (not admins): a user who has used the month\'s credits waits for the next month, and a task asks before it goes past its budget. Guests keep their fixed allowance.', creditsOn.node),
          row('Default plan name', 'Shown to users in the usage details once credits are on. Empty: Free.', form.planName),
          row('Credits a month', 'What a signed-in user may use in a month. Empty: no monthly cap.', form.planCredits),
          row('Credits one task may use', 'A task asks the user before the next step would pass this, and carries on only if they say yes. Empty: no budget; the steps of a task stay at the number above.', form.planBudget),
          weightInputs.map(function (p) {
            return row('Credits: ' + p.model.name, 'Credits for a thousand tokens, sent then written. Empty: worked out from the price above.',
              h('div', { class: 'toolbar' }, h('span', { class: 'sub', text: 'Sent' }), p.inBox, h('span', { class: 'sub', text: 'Written' }), p.outBox));
          }) ]),
        keysBox,
        adminsBox,
        bar);
      form.ready = true;
      late.plugs = drawPlugs;
      late.conn = refreshConn;
      late.connDraw = drawConn;
      if (connState) drawConn(connState);
      if (plugsState) drawPlugs(plugsState); // the list came before the form was drawn
      late.keys = drawKeys;
      late.admins = drawAdmins;
      syncDefaultModels(); // the lists that follow other controls, as they will be, before the starting values of the sections are taken
      syncAgent();
      var shell = sectioned(out, bar);
      update();
      return shell;
    }

    // The settings are drawn as soon as they arrive; the keys and the admins are only in one section (Access), so they are drawn
    // in when they come, and a section is never kept waiting for them.
    function refresh() {
      if (form) return Promise.resolve(); // never redraw a form the admin may be editing
      var keys = api.get('admin/keys').then(function (r) { keysState = r; if (late.keys) late.keys(r); }, fail);
      var admins = api.get('admin/admins').then(function (r) { adminsState = r; if (late.admins) late.admins(r); }, fail);
      Promise.all([api.get('admin/connection-requests'), api.get('admin/connections')]).then(function (r) {
        connState = { requests: r[0].requests, connected: r[1].connected, calls: r[1].calls };
        if (late.connDraw) late.connDraw(connState);
      }, function () { /* not essential */ });
      api.get('admin/plugin-installs').then(function (r) { plugsState = r; if (late.plugs) late.plugs(r); }, function () { /* the list is not essential */ });
      api.get('admin/ide-usage?top=5').then(function (r) { ideState = r; if (late.ide) late.ide(r); }, function () { /* the list is not essential */ });
      return api.get('admin/settings').then(function (r) { body.textContent = ''; body.appendChild(build(r, keysState, adminsState)); return Promise.all([keys, admins]); });
    }
    return { root: root, refresh: refresh, poll: false, dirty: isDirty, destroy: function () { document.removeEventListener('keydown', onSlash); window.removeEventListener('hashchange', onHash); } };
  }

  window.AdminSite = { make: viewSite };
})();
