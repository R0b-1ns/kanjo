// Runs the page script against a minimal fake DOM and prints what it rendered
// as JSON. Usage: node dom.js app.js data.json
const fs = require("fs");
const [, , scriptPath, dataPath, secondPath] = process.argv;
class Node {
  constructor(tag) { this.tag = tag; this.children = []; this.attrs = {}; this.listeners = {}; this.text = ""; this.hidden = false; this.value = ""; this.className = ""; }
  appendChild(c) { this.children.push(c); return c; }
  removeChild(c) { this.children = this.children.filter((x) => x !== c); }
  get firstChild() { return this.children[0] || null; }
  setAttribute(k, v) { this.attrs[k] = v; }
  addEventListener(e, f) { this.listeners[e] = f; }
  set textContent(v) { this.text = String(v); this.children = []; }
  get textContent() { return this.text + this.children.map((c) => c.textContent).join(""); }
}
const ids = {};
globalThis.document = {
  getElementById: (id) => ids[id] || (ids[id] = new Node("#" + id)),
  createElement: (t) => new Node(t),
  createElementNS: (ns, t) => { if (ns !== "http://www.w3.org/2000/svg") throw new Error("bad namespace"); return new Node(t); },
  createTextNode: (t) => { const n = new Node("#text"); n.text = t; return n; },
};
const body = fs.readFileSync(dataPath, "utf8");
const second = secondPath ? fs.readFileSync(secondPath, "utf8") : body;
const requests = [];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// ACTION=race: the first answer is slow and the second one fast.
globalThis.fetch = async (url) => {
  requests.push(url);
  const n = requests.length;
  if (process.env.ACTION === "race" && n === 1) await sleep(60);
  return { ok: true, json: async () => JSON.parse(n === 1 ? body : second) };
};
const $ = (id) => document.getElementById(id);
$("scope").value = process.env.SCOPE || "project";
(async () => {
  (0, eval)(fs.readFileSync(scriptPath, "utf8"));
  await sleep(20);
  if (process.env.ACTION === "scope-change") { // a task was picked, then the scope changes
    $("task").value = "ABC-1";
    $("scope").value = "project";
    $("scope").listeners.change();
    await sleep(20);
  }
  if (process.env.ACTION === "race") { // a second request starts while the first is still pending
    $("since").value = "2026-11-02";
    $("since").listeners.change();
    await sleep(150);
  }
  const bars = (id) => $(id).children.filter((row) => row.className === "bar");
  const rowNames = (id) => bars(id).map((row) => row.children[0].textContent);
  const rowValues = (id) => bars(id).map((row) => row.children[2].textContent);
  console.log(JSON.stringify({
    requests, total: $("total").textContent, unpriced: $("unpriced").textContent, notice: $("notice").textContent,
    status: $("status").textContent, prices: $("prices").textContent,
    modelNames: rowNames("models"), modelValues: rowValues("models"), taskNames: rowNames("tasks"),
    projectsHidden: $("projects-card").hidden, projectNames: rowNames("projects"), failed: $("failed").children.map((c) => c.textContent),
    multi: $("multi").children.map((c) => c.textContent), taskOptions: $("task").children.map((c) => c.textContent),
    tokenRows: $("tokens").children.length,
    legend: $("split").children.length ? $("split").children[$("split").children.length - 1].textContent : "",
  }));
})().catch((e) => { console.error(e); process.exit(1); });
