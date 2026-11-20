// The Plugins dialog, opened with /add-plugin (LLD 19 section 23): the plugins of the site's catalogue, the ones installed in this
// workspace, and, when an admin allows it, a link. It only draws and asks the server (POST genie_ext); the server fetches a link and
// checks it, and nothing is written until the person has read what the plugin brings and says Install.

export interface PluginApi {
  call(body: Record<string, any>): Promise<{ ok: boolean; status: number; data: any }>;
  // a plugin was installed, updated or removed: the skills of the box change
  changed(): void;
  close(): void;
}

interface CatalogueRow {
  id: string;
  title: string;
  version: string;
  description: string;
  official: boolean;
  skills: string[];
  notes?: number;
  installed: boolean;
  installedVersion?: string;
  update: boolean;
  site: boolean;
}
interface InstalledRow {
  name: string;
  title: string;
  version: string;
  description: string;
  source: string;
  link: boolean;
  skills: number;
}

function el(tag: string, cls = "", text = ""): HTMLElement {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text) e.textContent = text;
  return e;
}

function button(label: string, cls: string, onClick: () => void): HTMLButtonElement {
  const b = el("button", "cw-plg__btn " + cls, label) as HTMLButtonElement;
  b.type = "button";
  b.addEventListener("click", onClick);
  return b;
}

function pill(text: string, cls = ""): HTMLElement {
  return el("span", "cw-plg__pill" + (cls ? " cw-plg__pill--" + cls : ""), text);
}

// the actions a skill uses that can change things: shown in yellow on the card
const CHANGING = ["bash", "editor_write", "editor_insert", "editor_replace", "files_new", "files_save", "files_rename", "files_cut", "files_copy", "files_paste", "files_move", "files_delete", "terminal_type", "run", "debug"];

export function errorText(r: { ok: boolean; status: number; data: any }): string {
  const m = r.data && r.data.error && r.data.error.message;
  if (m) return String(m);
  if (r.status === 403) return "That is not allowed here.";
  return "That did not work. Try again in a moment.";
}

export function drawPlugins(host: HTMLElement, api: PluginApi) {
  let catalogue: CatalogueRow[] = [];
  let installed: InstalledRow[] = [];
  let fromLinks = false;
  let tab: "all" | "installed" = "all";
  let query = "";
  let card: HTMLElement | null = null; // the install card, when one is shown
  let message = "";

  host.textContent = "";
  const head = el("div", "cw-ctx__head");
  const title = el("h2", "cw-ctx__title", "Plugins");
  title.id = "chat-widget__plg-title";
  const close = el("button", "cw-ctx__close") as HTMLButtonElement;
  close.type = "button";
  close.setAttribute("aria-label", "Close plugins");
  close.textContent = "✕";
  close.addEventListener("click", () => api.close());
  head.append(title, close);
  const lede = el("p", "cw-ctx__lede", "A plugin adds skills to the / menu. It is text and settings only: it cannot run code. What it brings is shown before you install it.");
  const search = el("input", "cw-plg__search") as HTMLInputElement;
  search.type = "search";
  search.placeholder = "Search plugins";
  search.setAttribute("aria-label", "Search plugins");
  const tabs = el("div", "cw-plg__tabs");
  const msg = el("div", "cw-plg__msg");
  msg.setAttribute("role", "status");
  const list = el("div", "cw-plg__list");
  const linkBox = el("div", "cw-plg__link");
  host.append(head, lede, search, tabs, msg, list, linkBox);
  search.addEventListener("input", () => {
    query = search.value.trim().toLowerCase();
    drawList();
  });

  function say(text: string, bad = false) {
    message = text;
    msg.textContent = text;
    msg.className = "cw-plg__msg" + (bad ? " is-bad" : "");
  }

  async function load() {
    const r = await api.call({ op: "catalogue" });
    if (!r.ok) {
      say(errorText(r), true);
      return;
    }
    catalogue = r.data.plugins || [];
    installed = r.data.installed || [];
    fromLinks = !!r.data.fromLinks;
    drawTabs();
    drawList();
    drawLink();
    // the button that was pressed may be gone: keep the focus in the dialog, so that Escape still closes it
    if (!host.contains(document.activeElement)) close.focus();
  }

  function drawTabs() {
    tabs.textContent = "";
    ([["all", "All"], ["installed", "Installed " + installed.length]] as const).forEach(([id, label]) => {
      const b = button(label, "cw-plg__tab" + (tab === id ? " is-on" : ""), () => {
        tab = id;
        card = null;
        drawTabs();
        drawList();
      });
      b.setAttribute("aria-pressed", tab === id ? "true" : "false");
      tabs.appendChild(b);
    });
  }

  function matches(text: string): boolean {
    return !query || text.toLowerCase().includes(query);
  }

  function drawList() {
    list.textContent = "";
    if (card) {
      list.appendChild(card);
      return;
    }
    if (tab === "all") {
      const rows = catalogue.filter((c) => matches(c.title + " " + c.description + " " + c.skills.join(" ")));
      rows.forEach((c) => list.appendChild(catalogueCard(c)));
      if (rows.length === 0) list.appendChild(el("div", "cw-plg__none", "No plugin matches."));
    } else {
      const rows = installed.filter((p) => matches(p.title + " " + p.description + " " + p.name));
      rows.forEach((p) => list.appendChild(installedCard(p)));
      if (rows.length === 0) list.appendChild(el("div", "cw-plg__none", installed.length === 0 ? "No plugin is installed in this workspace yet." : "No plugin matches."));
    }
  }

  function catalogueCard(c: CatalogueRow): HTMLElement {
    const box = el("div", "cw-plg__card");
    box.dataset.plugin = c.id;
    const body = el("div", "cw-plg__body");
    body.append(el("b", "", c.title), el("div", "cw-plg__desc", c.description));
    const pills = el("div", "cw-plg__pills");
    pills.append(pill(c.skills.length + (c.skills.length === 1 ? " skill" : " skills"), "ok"));
    if (c.notes) pills.append(pill(c.notes + (c.notes === 1 ? " note" : " notes"), "ok"));
    if (c.official) pills.append(pill("official", "info"));
    if (c.site) pills.append(pill("on for everyone", "info"));
    body.appendChild(pills);
    box.appendChild(body);
    if (c.installed) {
      if (c.update) box.appendChild(button("Update", "cw-plg__btn--main", () => void review({ id: c.id }, c.title)));
      else box.appendChild(el("span", "cw-plg__state", "Installed"));
    } else {
      box.appendChild(button("Install", "cw-plg__btn--main", () => void review({ id: c.id }, c.title)));
    }
    return box;
  }

  function installedCard(p: InstalledRow): HTMLElement {
    const box = el("div", "cw-plg__card");
    box.dataset.plugin = p.name;
    const body = el("div", "cw-plg__body");
    body.append(el("b", "", p.title), el("div", "cw-plg__desc", p.description || p.name));
    const pills = el("div", "cw-plg__pills");
    pills.append(pill(p.skills + (p.skills === 1 ? " skill" : " skills"), "ok"), pill("v" + (p.version || "?")));
    pills.append(p.link ? pill("from a link", "warn") : pill("catalogue", "info"));
    body.appendChild(pills);
    box.appendChild(body);
    const actions = el("div", "cw-plg__actions");
    actions.append(button("Update", "", () => void review({ name: p.name }, p.title)), button("Remove", "cw-plg__btn--danger", () => confirmRemove(p, box)));
    box.appendChild(actions);
    return box;
  }

  function confirmRemove(p: InstalledRow, box: HTMLElement) {
    const ask = el("div", "cw-plg__confirm");
    ask.append(el("span", "", "Remove " + p.title + "? Its folder in .genie/plugins goes. Skills you wrote are never touched."));
    ask.append(
      button("Remove", "cw-plg__btn--danger", async () => {
        const r = await api.call({ op: "plugin_remove", name: p.name });
        if (!r.ok) return say(errorText(r), true);
        say(p.title + " was removed.");
        api.changed();
        await load();
      }),
      button("Keep", "", () => ask.remove())
    );
    box.appendChild(ask);
  }

  // what a plugin brings, before anything is written
  async function review(sel: Record<string, string>, label: string) {
    say("Looking at " + label + "…");
    const r = await api.call({ op: "plugin_fetch", ...sel });
    if (!r.ok) {
      say(errorText(r), true);
      return;
    }
    say("");
    const p = r.data.plugin;
    const box = el("div", "cw-plg__review");
    box.dataset.review = p.name;
    const top = el("div", "cw-plg__reviewhead");
    top.append(el("b", "", p.title + (p.version ? " · v" + p.version : "") + (p.author ? " · " + p.author : "")));
    box.appendChild(top);
    if (p.description) box.appendChild(el("div", "cw-plg__desc", p.description));
    if (p.link) {
      const warn = el("div", "cw-plg__warn");
      warn.append(el("b", "", "Not from the catalogue. "), document.createTextNode("Nobody at this site has reviewed it. It is text and settings only: it cannot run code. Source: " + String(p.source).replace(/^link:/, "")));
      box.appendChild(warn);
    }
    const rows = el("div", "cw-plg__rows");
    (p.skills as any[]).forEach((s) => {
      const row = el("div", "cw-plg__row");
      row.append(el("b", "", "/" + s.name), el("span", "cw-plg__desc", s.description), pill(s.mode));
      const acts = el("div", "cw-plg__pills");
      if (s.actions && s.actions.length) (s.actions as string[]).forEach((a) => acts.appendChild(pill(a, CHANGING.indexOf(a) >= 0 ? "warn" : "ok")));
      else acts.appendChild(pill(s.mode === "agent" ? "every action, asked as usual" : "no actions", ""));
      row.appendChild(acts);
      rows.appendChild(row);
    });
    box.appendChild(rows);
    if (p.notes && p.notes.length) {
      const notes = el("div", "cw-plg__notes");
      notes.appendChild(el("b", "", (p.notes.length === 1 ? "1 note" : p.notes.length + " notes") + ": "));
      notes.appendChild(document.createTextNode((p.notes as any[]).map((n) => n.title).join(", ") + ". Genie reads a note when a task is about what its title says."));
      box.appendChild(notes);
    }
    if (p.ignored > 0) box.appendChild(el("div", "cw-plg__desc", p.ignored + " other file" + (p.ignored === 1 ? "" : "s") + " in the package will not be installed (only plugin.json and skills are kept)."));
    if (p.installed && p.changes) {
      const changes = el("div", "cw-plg__changes");
      if ((p.changes as any[]).length === 0) changes.textContent = "Nothing changed since the installed version.";
      else {
        changes.appendChild(el("b", "", "What changes:"));
        (p.changes as any[]).forEach((c) => changes.appendChild(el("div", "", c.status + ": " + c.path)));
      }
      box.appendChild(changes);
    }
    const files = el("details", "cw-plg__files");
    files.appendChild(el("summary", "", "View the files"));
    (p.files as any[]).forEach((f) => {
      files.appendChild(el("div", "cw-plg__fname", f.path + " · " + f.bytes + " bytes"));
      files.appendChild(el("pre", "cw-plg__pre", String(f.text || "")));
    });
    box.appendChild(files);
    const foot = el("div", "cw-plg__foot");
    let tick: HTMLInputElement | null = null;
    if (p.link) {
      const label = el("label", "cw-plg__tick");
      tick = el("input") as HTMLInputElement;
      tick.type = "checkbox";
      label.append(tick, document.createTextNode(" I read the skills and I want them in .genie/plugins/" + p.name));
      foot.appendChild(label);
    }
    const go = button(p.installed ? "Update" : "Install", "cw-plg__btn--main", async () => {
      go.disabled = true;
      const res = await api.call({ op: "plugin_apply", ...sel, digest: p.digest });
      if (!res.ok) {
        go.disabled = tick ? !tick.checked : false;
        say(errorText(res), true);
        return;
      }
      card = null;
      say(p.title + (p.installed ? " was updated." : " is installed.") + " Its skills are in the / menu.");
      api.changed();
      await load();
    });
    if (tick) {
      go.disabled = true;
      tick.addEventListener("change", () => (go.disabled = !tick!.checked));
    }
    foot.append(go, button("Cancel", "", () => {
      card = null;
      say("");
      drawList();
    }));
    box.appendChild(foot);
    card = box;
    drawList();
    (go as HTMLElement).scrollIntoView({ block: "nearest" });
  }

  function drawLink() {
    linkBox.textContent = "";
    if (!fromLinks) {
      linkBox.appendChild(el("div", "cw-plg__desc", "Add from a link: ask an admin to allow plugins from a link."));
      return;
    }
    linkBox.appendChild(el("b", "", "Add from a link"));
    const row = el("div", "cw-plg__linkrow");
    const input = el("input", "cw-plg__search") as HTMLInputElement;
    input.type = "url";
    input.placeholder = "https://github.com/owner/repository";
    input.setAttribute("aria-label", "Link to a plugin");
    const go = button("Look", "cw-plg__btn--main", () => {
      const v = input.value.trim();
      if (v) void review({ url: v }, "the link");
    });
    input.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter") {
        ev.preventDefault();
        go.click();
      }
    });
    row.append(input, go);
    linkBox.append(row, el("div", "cw-plg__desc", "A GitHub repository, a folder in one, or a link to a .zip file. The server fetches it and shows what it brings before anything is installed."));
  }

  void load();
  return close;
}
