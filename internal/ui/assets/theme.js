// theme.js toggles the cockpit's light/dark theme by stamping data-theme on
// <html>. On the first click it seeds from the OS colour-scheme, then flips.
// The stylesheet's :root[data-theme="…"] blocks (see styles/app.tailwind.css)
// win over the prefers-color-scheme default once the attribute is set.
// Served at /assets/theme.js by internal/api's assetHandler; a static asset,
// not a generated artifact, so it carries no drift-check surface.
(function () {
  var toggle = document.getElementById("theme");
  if (!toggle) return;
  toggle.addEventListener("click", function () {
    var root = document.documentElement;
    var cur = root.getAttribute("data-theme");
    if (!cur) {
      cur = matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
    }
    root.setAttribute("data-theme", cur === "dark" ? "light" : "dark");
  });
})();
