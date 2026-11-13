// HTML and CSS in the language picker. They are not languages of the terminal: choosing one sets the editor's mode, puts a starter page in
// an empty untitled editor, and makes Run show the page in the Preview. The terminal and its language are not touched. This file is the
// part that needs no page: the starters, the names of the files made, and what Run does (29-web-language.js does it).

export const WEB_LANGUAGES = ["html", "css"];

export const WEB_LABELS = { html: "HTML", css: "CSS" };

export const WEB_STARTERS = {
  html: `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>My page</title>
  <style>
    body { font-family: system-ui, sans-serif; max-width: 40rem; margin: 3rem auto; padding: 0 1rem; }
    button { padding: .4rem .9rem; }
  </style>
</head>
<body>
  <h1>Hello, OpenREPL</h1>
  <p id="out">Press Run to see this page in the Preview.</p>
  <button id="go">Click me</button>
  <script>
    var n = 0;
    document.getElementById("go").onclick = function () {
      n++;
      document.getElementById("out").textContent = "Clicked " + n + " time" + (n === 1 ? "" : "s");
      console.log("clicked", n);
    };
  </script>
</body>
</html>
`,
  css: `/* Press Run: the Preview shows index.html with this stylesheet (link it with <link rel="stylesheet" href="style.css">). */
body {
  font-family: system-ui, sans-serif;
  max-width: 40rem;
  margin: 3rem auto;
  padding: 0 1rem;
  color: #1b1b24;
}
h1 { color: #2456b8; }
button {
  padding: .4rem .9rem;
  border: 1px solid #2456b8;
  border-radius: 6px;
  background: #e3edff;
}
`,
};

// is the text one of the starters (so that another starter may replace it)
export function isStarter(text) {
  const t = String(text || "").trim();
  return t === "" || WEB_LANGUAGES.some((l) => WEB_STARTERS[l].trim() === t);
}

const dirOf = (p) => (p.lastIndexOf("/") > 0 ? p.slice(0, p.lastIndexOf("/")) : "");
const join = (dir, name) => (dir ? dir + "/" + name : name);
export const isHtmlName = (p) => /\.html?$/i.test(String(p || ""));
export const isCssName = (p) => /\.css$/i.test(String(p || ""));

// the first of base, base-2, base-3 ... that does not exist (exists: path => bool)
export function freeName(dir, stem, ext, exists) {
  let name = stem + ext;
  for (let n = 2; exists(join(dir, name)) && n < 1000; n++) name = stem + "-" + n + ext;
  return join(dir, name);
}

// What Run does with a web language. `current` is the file shown ("" for an untitled editor), `home` the folder of the workspace, `exists` says
// whether a path of the workspace exists. The answer:
//   {save: path}                 save the editor into this file first (it is open)
//   {create: path}               make this file from the editor's text first (an untitled editor)
//   {preview: path}              then show this file in the Preview
//   {message: "..."}             say this instead (and after a save/create, when there is no page to show)
export function planRun(lang, current, home, exists) {
  const cur = String(current || "");
  if (lang === "html") {
    if (cur === "") {
      const file = freeName(home, "index", ".html", exists);
      return { create: file, preview: file };
    }
    if (isHtmlName(cur)) return { save: cur, preview: cur };
    return { message: "The open file is not an HTML page. Open an .html file, or start from an untitled editor, and press Run again." };
  }
  // css: the page it styles is the index.html beside it
  const dir = cur === "" ? home : dirOf(cur);
  const page = join(dir, "index.html");
  const plan = {};
  if (cur === "") plan.create = freeName(home, "style", ".css", exists);
  else if (isCssName(cur)) plan.save = cur;
  else return { message: "The open file is not a stylesheet. Open a .css file, or start from an untitled editor, and press Run again." };
  if (exists(page)) plan.preview = page;
  else plan.message = "The stylesheet is saved. A stylesheet is shown on an HTML page: make an index.html that links to it (<link rel=\"stylesheet\" href=\"" + (plan.create ? plan.create.split("/").pop() : cur.split("/").pop()) + "\">), then press Run.";
  return plan;
}
