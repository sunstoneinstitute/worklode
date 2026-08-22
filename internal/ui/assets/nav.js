// nav.js wires the two mobile-only sheet toggles added for the GitHub-app
// style bottom navigation (layout.templ's header comment): the global nav's
// "More" tab and the project drawer's menu button. Both panels are the same
// landmark elements the desktop sidebar renders — never a duplicate copy —
// so all this script does is flip one attribute, body[data-open-panel], that
// the mobile media query in styles/app.tailwind.css reads to show/hide them,
// plus keep aria-expanded and focus in sync. Desktop never shows either
// trigger button, so this script is inert above 880px.
// Served at /assets/nav.js by internal/api's assetHandler; a static asset,
// not a generated artifact, so it carries no drift-check surface.
(function () {
  var body = document.body;
  var backdrop = document.querySelector(".sheet-backdrop");
  var toggles = [
    { button: document.getElementById("more-toggle"), panel: "more" },
    { button: document.getElementById("drawer-toggle"), panel: "drawer" },
  ].filter(function (t) {
    return !!t.button;
  });

  function panelFor(name) {
    return document.getElementById(name === "more" ? "global-nav" : "project-local-nav");
  }

  function close() {
    var openPanel = body.getAttribute("data-open-panel");
    if (!openPanel) return;
    body.removeAttribute("data-open-panel");
    toggles.forEach(function (t) {
      t.button.setAttribute("aria-expanded", "false");
      if (t.panel === openPanel) t.button.focus();
    });
  }

  function open(t) {
    if (body.getAttribute("data-open-panel")) close();
    body.setAttribute("data-open-panel", t.panel);
    t.button.setAttribute("aria-expanded", "true");
    var panel = panelFor(t.panel);
    var firstLink = panel && panel.querySelector("a");
    if (firstLink) firstLink.focus();
  }

  toggles.forEach(function (t) {
    t.button.addEventListener("click", function () {
      if (body.getAttribute("data-open-panel") === t.panel) {
        close();
      } else {
        open(t);
      }
    });
  });

  if (backdrop) backdrop.addEventListener("click", close);
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") close();
  });
})();
