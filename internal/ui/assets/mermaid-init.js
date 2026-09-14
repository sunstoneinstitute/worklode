// mermaid-init.js draws the page's ```mermaid blocks (mdrender renders them
// client-side, as pre.mermaid) in the cockpit's own palette.
//
// Two things here are not optional. First, mermaid ships its generated
// stylesheet as a <style> element inside each SVG, and the page CSP's
// style-src 'self' refuses it — so the rules are copied into a constructed
// CSSStyleSheet, which CSSOM builds and CSP does not police. The same refusal
// hits the style="" attributes mermaid writes, re-applied through .style the
// same way. Without both, every shape falls back to SVG's default black fill.
//
// Second, the colours come from the cockpit's custom properties rather than
// from a literal palette here, so a diagram tracks the light/dark theme like
// the rest of the page. Rendering is what reads them, which is why the theme
// button re-draws rather than re-colouring in place.
//
// Served at /assets/mermaid-init.js by internal/api's assetHandler.
(function () {
  if (!window.mermaid) return;

  var blocks = [].slice.call(document.querySelectorAll("pre.mermaid"));
  if (!blocks.length) return;

  // The source, before mermaid replaces each block with its SVG.
  var sources = blocks.map(function (el) { return el.textContent; });
  var adopted = [];

  function token(cs, name) { return cs.getPropertyValue(name).trim(); }

  function themeVariables() {
    var cs = getComputedStyle(document.documentElement);
    var body = getComputedStyle(document.body);
    return {
      background: token(cs, "--bg"),
      fontFamily: body.fontFamily,
      fontSize: body.fontSize,

      primaryColor: token(cs, "--surface"),
      primaryBorderColor: token(cs, "--link"),
      primaryTextColor: token(cs, "--ink"),
      secondaryColor: token(cs, "--surface-2"),
      secondaryBorderColor: token(cs, "--line-2"),
      secondaryTextColor: token(cs, "--ink"),
      tertiaryColor: token(cs, "--sunk"),
      tertiaryBorderColor: token(cs, "--line-2"),
      tertiaryTextColor: token(cs, "--ink-2"),

      mainBkg: token(cs, "--surface"),
      nodeBorder: token(cs, "--link"),
      nodeTextColor: token(cs, "--ink"),
      textColor: token(cs, "--ink"),
      titleColor: token(cs, "--ink"),
      lineColor: token(cs, "--ink-3"),
      edgeLabelBackground: token(cs, "--bg"),
      clusterBkg: token(cs, "--sunk"),
      clusterBorder: token(cs, "--line-2"),
      noteBkgColor: token(cs, "--accent"),
      noteTextColor: token(cs, "--accent-ink"),
      noteBorderColor: token(cs, "--accent-line"),
      errorBkgColor: token(cs, "--crit-bg"),
      errorTextColor: token(cs, "--crit")
    };
  }

  // Copy what CSP refused into channels it does not police: a constructed
  // stylesheet for the <style> elements, the CSSOM for the attributes. Scoped
  // to the diagrams, so the page's own inline icon SVGs are left alone.
  function applyBlockedStyles() {
    var sheets = [];
    blocks.forEach(function (block) {
      block.querySelectorAll("style").forEach(function (el) {
        try {
          var sheet = new CSSStyleSheet();
          sheet.replaceSync(el.textContent);
          sheets.push(sheet);
        } catch (e) { /* no constructed stylesheets: diagram stays unstyled */ }
      });
      block.querySelectorAll("[style]").forEach(function (el) {
        el.style.cssText = el.getAttribute("style");
      });
    });
    document.adoptedStyleSheets = document.adoptedStyleSheets
      .filter(function (s) { return adopted.indexOf(s) < 0; })
      .concat(sheets);
    adopted = sheets;
  }

  function draw() {
    blocks.forEach(function (el, i) {
      el.removeAttribute("data-processed");
      el.textContent = sources[i];
    });
    // securityLevel is mermaid's current default, pinned so a bundle bump
    // cannot quietly start trusting corpus-authored label markup.
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: "strict",
      theme: "base",
      themeVariables: themeVariables()
    });
    // Style whatever was drawn, including the diagram mermaid renders in place
    // of one it could not parse.
    mermaid.run({ nodes: blocks }).catch(function () {}).then(applyBlockedStyles);
  }

  draw();

  // theme.js stamps data-theme in its own click handler; the timeout is what
  // puts this read after that write, whichever listener registered first.
  var toggle = document.getElementById("theme");
  if (toggle) toggle.addEventListener("click", function () { setTimeout(draw, 0); });
})();
