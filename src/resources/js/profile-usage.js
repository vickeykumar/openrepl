// The "Data usage" section of the profile page (canvas "IDE data usage and limits", board 6): what the IDE sent to the signed-in user today
// (a bar split by part), the last 7 days, and the plan. The numbers come from GET ./ide_usage, which the server counts (package ideusage):
// only sizes, never what was sent. The time the day resets is shown in the user's own time zone.
(function () {
  "use strict";
  var card = document.getElementById("profile-usage");
  if (!card) return;
  var body = card.querySelector("[data-usage-body]");
  var PARTS = [
    ["terminal", "Terminal", "profile-usage__c-terminal"],
    ["genie", "Genie", "profile-usage__c-genie"],
    ["files", "File browser", "profile-usage__c-files"],
    ["editor", "Editor", "profile-usage__c-editor"],
    ["preview", "Preview", "profile-usage__c-preview"]
  ];
  var DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }
  function mb(n) {
    n = (n || 0) / 1048576;
    if (n >= 1024) return (n / 1024).toFixed(2) + " GB";
    return (n >= 10 ? Math.round(n) : n.toFixed(1)) + " MB";
  }
  function localTime(ms) {
    return new Date(ms).toLocaleTimeString([], { hour: "numeric", minute: "2-digit", timeZoneName: "short" });
  }
  function left(ms) {
    var m = Math.max(0, Math.round((ms - Date.now()) / 60000));
    return Math.floor(m / 60) + " h " + (m % 60) + " min";
  }
  function kv(label, value) {
    var r = el("div", "profile-usage__kv");
    r.appendChild(el("span", "profile-usage__k", label));
    r.appendChild(el("span", "profile-usage__v", value));
    return r;
  }

  function draw(data) {
    var st = data.state || {};
    var hist = data.history || [];
    body.textContent = "";
    card.hidden = false;

    // today: the number, the bar split by part, and when it resets
    var today = el("div", "profile-usage__today");
    var head = el("div", "profile-usage__head");
    var num = el("div", "profile-usage__num");
    num.appendChild(el("span", "profile-usage__kicker", st.exempt ? "Admin, not limited" : "Free plan, today"));
    var amount = el("span", "profile-usage__amount");
    amount.appendChild(el("span", "profile-usage__big", mb(st.used)));
    if (!st.exempt) amount.appendChild(el("span", "profile-usage__of", " of " + mb(st.limit)));
    num.appendChild(amount);
    head.appendChild(num);
    if (st.resetsAt) head.appendChild(el("div", "profile-usage__reset", "Resets at " + localTime(st.resetsAt) + " in " + left(st.resetsAt)));
    today.appendChild(head);
    var bar = el("div", "profile-usage__bar");
    bar.setAttribute("role", "img");
    var scale = Math.max(st.limit || 0, st.used || 1);
    var parts = st.parts || {};
    var desc = [];
    PARTS.forEach(function (p) {
      var v = parts[p[0]] || 0;
      desc.push(p[1] + " " + mb(v));
      if (v > 0) {
        var seg = el("i", p[2]);
        seg.style.width = (v * 100 / scale) + "%";
        bar.appendChild(seg);
      }
    });
    bar.setAttribute("aria-label", "Data used today by part: " + desc.join(", "));
    today.appendChild(bar);
    var legend = el("div", "profile-usage__legend");
    PARTS.forEach(function (p) {
      var item = el("span", "profile-usage__lg");
      item.appendChild(el("i", "profile-usage__sw " + p[2]));
      item.appendChild(document.createTextNode(p[1] + " "));
      item.appendChild(el("b", "profile-usage__mono", mb(parts[p[0]])));
      legend.appendChild(item);
    });
    today.appendChild(legend);
    if (st.atLimit) today.appendChild(el("p", "profile-usage__alert", "You have reached today's limit. Terminal output, Genie questions, downloads and the Preview are paused until the reset; saving and small files keep working."));
    else if (st.warn) today.appendChild(el("p", "profile-usage__alert", "You have used " + st.pct + "% of today's data."));
    body.appendChild(today);

    var two = el("div", "profile-usage__two");

    // the last 7 days
    var week = el("div", "profile-usage__box");
    var wh = el("div", "profile-usage__boxhead");
    wh.appendChild(el("b", "", "Last 7 days"));
    wh.appendChild(el("span", "profile-usage__note", "limit line at " + mb(st.limit)));
    week.appendChild(wh);
    var chart = el("div", "profile-usage__chart");
    chart.setAttribute("role", "img");
    var top = Math.max(st.limit || 1, 1);
    var cdesc = [];
    hist.forEach(function (d, i) {
      var col = el("div", "profile-usage__col");
      var b = el("i", "profile-usage__colbar" + (i === hist.length - 1 ? " is-today" : ""));
      b.style.height = Math.min(100, d.total * 100 / top) + "%";
      b.title = d.day + ": " + mb(d.total);
      col.appendChild(b);
      var dt = new Date(d.day + "T12:00:00Z");
      col.appendChild(el("span", "profile-usage__colname", i === hist.length - 1 ? "Today" : DAYS[dt.getUTCDay()]));
      chart.appendChild(col);
      cdesc.push((i === hist.length - 1 ? "today" : d.day) + " " + mb(d.total));
    });
    chart.setAttribute("aria-label", "Data used on each of the last 7 days: " + cdesc.join(", "));
    var line = el("i", "profile-usage__limit");
    chart.appendChild(line);
    week.appendChild(chart);
    week.appendChild(el("p", "profile-usage__note", "Daily records are kept for 90 days. Only sizes are stored, never what was sent."));
    two.appendChild(week);

    // the plan
    var plan = el("div", "profile-usage__box");
    plan.appendChild(el("b", "", "Your plan"));
    plan.appendChild(kv("Plan", st.plan || "Free"));
    plan.appendChild(kv("Daily data", mb(st.limit)));
    plan.appendChild(kv("Terminal speed", mb(st.termBytes) + " / " + (st.termSecs || 10) + " s"));
    plan.appendChild(kv("Warning at", (st.warnPct || 80) + "%"));
    var later = el("div", "profile-usage__later");
    later.appendChild(el("b", "", "Paid plans and credits"));
    later.appendChild(el("span", "", "Coming later. This area will hold the plan picker and the credit balance."));
    plan.appendChild(later);
    two.appendChild(plan);
    body.appendChild(two);

    body.appendChild(el("p", "profile-usage__source", "Where the numbers come from: the size of each message the IDE sent to this account, counted on the server. Website pages and downloads from the site are not in this total."));
  }

  function load() {
    var tz = -new Date().getTimezoneOffset();
    fetch("./ide_usage?days=7&tz=" + tz, { credentials: "same-origin", cache: "no-store" })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(new Error("HTTP " + r.status)); })
      .then(function (data) {
        if (!data.state || !data.state.on) return; // metering is off: nothing to show
        draw(data);
      })
      .catch(function () { /* the section stays hidden */ });
  }
  load();
})();
