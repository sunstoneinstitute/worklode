// graph.js draws the project Graph page (WL-856): the project's tasks and the
// documents reachable from them, as a force-directed drawing on one canvas.
//
// Everything comes from the JSON at #graph's data-src (GET
// /projects/{id}/graph/data). Nothing is stored: the force layout runs 300
// synchronous ticks before the first paint, so positions are recomputed on
// every load and a node sits where this run put it, not where the last one did.
//
// The canvas is one draw() over the whole node and link set, called on zoom,
// hover, selection, filter and theme change. Hit testing goes through a d3
// quadtree built once over the settled positions. Colours are read from the
// stylesheet's tokens via getComputedStyle, and re-read when the OS scheme or
// <html data-theme> changes, so the drawing follows the cockpit's theme.
//
// Under the cockpit's CSP (style-src 'self') no generated markup may carry a
// style attribute: a swatch's colour is set with el.style.setProperty('--c').
//
// Served at /assets/graph.js by internal/api's assetHandler; a static asset,
// not a generated artifact, so it carries no drift-check surface.
(function () {
  var root = document.getElementById("graph");
  if (!root) return;

  var REL = {
    blocks: ["blocks", [], "blocked by"],
    child_of: ["part of", [1.5, 3], "contains"],
    follow_up_to: ["follows up", [5, 4], "followed up by"],
    duplicate_of: ["duplicate of", [7, 3, 1.5, 3], "duplicated by"],
    planned_in: ["planned in", [2, 2], "mints"],
    about: ["about", [8, 3], "subject of"],
    generated_by: ["wrote", [3, 3, 8, 3], "written by"],
    covers: ["covers", [], "covered by"],
    implements: ["implements", [6, 2], "implemented by"],
    amends: ["amends", [4, 4], "amended by"],
    replaces: ["replaces", [10, 4], "replaced by"],
    requires: ["requires", [2, 4], "required by"],
    wasDerivedFrom: ["derived from", [1, 4], "derived into"],
    defers: ["defers", [6, 3, 1, 3], "deferred by"]
  };
  var KINDS = [["task", "Task", "--g-task"], ["spec", "Spec", "--g-spec"], ["adr", "ADR", "--g-adr"], ["plan", "Plan", "--g-plan"]];
  var DONE = { merged: 1, deployed_prod: 1, released: 1 };
  var STATES = [["open", "Open"], ["done", "Done"], ["abandoned", "Abandoned"]];
  var STATE_LABEL = {
    draft: "Draft", ready: "Ready", in_progress: "In progress", in_review: "In review",
    merged: "Merged", deployed_dev: "Deployed to dev", deployed_prod: "Deployed to prod",
    released: "Released", abandoned: "Abandoned", accepted: "Accepted",
    superseded: "Superseded", withdrawn: "Withdrawn"
  };
  function bucket(kind, s) {
    if (kind === "task") return DONE[s] ? "done" : s === "abandoned" ? "abandoned" : "open";
    if (s === "accepted") return "done";
    if (s === "superseded" || s === "withdrawn") return "abandoned";
    return "open";
  }

  var el = function (id) { return document.getElementById(id); };
  var stage = el("graph-stage"), canvas = el("graph-canvas"), tip = el("graph-tip"), sel = el("graph-sel");
  var ctx = canvas.getContext("2d");
  var nodes = [], links = [], byId = new Map(), adj = new Map();
  var C = {}, transform = d3.zoomIdentity, dpr = 1, cw = 0, ch = 0;
  var hover = null, selected = null, matches = null;
  var on = { kind: new Set(), state: new Set(), edge: new Set() };

  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"]/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c];
    });
  }
  function visible(n) { return on.kind.has(n.kind) && on.state.has(n.st); }
  function linkVisible(l) { return on.edge.has(l.p) && visible(l.source) && visible(l.target); }

  function readTheme() {
    var cs = getComputedStyle(document.documentElement);
    var g = function (t) { return cs.getPropertyValue(t).trim(); };
    C = { bg: g("--bg"), ink: g("--ink"), ink2: g("--ink-2"), muted: g("--ink-3"), edge: g("--g-edge"), hi: g("--accent"), stroke: g("--surface") };
    KINDS.forEach(function (k) { C[k[0]] = g(k[2]); });
  }

  fetch(root.dataset.src, { credentials: "same-origin", headers: { Accept: "application/json" } })
    .then(function (res) {
      if (!res.ok) throw new Error("HTTP " + res.status);
      return res.json();
    })
    .then(start)
    .catch(function (err) {
      sel.innerHTML = '<h2 id="graph-sel-h">Selected</h2><p class="empty">Could not load the graph data: ' +
        esc(err.message) + "</p>";
    });

  function start(data) {
    readTheme();
    var docByID = new Map();
    (data.tasks || []).forEach(function (t) {
      nodes.push({
        id: t.id, kind: "task", title: t.title, state: t.state, taskKind: t.kind, priority: t.priority,
        actor: t.assignee || t.created_by, human: t.human_only, updated: (t.updated_at || "").slice(0, 10),
        url: "/tasks/" + t.id, st: bucket("task", t.state), deg: 0
      });
    });
    (data.docs || []).forEach(function (d) {
      var n = {
        id: d.ref || d.kind.toUpperCase() + "-" + d.id, kind: d.kind, title: d.title, state: d.status,
        updated: (d.updated_at || "").slice(0, 10), url: "/docs/" + d.id, docID: d.id,
        st: bucket(d.kind, d.status), deg: 0
      };
      docByID.set(d.id, n);
      nodes.push(n);
    });
    nodes.forEach(function (n) { byId.set(n.id, n); adj.set(n.id, []); });

    var add = function (from, to, p) {
      var s = byId.get(from), t = byId.get(to);
      if (!s || !t || !REL[p]) return;
      links.push({ source: s, target: t, p: p });
    };
    (data.task_edges || []).forEach(function (e) { add(e.from, e.to, e.type); });
    (data.doc_edges || []).forEach(function (e) {
      var a = docByID.get(e.from_doc), b = docByID.get(e.to_doc);
      if (a && b) add(a.id, b.id, e.type);
    });
    (data.links || []).forEach(function (l) {
      var d = docByID.get(l.doc);
      if (!d) return;
      if (l.type === "generated_by") add(d.id, l.task, l.type);
      else add(l.task, d.id, l.type);
    });
    links.forEach(function (l) {
      l.source.deg++; l.target.deg++;
      adj.get(l.source.id).push(l); adj.get(l.target.id).push(l);
    });
    nodes.forEach(function (n) { n.r = 2.6 + Math.sqrt(n.deg) * 1.6; });

    KINDS.forEach(function (k) { on.kind.add(k[0]); });
    STATES.forEach(function (s) { on.state.add(s[0]); });
    Object.keys(REL).forEach(function (p) { on.edge.add(p); });

    // Layout: one gravity well at the centre; settled before the first paint.
    var W = 1400, H = 900;
    nodes.forEach(function (n, i) {
      var a = i * 2.399, rr = 30 + Math.sqrt(i) * 6;
      n.x = W / 2 + Math.cos(a) * rr; n.y = H / 2 + Math.sin(a) * rr;
    });
    var near = { child_of: 1, covers: 1, planned_in: 1 };
    d3.forceSimulation(nodes)
      .force("link", d3.forceLink(links).distance(function (l) { return near[l.p] ? 30 : 44; }).strength(0.3))
      .force("charge", d3.forceManyBody().strength(-90).theta(0.9))
      .force("x", d3.forceX(W / 2).strength(0.05))
      .force("y", d3.forceY(H / 2).strength(0.05))
      .force("collide", d3.forceCollide(function (n) { return n.r + 1.5; }).iterations(1))
      .stop()
      .tick(300);

    qt = d3.quadtree().x(function (n) { return n.x; }).y(function (n) { return n.y; }).addAll(nodes);
    buildChips(); buildLegend(); renderList(); updateStats();
    matchMedia("(prefers-color-scheme: dark)").addEventListener("change", function () { readTheme(); draw(); buildLegend(); });
    new MutationObserver(function () { readTheme(); draw(); buildLegend(); })
      .observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    window.addEventListener("resize", resize);
    resize();
  }

  // ---- canvas ----
  var qt = null;
  var zoom = d3.zoom().scaleExtent([0.2, 8]).on("zoom", function (e) { transform = e.transform; draw(); });
  d3.select(canvas).call(zoom).on("dblclick.zoom", null);

  function resize() {
    dpr = window.devicePixelRatio || 1;
    cw = stage.clientWidth; ch = stage.clientHeight;
    canvas.width = cw * dpr; canvas.height = ch * dpr;
    fit(); draw();
  }
  function fit() {
    if (!nodes.length || !cw || !ch) return;
    var xs = d3.extent(nodes, function (n) { return n.x; }), ys = d3.extent(nodes, function (n) { return n.y; });
    var k = Math.min(cw / (xs[1] - xs[0] + 120), ch / (ys[1] - ys[0] + 120), 4);
    transform = d3.zoomIdentity.translate(cw / 2 - k * (xs[0] + xs[1]) / 2, ch / 2 - k * (ys[0] + ys[1]) / 2).scale(k);
    d3.select(canvas).call(zoom.transform, transform);
  }
  function neighbourhood(n) {
    var s = new Set([n.id]);
    adj.get(n.id).forEach(function (l) { s.add(l.source.id); s.add(l.target.id); });
    return s;
  }

  function draw() {
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, cw, ch);
    ctx.translate(transform.x, transform.y); ctx.scale(transform.k, transform.k);
    var k = transform.k, focus = selected || hover;
    var nb = focus ? neighbourhood(focus) : null;
    var dimmed = !!(nb || matches);
    var emph = function (n) { return nb ? nb.has(n.id) : matches ? matches.has(n.id) : true; };

    links.forEach(function (l) {
      if (!linkVisible(l)) return;
      var hot = nb && (l.source.id === focus.id || l.target.id === focus.id);
      ctx.globalAlpha = dimmed && !hot && !(matches && emph(l.source) && emph(l.target)) ? 0.12 : hot ? 1 : 0.55;
      ctx.strokeStyle = hot ? C.ink : C.edge;
      ctx.lineWidth = (hot ? 1.8 : 1) / k;
      ctx.setLineDash(REL[l.p][1].map(function (v) { return v / k; }));
      var dx = l.target.x - l.source.x, dy = l.target.y - l.source.y, d = Math.hypot(dx, dy) || 1;
      var ux = dx / d, uy = dy / d;
      var x1 = l.source.x + ux * l.source.r, y1 = l.source.y + uy * l.source.r;
      var x2 = l.target.x - ux * (l.target.r + 2 / k), y2 = l.target.y - uy * (l.target.r + 2 / k);
      ctx.beginPath(); ctx.moveTo(x1, y1); ctx.lineTo(x2, y2); ctx.stroke();
      ctx.setLineDash([]);
      var ah = Math.max(3.5 / k, 2.2);
      ctx.fillStyle = ctx.strokeStyle;
      ctx.beginPath();
      ctx.moveTo(x2 + ux * 2 / k, y2 + uy * 2 / k);
      ctx.lineTo(x2 - ux * ah - uy * ah * 0.5, y2 - uy * ah + ux * ah * 0.5);
      ctx.lineTo(x2 - ux * ah + uy * ah * 0.5, y2 - uy * ah - ux * ah * 0.5);
      ctx.closePath(); ctx.fill();
    });
    ctx.setLineDash([]);

    nodes.forEach(function (n) {
      if (!visible(n)) return;
      var base = dimmed && !emph(n) ? 0.15 : 1;
      ctx.globalAlpha = base;
      var col = C[n.kind] || C.ink2;
      ctx.beginPath(); ctx.arc(n.x, n.y, n.r, 0, Math.PI * 2);
      if (n.st === "abandoned") {
        ctx.fillStyle = C.bg; ctx.fill();
        ctx.lineWidth = 1.5 / Math.sqrt(k); ctx.strokeStyle = col; ctx.stroke();
      } else {
        ctx.fillStyle = col;
        if (n.st === "done") ctx.globalAlpha = base * 0.5;
        ctx.fill();
        ctx.globalAlpha = base;
        ctx.lineWidth = 1 / k; ctx.strokeStyle = C.stroke; ctx.stroke();
      }
      if (n.human) {
        ctx.beginPath(); ctx.arc(n.x, n.y, n.r + 2.2 / k, 0, Math.PI * 2);
        ctx.lineWidth = 1.2 / k; ctx.strokeStyle = C.ink2; ctx.stroke();
      }
    });
    if (focus && visible(focus)) {
      ctx.globalAlpha = 1;
      ctx.beginPath(); ctx.arc(focus.x, focus.y, focus.r + 4 / k, 0, Math.PI * 2);
      ctx.lineWidth = 3 / k; ctx.strokeStyle = C.hi; ctx.stroke();
    }
    ctx.globalAlpha = 1;
    if (k > 1.6) {
      ctx.font = 11 / k + 'px "DM Sans", sans-serif';
      ctx.textBaseline = "middle"; ctx.fillStyle = C.ink;
      nodes.forEach(function (n) {
        if (!visible(n) || (dimmed && !emph(n))) return;
        if (k < 2.6 && n.deg < 3) return;
        ctx.fillText(n.id, n.x + n.r + 3 / k, n.y);
      });
    }
  }

  // ---- hit testing, hover, selection ----
  function pick(px, py) {
    if (!qt) return null;
    var p = transform.invert([px, py]), x = p[0], y = p[1];
    var best = null, bd = Infinity;
    qt.visit(function (q, x0, y0, x1, y1) {
      if (!q.length) {
        do {
          var n = q.data;
          if (visible(n)) {
            var d = Math.hypot(n.x - x, n.y - y) - n.r;
            if (d < bd && d < 6 / transform.k) { bd = d; best = n; }
          }
        } while ((q = q.next));
      }
      var m = 20 / transform.k;
      return x0 > x + m || x1 < x - m || y0 > y + m || y1 < y - m;
    });
    return best;
  }
  function stateLabel(n) { return STATE_LABEL[n.state] || n.state; }
  function meta(n) {
    var bits = [n.kind === "task" ? "task" : n.kind, stateLabel(n)];
    if (n.kind === "task") bits.push(n.taskKind, n.priority + " priority");
    if (n.updated) bits.push("updated " + n.updated);
    return bits.map(esc).join(" &middot; ");
  }
  canvas.addEventListener("mousemove", function (e) {
    var n = pick(e.offsetX, e.offsetY);
    if (n !== hover) { hover = n; draw(); }
    canvas.style.cursor = n ? "pointer" : "";
    if (!n) { tip.hidden = true; return; }
    tip.hidden = false;
    tip.innerHTML = "<b>" + esc(n.id) + "</b> " + esc(n.title) + '<div class="meta">' + meta(n) + "</div>";
    var tw = tip.offsetWidth, th = tip.offsetHeight;
    tip.style.left = Math.max(4, Math.min(e.offsetX + 14, cw - tw - 8)) + "px";
    tip.style.top = (e.offsetY + 14 + th > ch ? e.offsetY - th - 10 : e.offsetY + 14) + "px";
  });
  canvas.addEventListener("mouseleave", function () { hover = null; tip.hidden = true; draw(); });
  canvas.addEventListener("click", function (e) { select(pick(e.offsetX, e.offsetY)); });

  function dot(n) {
    var s = document.createElement("span");
    s.className = "dot" + (n.st === "abandoned" ? " hollow" : n.st === "done" ? " done" : "");
    s.style.setProperty("--c", C[n.kind] || C.ink2);
    return s;
  }
  function item(n) {
    var li = document.createElement("li");
    li.dataset.id = n.id;
    li.appendChild(dot(n));
    var id = document.createElement("span"); id.className = "id"; id.textContent = n.id;
    var t = document.createElement("span"); t.className = "t"; t.title = n.title; t.textContent = n.title;
    li.appendChild(id); li.appendChild(t);
    return li;
  }
  function list(ns) {
    var ul = document.createElement("ul");
    ns.forEach(function (n) { ul.appendChild(item(n)); });
    return ul;
  }

  function select(n) {
    selected = n; draw();
    sel.innerHTML = "";
    var h = document.createElement("h2"); h.id = "graph-sel-h"; h.textContent = "Selected";
    sel.appendChild(h);
    if (!n) {
      var p = document.createElement("p"); p.className = "empty";
      p.textContent = "Hover a node to preview it. Click to pin it here with its neighbours.";
      sel.appendChild(p);
      return;
    }
    var idEl = document.createElement("div"); idEl.className = "id";
    var a = document.createElement("a"); a.href = n.url; a.textContent = n.id;
    idEl.appendChild(a);
    var title = document.createElement("div"); title.className = "title"; title.textContent = n.title;
    var dl = document.createElement("dl"); dl.className = "kv";
    var rows = [["Kind", n.kind === "task" ? n.taskKind || "task" : n.kind], ["Status", null]];
    if (n.kind === "task") rows.push(["Priority", n.priority], ["Actor", (n.actor || "unassigned") + (n.human ? " · human only" : "")]);
    rows.push(["Updated", n.updated || "unknown"], ["Links", String(n.deg)]);
    rows.forEach(function (r) {
      var dt = document.createElement("dt"); dt.textContent = r[0];
      var dd = document.createElement("dd");
      if (r[0] === "Status") {
        var pill = document.createElement("span"); pill.className = "pill"; pill.textContent = stateLabel(n);
        dd.appendChild(pill);
      } else dd.textContent = r[1];
      dl.appendChild(dt); dl.appendChild(dd);
    });
    sel.appendChild(idEl); sel.appendChild(title); sel.appendChild(dl);

    var groups = new Map();
    adj.get(n.id).forEach(function (l) {
      var out = l.source.id === n.id, other = out ? l.target : l.source;
      var label = out ? REL[l.p][0] : REL[l.p][2];
      if (!groups.has(label)) groups.set(label, []);
      groups.get(label).push(other);
    });
    if (!groups.size) {
      var none = document.createElement("p"); none.className = "empty"; none.textContent = "No links to anything else.";
      sel.appendChild(none);
      return;
    }
    groups.forEach(function (ns, label) {
      var h3 = document.createElement("h3");
      h3.textContent = label + " ";
      var c = document.createElement("span"); c.className = "count"; c.textContent = ns.length;
      h3.appendChild(c);
      sel.appendChild(h3); sel.appendChild(list(ns));
    });
  }
  root.querySelector(".graph-side").addEventListener("click", function (e) {
    var li = e.target.closest("li[data-id]");
    if (!li) return;
    var n = byId.get(li.dataset.id);
    if (!n) return;
    select(n); centerOn(n);
  });
  function centerOn(n) {
    var k = Math.max(transform.k, 2.2);
    d3.select(canvas)
      .transition().duration(matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 450)
      .call(zoom.transform, d3.zoomIdentity.translate(cw / 2 - k * n.x, ch / 2 - k * n.y).scale(k));
  }

  // ---- search, list, chips, stats, legend ----
  var q = el("graph-q");
  function runSearch() {
    var v = q.value.trim().toLowerCase();
    matches = v ? new Set(nodes.filter(function (n) {
      return visible(n) && (n.id.toLowerCase().indexOf(v) >= 0 || n.title.toLowerCase().indexOf(v) >= 0);
    }).map(function (n) { return n.id; })) : null;
    draw(); renderList();
  }
  q.addEventListener("input", runSearch);
  q.addEventListener("keydown", function (e) {
    if (e.key === "Enter" && matches && matches.size) {
      var n = byId.get(matches.values().next().value);
      select(n); centerOn(n);
    }
    if (e.key === "Escape") { q.value = ""; runSearch(); }
  });

  function renderList() {
    var vis = nodes.filter(function (n) { return visible(n) && (!matches || matches.has(n.id)); });
    vis.sort(function (a, b) { return b.deg - a.deg || a.id.localeCompare(b.id); });
    el("graph-list-count").textContent = vis.length + (vis.length > 80 ? ", top 80 by links" : "");
    var ul = el("graph-list");
    ul.innerHTML = "";
    vis.slice(0, 80).forEach(function (n) { ul.appendChild(item(n)); });
  }

  function chip(container, key, label, color, set, count) {
    var b = document.createElement("button");
    b.className = "graph-chip"; b.type = "button"; b.setAttribute("aria-pressed", "true");
    if (color) {
      var sw = document.createElement("span"); sw.className = "sw";
      sw.style.setProperty("--c", color);
      b.appendChild(sw);
    }
    b.appendChild(document.createTextNode(label + " "));
    var n = document.createElement("span"); n.className = "n"; n.textContent = count;
    b.appendChild(n);
    b.addEventListener("click", function () {
      if (set.has(key)) set.delete(key); else set.add(key);
      b.classList.toggle("off", !set.has(key));
      b.setAttribute("aria-pressed", String(set.has(key)));
      runSearch(); updateStats();
    });
    container.appendChild(b);
  }
  function buildChips() {
    KINDS.forEach(function (k) {
      var c = nodes.filter(function (n) { return n.kind === k[0]; }).length;
      if (c) chip(el("graph-kind-chips"), k[0], k[1], "var(" + k[2] + ")", on.kind, c);
    });
    STATES.forEach(function (s) {
      var c = nodes.filter(function (n) { return n.st === s[0]; }).length;
      if (c) chip(el("graph-state-chips"), s[0], s[1], null, on.state, c);
    });
    Object.keys(REL).forEach(function (p) {
      var c = links.filter(function (l) { return l.p === p; }).length;
      if (c) chip(el("graph-edge-chips"), p, REL[p][0], null, on.edge, c);
    });
  }
  function updateStats() {
    var vn = nodes.filter(visible), vl = links.filter(linkVisible);
    var stats = el("graph-stats");
    stats.innerHTML = "";
    [[vn.length, "shown"], [vl.length, "links"], [vn.filter(function (n) { return n.st === "open"; }).length, "open"]]
      .forEach(function (s) {
        var d = document.createElement("div"); d.className = "stat";
        var b = document.createElement("b"); b.textContent = s[0];
        d.appendChild(b); d.appendChild(document.createTextNode(s[1]));
        stats.appendChild(d);
      });
  }
  function buildLegend() {
    var line = function (dash) {
      return '<svg viewBox="0 0 34 12" aria-hidden="true"><line x1="1" y1="6" x2="27" y2="6" stroke="' + C.edge +
        '" stroke-width="1.5" stroke-dasharray="' + dash.join(" ") + '"/><path d="M26 2 L33 6 L26 10 Z" fill="' + C.edge + '"/></svg>';
    };
    var blob = function (fill, stroke, op) {
      return '<svg viewBox="0 0 34 12" aria-hidden="true"><circle cx="17" cy="6" r="4.5" fill="' + fill +
        '" stroke="' + stroke + '" stroke-width="1.5" opacity="' + op + '"/></svg>';
    };
    var html = '<div class="h">Node</div>' +
      '<div class="row">' + blob(C.ink2, "none", 1) + "Open, size by link count</div>" +
      '<div class="row">' + blob(C.ink2, "none", 0.5) + "Merged, deployed or accepted</div>" +
      '<div class="row">' + blob("none", C.ink2, 1) + "Abandoned, superseded or withdrawn</div>" +
      '<div class="row"><svg viewBox="0 0 34 12" aria-hidden="true"><circle cx="17" cy="6" r="3.5" fill="' + C.ink2 +
      '"/><circle cx="17" cy="6" r="5.5" fill="none" stroke="' + C.ink2 + '" stroke-width="1"/></svg>Human only</div>' +
      '<div class="h">Link, arrow at the target</div>';
    Object.keys(REL).forEach(function (p) {
      if (!links.some(function (l) { return l.p === p; })) return;
      html += '<div class="row">' + line(REL[p][1]) + esc(REL[p][0]) + "</div>";
    });
    el("graph-legend").innerHTML = html;
  }
})();
