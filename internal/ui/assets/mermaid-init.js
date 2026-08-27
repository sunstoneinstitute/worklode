// mermaid-init.js starts mermaid.js once it and the page's pre.mermaid
// blocks (mdrender's client-side render mode) are both in the DOM. A second
// file, not inlined into mermaid.min.js, because that bundle is vendored
// as-is; keeping the one line worklode owns separate is what makes future
// version bumps a straight file replacement.
// Served at /assets/mermaid-init.js by internal/api's assetHandler.
(function () {
  if (window.mermaid) mermaid.initialize({ startOnLoad: true });
})();
