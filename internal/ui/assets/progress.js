// progress.js drives the Progress page (WL-SPEC-66 §2.3, §2.4): it expands a
// spec row, and it owns the page's tooltips.
//
// Expansion is one class. Every row's detail block is already in the document;
// putting "open" on the row is what the stylesheet reads to show it, so
// expanding costs no request and no markup. A click that lands on a link is
// left alone — the reference is the row's only link and it has to stay one.
//
// An action is a two-step button (WL-SPEC-66 §3.1). The first click never
// writes: it swaps the button's own content for the act named in full with
// Confirm and Cancel, at the width measured before the swap, so nothing on
// the page moves. Cancel, Escape, or a click elsewhere puts the button back.
// Confirm posts through the page-script write gate (§4.2) and the button is
// busy until the reply. The confirmation step is a mis-click guard and
// nothing more; what stands between a hostile page and a write is that gate.
//
// The page follows its project's event stream (WL-SPEC-66 §5.1) and redraws
// from server-rendered fragments: a frame naming a spec swaps that spec's row
// and detail, and any frame refreshes the band, counts, bar and footer. The
// page derives nothing from a frame — it re-reads what the server renders — so
// a swapped row can never disagree with a reloaded page.
//
// A tooltip is one <div id="tip"> reused for every element carrying data-tip,
// positioned from the hovered element's rect. It appears after 150ms so a
// pointer crossing the strip does not flash a tooltip per cell, and it never
// captures the pointer while hovering. A click pins it: a pinned tooltip stays
// until the next click or Escape, and only a pinned one takes the pointer, so
// a link inside one (part 4's PR link) can never be hit by a moving pointer.
// Served at /assets/progress.js by internal/api's assetHandler; a static
// asset, not a generated artifact, so it carries no drift-check surface.
(function () {
  var rows = document.querySelectorAll(".prog-row");
  if (!rows.length) return;

  function toggle(row) {
    var open = row.classList.toggle("open");
    row.setAttribute("aria-expanded", open ? "true" : "false");
  }

  // An action button sits inside the row it acts on, so both row handlers
  // step aside for one; otherwise pressing it would also expand the row.
  function bindRow(row) {
    row.addEventListener("click", function (e) {
      if (e.target.closest("a") || e.target.closest(".act")) return;
      toggle(this);
    });
    row.addEventListener("keydown", function (e) {
      if (e.key !== "Enter" && e.key !== " ") return;
      if (e.target.closest(".act")) return;
      e.preventDefault();
      toggle(this);
    });
  }

  for (var i = 0; i < rows.length; i++) bindRow(rows[i]);

  var tip = document.createElement("div");
  tip.id = "tip";
  tip.setAttribute("role", "tooltip");
  tip.hidden = true;
  document.body.appendChild(tip);

  var timer = null;
  var pinned = false;
  var pinnedEl = null; // the element a pinned tooltip came from, re-found after a swap

  function show(el) {
    tip.textContent = el.getAttribute("data-tip");
    tip.hidden = false;
    var r = el.getBoundingClientRect();
    var box = tip.getBoundingClientRect();
    var left = Math.min(Math.max(4, r.left), window.innerWidth - box.width - 4);
    var top = r.top - box.height - 6;
    if (top < 4) top = r.bottom + 6;
    tip.style.left = Math.max(4, left) + "px";
    tip.style.top = top + "px";
  }

  function pin(el) {
    show(el);
    pinned = true;
    pinnedEl = el;
    tip.setAttribute("data-pinned", "");
  }

  function hide() {
    clearTimeout(timer);
    pinned = false;
    pinnedEl = null;
    tip.removeAttribute("data-pinned");
    tip.hidden = true;
  }

  document.addEventListener("mouseover", function (e) {
    if (pinned) return;
    var el = e.target.closest("[data-tip]");
    if (!el) return;
    clearTimeout(timer);
    timer = setTimeout(function () { show(el); }, 150);
  });

  document.addEventListener("mouseout", function (e) {
    if (pinned || !e.target.closest("[data-tip]")) return;
    clearTimeout(timer);
    tip.hidden = true;
  });

  document.addEventListener("click", function (e) {
    if (tip.contains(e.target)) return;
    var el = e.target.closest("[data-tip]");
    hide();
    if (!el) return;
    pin(el);
  });

  document.addEventListener("keydown", function (e) {
    if (e.key !== "Escape") return;
    hide();
    restore(true);
  });

  // --- actions (§3.1) -------------------------------------------------------

  var armed = null; // the button currently showing its confirmation
  var sending = null; // the button whose write is in flight; every button ignores clicks

  // arm turns a button into its confirmation in place. The width is pinned to
  // what the button measured as a button, and the confirmation is laid over
  // it, so the row keeps its geometry through both steps (§5.4).
  function arm(a) {
    a.dataset.label = a.textContent;
    a.style.width = a.getBoundingClientRect().width + "px";
    a.textContent = "";

    var box = document.createElement("span");
    box.className = "confirm-box";
    var question = document.createElement("span");
    question.textContent = (a.getAttribute("data-confirm") || "Confirm") + "?";
    box.appendChild(question);
    box.appendChild(stepButton("confirm", "Confirm"));
    box.appendChild(stepButton("cancel", "Cancel"));
    a.appendChild(box);
    a.setAttribute("aria-expanded", "true");

    armed = a;
    box.querySelector(".confirm").focus();
  }

  function stepButton(cls, label) {
    var b = document.createElement("button");
    b.type = "button";
    b.className = cls;
    b.textContent = label;
    return b;
  }

  function restore(refocus) {
    if (!armed || sending) return;
    var a = armed;
    armed = null;
    a.textContent = a.dataset.label;
    a.style.width = "";
    a.removeAttribute("aria-expanded");
    if (refocus) a.focus();
  }

  // send is the write itself: the three things beginJSONPost requires (POST,
  // a JSON body, the page's header) and nothing else. The page applies no
  // result of its own — the server records the change and the row is read
  // back from the backbone.
  function send(a) {
    sending = a;
    a.setAttribute("aria-busy", "true");
    var done = function (msg) {
      // A stream swap may have replaced the button mid-flight; sending points
      // at whichever node is on the page now, so the reply lands on it.
      var el = sending;
      sending = null;
      if (el) el.removeAttribute("aria-busy");
      restore(true);
      if (msg && el) showError(el, msg);
    };
    fetch(base() + "/" + a.getAttribute("data-route"), {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-Requested-With": "lode-cockpit" },
      body: a.getAttribute("data-body") || "{}"
    }).then(function (res) {
      return res.text().then(function (text) {
        var reply = {};
        try { reply = JSON.parse(text); } catch (err) { /* a reply the page cannot read */ }
        if (!res.ok) {
          done(reply.error || "the request was refused");
          return;
        }
        done("");
        // Nothing else: the stream re-renders the row from the backbone.
      });
    }, function () {
      done("the request could not be sent");
    });
  }

  // showError puts the server's reason beside the button for five seconds,
  // where the person who pressed it is already looking.
  function showError(a, msg) {
    var note = document.createElement("span");
    note.className = "act-err";
    note.setAttribute("role", "status");
    note.textContent = msg;
    a.parentNode.insertBefore(note, a.nextSibling);
    setTimeout(function () {
      if (note.parentNode) note.parentNode.removeChild(note);
    }, 5000);
  }

  // --- live updates (§5) ---------------------------------------------------

  // Every route this page reads or writes hangs off the page's own path, so
  // one base is all the script needs to know about where it is.
  function base() { return location.pathname.replace(/\/+$/, ""); }

  function get(url) {
    return fetch(url, { credentials: "same-origin" }).then(function (res) {
      return res.ok ? res.text() : null;
    }, function () { return null; });
  }

  function parse(html) { return new DOMParser().parseFromString(html, "text/html"); }

  function rowFor(doc) { return document.querySelector('.prog-row[data-doc="' + doc + '"]'); }

  function groupBody(key) {
    var g = key && document.querySelector('section[data-group="' + key + '"]');
    return g && g.querySelector(".bd");
  }

  // Frames arrive one per backbone event, so a single transition can produce
  // several in a row. A burst is coalesced into one fetch per named spec and
  // one summary fetch. wantTasks carries along which task(s) named a given
  // spec in the burst, so the row that spec swaps into can pulse them (§5.3).
  var wantRows = {};
  var wantTasks = {};
  var wantSummary = false;
  var flushTimer = null;

  function schedule(docs, task) {
    for (var i = 0; i < docs.length; i++) {
      var doc = String(docs[i]);
      if (!/^\d+$/.test(doc)) continue;
      wantRows[doc] = true;
      if (task) {
        wantTasks[doc] = wantTasks[doc] || {};
        wantTasks[doc][task] = true;
      }
    }
    wantSummary = true;
    if (flushTimer === null) flushTimer = setTimeout(flush, 150);
  }

  function flush() {
    flushTimer = null;
    var docs = Object.keys(wantRows);
    wantRows = {};
    for (var i = 0; i < docs.length; i++) {
      var tasks = wantTasks[docs[i]] ? Object.keys(wantTasks[docs[i]]) : [];
      delete wantTasks[docs[i]];
      refreshRow(docs[i], tasks);
    }
    if (!wantSummary) return;
    wantSummary = false;
    get(base() + "/summary").then(function (html) {
      if (html !== null) swapSummary(html);
    });
  }

  function refreshRow(doc, tasks) {
    get(base() + "/spec/" + doc).then(function (html) {
      if (html !== null) swapRow(doc, html, tasks);
    });
  }

  // swapRow replaces one spec's row and detail with the server's fragment,
  // carrying over what the reader had done to them: the row's expansion, a
  // pinned tooltip's target, and a confirmation the row is in the middle of.
  // A spec the page does not show yet joins the end of its group instead.
  // tasks names which task(s) an event just named for this spec, so the
  // fresh row can flash them (§5.3); it is empty on a summary-only refresh.
  function swapRow(doc, html, tasks) {
    var frag = parse(html);
    var row = frag.querySelector(".prog-row");
    var detail = frag.querySelector(".detail");
    if (!row || !detail) return;

    var oldRow = rowFor(doc);
    if (!oldRow) {
      appendRow(row, detail);
      touch(row, detail, tasks);
      return;
    }
    var oldDetail = document.getElementById(oldRow.getAttribute("aria-controls"));
    if (!oldDetail) return;

    if (oldRow.classList.contains("open")) {
      row.classList.add("open");
      row.setAttribute("aria-expanded", "true");
    }
    var tipWas = carriedTip(oldRow, oldDetail);
    var actWas = carriedAct(oldRow, oldDetail);

    oldRow.parentNode.replaceChild(row, oldRow);
    oldDetail.parentNode.replaceChild(detail, oldDetail);
    bindRow(row);
    restoreTip(row, detail, tipWas);
    restoreAct(row, detail, actWas);
    touch(row, detail, tasks);

    // The swap is always in place; only the move between groups waits for the
    // pointer to leave the list (§5.4 rule 3).
    var holder = row.closest("section[data-group]");
    if (holder && holder.getAttribute("data-group") !== row.getAttribute("data-group")) {
      if (overList) deferred[doc] = true;
      else moveRow(doc);
    }
  }

  // touch is §5.3's activity feedback: the row that just swapped flashes
  // along its left edge, and any task cell a frame named pulses within it.
  // Both classes come off on animationend (app.tailwind.css keeps that event
  // firing under prefers-reduced-motion too, just with no motion in it), so a
  // stalled removal cannot leave a cell stuck flashing.
  function touch(row, detail, tasks) {
    flash(row, "touched");
    for (var i = 0; i < (tasks || []).length; i++) {
      var cell = detail.querySelector('[data-task="' + tasks[i] + '"]');
      if (cell) flash(cell, "pulse");
    }
  }

  // flash (re)starts one CSS animation-driven class on el. The remove/reflow/
  // add sequence restarts the animation even if el was still mid-flash from a
  // previous event.
  function flash(el, cls) {
    el.classList.remove(cls);
    void el.offsetWidth;
    el.classList.add(cls);
    el.addEventListener("animationend", function handler(e) {
      if (e.target !== el) return;
      el.classList.remove(cls);
      el.removeEventListener("animationend", handler);
    });
  }

  // A pinned tooltip is re-found in the fresh markup by what it was about: a
  // task cell by its task, a section cell by its anchor. Anything else pinned
  // inside the swapped row has no stable identity and the tooltip closes.
  function carriedTip(row, detail) {
    if (!pinned || !pinnedEl) return null;
    if (!row.contains(pinnedEl) && !detail.contains(pinnedEl)) return null;
    var task = pinnedEl.getAttribute("data-task");
    if (task) return '[data-task="' + task + '"]';
    var anchor = pinnedEl.getAttribute("data-anchor");
    return anchor ? '[data-anchor="' + anchor + '"]' : "";
  }

  function restoreTip(row, detail, sel) {
    if (sel === null) return;
    hide();
    var el = sel && (row.querySelector(sel) || detail.querySelector(sel));
    if (el) pin(el);
  }

  // An armed or in-flight action is matched in the fresh markup by what it
  // would post: the same route and the same body is the same act.
  function carriedAct(row, detail) {
    if (!armed) return null;
    if (!row.contains(armed) && !detail.contains(armed)) return null;
    return {
      route: armed.getAttribute("data-route"),
      body: armed.getAttribute("data-body") || "",
      inflight: sending === armed
    };
  }

  function restoreAct(row, detail, want) {
    if (!want) return;
    armed = null;
    var acts = [].slice.call(row.querySelectorAll(".act"));
    acts = acts.concat([].slice.call(detail.querySelectorAll(".act")));
    var found = null;
    for (var i = 0; i < acts.length && !found; i++) {
      if (acts[i].getAttribute("data-route") === want.route &&
        (acts[i].getAttribute("data-body") || "") === want.body) found = acts[i];
    }
    if (!found) {
      sending = null;
      return;
    }
    arm(found);
    if (!want.inflight) return;
    sending = found;
    found.setAttribute("aria-busy", "true");
  }

  function appendRow(row, detail) {
    var body = groupBody(row.getAttribute("data-group"));
    if (!body) {
      reloadWhenIdle();
      return;
    }
    body.appendChild(row);
    body.appendChild(detail);
    bindRow(row);
    recount();
  }

  // moveRow re-parents an existing row and its detail, which keeps their
  // handlers and the "open" class they already carry.
  function moveRow(doc) {
    var row = rowFor(doc);
    if (!row) return;
    var detail = document.getElementById(row.getAttribute("aria-controls"));
    var body = groupBody(row.getAttribute("data-group"));
    if (!detail || !body) {
      reloadWhenIdle();
      return;
    }
    body.appendChild(row);
    body.appendChild(detail);
    recount();
  }

  // A group heading carries its size, and the server renders no group that is
  // empty, so an emptied one goes away rather than heading nothing.
  function recount() {
    var groups = document.querySelectorAll("section[data-group]");
    for (var i = 0; i < groups.length; i++) {
      var n = groups[i].querySelectorAll(".prog-row").length;
      var slot = groups[i].querySelector(".hd h3 .n");
      if (slot) slot.textContent = String(n);
      groups[i].hidden = n === 0;
    }
  }

  // A group the server would have to draw from scratch is the one case the
  // page cannot assemble itself. Re-reading the page is the honest answer, and
  // it waits for the pointer to leave the list like every other move.
  function reloadWhenIdle() {
    if (overList) pendingReload = true;
    else location.reload();
  }

  // §5.2's second fragment. Each piece is replaced by its content, never by
  // its container: the band, the counts and the footer hold §5.4's fixed
  // heights, and the reconnect note lives in the band.
  function swapSummary(html) {
    var frag = parse(html);
    var parts = [".prog-band", ".prog-counts", 'section[aria-labelledby="prog-bar-h"]', ".prog-footer"];
    for (var i = 0; i < parts.length; i++) {
      var fresh = frag.querySelector(parts[i]);
      var cur = document.querySelector(parts[i]);
      if (!fresh || !cur) continue;
      cur.innerHTML = fresh.innerHTML;
      if (fresh.hasAttribute("aria-hidden")) cur.setAttribute("aria-hidden", "true");
      else cur.removeAttribute("aria-hidden");
    }
    placeNote();
  }

  var note = document.createElement("span");
  note.className = "prog-recon muted small";
  note.setAttribute("role", "status");

  function placeNote() {
    var band = document.querySelector(".prog-band");
    if (band) band.appendChild(note);
  }

  placeNote();

  // Moving a row between groups is the one redraw that would shift the list
  // under the pointer, so the list says when it is being pointed at.
  var overList = false;
  var deferred = {};
  var pendingReload = false;

  var list = document.querySelector(".prog-groups");
  if (list) {
    list.addEventListener("mouseenter", function () { overList = true; });
    list.addEventListener("mouseleave", function () {
      overList = false;
      var docs = Object.keys(deferred);
      deferred = {};
      for (var i = 0; i < docs.length; i++) moveRow(docs[i]);
      if (pendingReload) location.reload();
    });
  }

  // The stream itself (§5.1). EventSource reconnects on its own and resumes
  // from Last-Event-ID, so a drop needs no retry logic here: only the note in
  // the band, and the one full refresh that follows the reconnect (§5.2).
  var dropped = false;
  var stream = new EventSource(base() + "/events");

  stream.addEventListener("progress", function (e) {
    var frame;
    try { frame = JSON.parse(e.data); } catch (err) { return; }
    schedule(frame.specs || [], frame.task);
  });

  stream.addEventListener("open", function () {
    if (!dropped) return;
    dropped = false;
    note.textContent = "";
    var all = document.querySelectorAll(".prog-row[data-doc]");
    var docs = [];
    for (var i = 0; i < all.length; i++) docs.push(all[i].getAttribute("data-doc"));
    schedule(docs);
  });

  stream.addEventListener("error", function () {
    dropped = true;
    note.textContent = "reconnecting…";
  });

  document.addEventListener("click", function (e) {
    var a = e.target.closest("button.act");
    if (!a) {
      restore(false);
      return;
    }
    if (a.disabled || sending) return;
    if (e.target.closest(".cancel")) {
      restore(true);
      return;
    }
    if (e.target.closest(".confirm")) {
      send(a);
      return;
    }
    if (a !== armed) {
      restore(false);
      arm(a);
    }
  });
})();
