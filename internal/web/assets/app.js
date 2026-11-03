"use strict";
// Every value shown comes from /api/data and is data, never markup: it is
// only ever placed with textContent or an SVG attribute.
const SVG_NS = "http://www.w3.org/2000/svg";
const byId = (id) => document.getElementById(id);

function el(tag, className, text) {
  const e = document.createElement(tag);
  if (className) e.className = className;
  if (text !== undefined) e.textContent = text;
  return e;
}

function svg(tag, attrs) {
  const e = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, String(v));
  return e;
}

const usd = (v) => (v === null ? "no price" : "$" + v.toFixed(4));
const num = (v) => v.toLocaleString("en-US");

function clear(node) {
  while (node.firstChild) node.removeChild(node.firstChild);
}

// bars draws one horizontal SVG bar per group; value gives the number to
// compare and label the text printed at the end of the row.
function bars(node, groups, value, label) {
  clear(node);
  const max = Math.max(0, ...groups.map(value));
  for (const g of groups) {
    const row = el("div", "bar");
    row.appendChild(el("span", "name", g.name));
    const chart = svg("svg", { viewBox: "0 0 100 10", preserveAspectRatio: "none", role: "img" });
    chart.appendChild(svg("rect", { class: "track", x: 0, y: 0, width: 100, height: 10 }));
    const width = max > 0 ? (100 * value(g)) / max : 0;
    chart.appendChild(svg("rect", { class: "claude", x: 0, y: 0, width, height: 10 }));
    row.appendChild(chart);
    row.appendChild(el("span", "val", label(g)));
    node.appendChild(row);
  }
  if (groups.length === 0) node.appendChild(el("p", "note", "No data."));
}

function renderTotal(data) {
  byId("total").textContent = "$" + data.total_usd.toFixed(4);
  const split = byId("split");
  clear(split);
  const sources = Object.keys(data.usd_by_source).sort();
  const chart = svg("svg", { viewBox: "0 0 100 10", preserveAspectRatio: "none", role: "img" });
  chart.appendChild(svg("rect", { class: "track", x: 0, y: 0, width: 100, height: 10 }));
  let x = 0;
  for (const s of sources) {
    const w = data.total_usd > 0 ? (100 * data.usd_by_source[s]) / data.total_usd : 0;
    chart.appendChild(svg("rect", { class: s === "junior" ? "junior" : "claude", x, y: 0, width: w, height: 10 }));
    x += w;
  }
  split.appendChild(chart);
  const legend = el("div", "legend");
  for (const s of ["claude", "junior"]) {
    const item = el("span");
    item.appendChild(el("span", "swatch " + s));
    item.appendChild(document.createTextNode((s === "claude" ? "Claude " : "Juniors ") + "$" + (data.usd_by_source[s] || 0).toFixed(4)));
    legend.appendChild(item);
  }
  split.appendChild(legend);
  byId("unpriced").textContent = data.unpriced_messages > 0
    ? num(data.unpriced_messages) + " messages have no price and are not in the total."
    : "";
  byId("notice").textContent = data.notice;
}

function renderTokens(models) {
  const table = byId("tokens");
  clear(table);
  const head = el("tr");
  for (const h of ["model", "input", "output", "cache write 5m", "cache write 1h", "cache read", "total"]) head.appendChild(el("th", "", h));
  table.appendChild(head);
  for (const m of models) {
    const total = m.input + m.output + m.cache_write_5m + m.cache_write_1h + m.cache_read;
    const row = el("tr");
    row.appendChild(el("td", "", m.name));
    for (const v of [m.input, m.output, m.cache_write_5m, m.cache_write_1h, m.cache_read, total]) row.appendChild(el("td", "", num(v)));
    table.appendChild(row);
  }
}

function renderList(node, lines) {
  clear(node);
  for (const l of lines) node.appendChild(el("li", "", l));
}

function renderTaskFilter(tasks) {
  const select = byId("task");
  const current = select.value;
  clear(select);
  const all = el("option", "", "All tasks");
  all.value = "";
  select.appendChild(all);
  for (const t of tasks) {
    const o = el("option", "", t);
    o.value = t;
    select.appendChild(o);
  }
  select.value = tasks.includes(current) ? current : "";
}

function render(data) {
  renderTotal(data);
  const cost = (g) => (g.usd === null ? 0 : g.usd);
  const costLabel = (g) => usd(g.usd);
  bars(byId("models"), data.models, cost, costLabel);
  bars(byId("tasks"), data.tasks, cost, costLabel);
  bars(byId("days"), data.days, cost, costLabel);
  bars(byId("output"), data.models, (g) => g.output, (g) => num(g.output));
  renderTokens(data.models);
  renderList(byId("multi"), data.multi_task_sessions.map((s) => "Session " + s + " mentions several tasks; it is counted on one task."));
  const all = data.scope === "all";
  byId("projects-card").hidden = !all;
  if (all) bars(byId("projects"), data.projects, cost, costLabel);
  renderList(byId("failed"), data.failed_projects.map((p) => "Project " + p + " could not be read."));
  renderTaskFilter(data.filter_tasks);
  byId("prices").textContent = "Prices: " + data.prices.source + " · retrieved " + data.prices.fetched;
  byId("status").textContent = data.models.length === 0 ? "No usage for this selection. Run kanjo snapshot to save usage." : "";
}

async function refresh() {
  const query = new URLSearchParams({ scope: byId("scope").value });
  if (byId("task").value) query.set("task", byId("task").value);
  if (byId("since").value) query.set("since", byId("since").value);
  try {
    const res = await fetch("/api/data?" + query.toString());
    if (!res.ok) throw new Error("HTTP " + res.status);
    render(await res.json());
  } catch (err) {
    byId("status").textContent = "Cannot load the data (" + err.message + ").";
  }
}

for (const id of ["scope", "task", "since"]) byId(id).addEventListener("change", refresh);
refresh();
