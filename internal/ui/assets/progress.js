// progress.js drives the Progress page (WL-SPEC-66 §2.3, §2.4): it expands a
// spec row, and it owns the page's tooltips.
//
// Expansion is one class. Every row's detail block is already in the document;
// putting "open" on the row is what the stylesheet reads to show it, so
// expanding costs no request and no markup. A click that lands on a link is
// left alone — the reference is the row's only link and it has to stay one.
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

  for (var i = 0; i < rows.length; i++) {
    rows[i].addEventListener("click", function (e) {
      if (e.target.closest("a")) return;
      toggle(this);
    });
    rows[i].addEventListener("keydown", function (e) {
      if (e.key !== "Enter" && e.key !== " ") return;
      e.preventDefault();
      toggle(this);
    });
  }

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
    if (e.key === "Escape") hide();
  });
})();
