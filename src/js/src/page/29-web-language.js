// HTML and CSS in the language picker (the top menu of the IDE). They are not languages of the terminal, so the real <select id="optionlist">
// keeps the language the terminal runs: choosing one only sets the editor's mode, puts a starter page in an empty untitled editor, makes the
// button show the choice, and makes Run show the page in the Preview. The part that needs no page is lib-weblang.mjs.
import { WEB_LANGUAGES, WEB_LABELS, WEB_STARTERS, isStarter, planRun } from "./lib-weblang.mjs";

const select = () => document.getElementById("optionlist");
const home = () => String(window.homedir || "");
const rel = (p) => (home() && String(p).indexOf(home() + "/") === 0 ? String(p).slice(home().length + 1) : String(p).replace(/^\/+/, ""));
const abs = (p) => (p ? (String(p).charAt(0) === "/" ? p : home() + "/" + p) : "");
const say = (m) => (typeof window.notify === "function" ? window.notify(m, { type: "info", timeout: 8000 }) : null);
const editorOf = () => (window.editor && window.editor.env ? window.editor.env.editor : null);

function has(value) {
  return WEB_LANGUAGES.indexOf(value) >= 0;
}

// the web language chosen now, "" when the picker shows the terminal's own
function current() {
  const sel = select();
  const o = sel && sel.__menuOverride;
  return o && o.base === sel.value ? o.value : "";
}

function choose(value) {
  const sel = select();
  if (!sel || !has(value)) return false;
  // the editor's mode (HTML, CSS): the colours and the editing; the terminal is left alone
  const mode = document.getElementById("select-lang");
  if (mode && mode.value !== value) {
    mode.value = value;
    mode.dispatchEvent(new Event("change", { bubbles: true }));
  }
  sel.__menuOverride = { value: value, base: sel.value };
  if (sel.__selectMenu) sel.__selectMenu.refresh();
  // a starter page in an empty untitled editor (never over text that is there, or over a file)
  const ed = editorOf();
  const untitled = !(window.FileBrowser && window.FileBrowser.current && window.FileBrowser.current());
  const own = !!(window.EditorTabs && window.EditorTabs.untitledHasOwnText && window.EditorTabs.untitledHasOwnText()); // text the visitor typed
  if (ed && untitled && !own) {
    ed.setValue(WEB_STARTERS[value], -1);
    say(WEB_LABELS[value] + ": Run shows the page in the Preview. The terminal is not changed.");
  } else if (ed && untitled && own && !isStarter(ed.getValue())) {
    if (typeof window.notify === "function") {
      window.notify("Your text is kept. " + WEB_LABELS[value] + ": Run shows the page in the Preview.", {
        type: "info", timeout: 10000,
        actions: [{ label: "Load a starter", onClick: function () { ed.setValue(WEB_STARTERS[value], -1); } }],
      });
    }
  } else {
    say(WEB_LABELS[value] + ": Run shows the page in the Preview. The terminal is not changed.");
  }
  return true;
}

function clear() {
  const sel = select();
  if (!sel || !sel.__menuOverride) return;
  sel.__menuOverride = null;
  if (sel.__selectMenu) sel.__selectMenu.refresh();
}

// Run with a web language: the page is saved (an untitled editor becomes index.html or style.css) and shown in the Preview
async function run() {
  const lang = current();
  const FB = window.FileBrowser;
  if (!lang || !FB || !FB.ready()) return false;
  const exists = (p) => FB.info(rel(p)).exists;
  const plan = planRun(lang, abs(FB.current()), home(), exists);
  const ed = editorOf();
  if (plan.message && !plan.save && !plan.create) {
    say(plan.message);
    return true;
  }
  const text = ed ? ed.getValue() : "";
  if (plan.create) {
    const made = await FB.create(rel(plan.create), "file");
    if (made && made.ok === false) {
      say("The file could not be made: " + made.error);
      return true;
    }
    await FB.open(rel(plan.create)); // it becomes a tab; the editor shows the new, empty file
    if (ed) ed.setValue(text, -1);
  }
  await FB.save();
  if (plan.preview && window.Preview) window.Preview.open(plan.preview);
  if (plan.message) say(plan.message);
  return true;
}

window.WebLanguage = { has: has, choose: choose, clear: clear, current: current, active: () => !!current(), run: run, labels: WEB_LABELS };
