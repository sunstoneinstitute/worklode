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

  function hide() {
    clearTimeout(timer);
    pinned = false;
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
    show(el);
    pinned = true;
    tip.setAttribute("data-pinned", "");
  });

  document.addEventListener("keydown", function (e) {
    if (e.key !== "Escape") return;
    hide();
    restore(true);
  });

  // --- actions (§3.1) -------------------------------------------------------

  var armed = null; // the button currently showing its confirmation
  var busy = false; // a write is in flight; every button ignores clicks

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
    if (!armed || busy) return;
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
    busy = true;
    a.setAttribute("aria-busy", "true");
    var done = function (msg) {
      busy = false;
      a.removeAttribute("aria-busy");
      restore(true);
      if (msg) showError(a, msg);
    };
    fetch(location.pathname.replace(/\/+$/, "") + "/" + a.getAttribute("data-route"), {
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
        refreshRow(a);
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

  // TODO(part 3): GET /projects/{id}/progress/spec/{doc} serves the row and
  // its detail as a fragment (§5.2), and the event stream swaps it in. Until
  // that route exists the page re-reads itself and lifts the one row out,
  // which is the same swap over a much larger response.
  function refreshRow(a) {
    var detail = a.closest(".detail");
    var row = a.closest(".prog-row") || (detail && detail.previousElementSibling);
    var id = row && row.getAttribute("aria-controls");
    if (!id) {
      location.reload();
      return;
    }
    fetch(location.href, { credentials: "same-origin" }).then(function (res) {
      return res.text();
    }).then(function (html) {
      var page = new DOMParser().parseFromString(html, "text/html");
      var freshRow = page.querySelector('.prog-row[aria-controls="' + id + '"]');
      var freshDetail = page.getElementById(id);
      var oldRow = document.querySelector('.prog-row[aria-controls="' + id + '"]');
      var oldDetail = document.getElementById(id);
      if (!freshRow || !freshDetail || !oldRow || !oldDetail) {
        location.reload();
        return;
      }
      if (oldRow.classList.contains("open")) {
        freshRow.classList.add("open");
        freshRow.setAttribute("aria-expanded", "true");
      }
      oldRow.parentNode.replaceChild(freshRow, oldRow);
      oldDetail.parentNode.replaceChild(freshDetail, oldDetail);
      bindRow(freshRow);
    }, function () { location.reload(); });
  }

  document.addEventListener("click", function (e) {
    var a = e.target.closest("button.act");
    if (!a) {
      restore(false);
      return;
    }
    if (a.disabled || busy) return;
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
