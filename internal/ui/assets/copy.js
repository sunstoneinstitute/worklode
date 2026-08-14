// copy.js wires every [data-copy="<selector>"] button to copy the text of the
// element it points at, briefly swapping its label for data-copied. Used by the
// CLI-code page (spec 001 §8.7).
//
// navigator.clipboard is unavailable outside a secure context, which is exactly
// where this page often runs — a worklode reached over plain http on a LAN or
// in compose. The fallback selects the text so the user can copy it with the
// keyboard rather than being left with a button that silently does nothing.
// Served at /assets/copy.js by internal/api's assetHandler; a static asset,
// not a generated artifact, so it carries no drift-check surface.
(function () {
  function flash(btn) {
    var done = btn.getAttribute("data-copied");
    if (!done) return;
    var was = btn.textContent;
    btn.textContent = done;
    setTimeout(function () { btn.textContent = was; }, 1500);
  }

  function select(el) {
    var range = document.createRange();
    range.selectNodeContents(el);
    var sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
  }

  var buttons = document.querySelectorAll("[data-copy]");
  for (var i = 0; i < buttons.length; i++) {
    buttons[i].addEventListener("click", function () {
      var btn = this;
      var target = document.querySelector(btn.getAttribute("data-copy"));
      if (!target) return;
      var text = target.textContent.trim();
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(text).then(function () { flash(btn); }, function () { select(target); });
      } else {
        select(target);
      }
    });
  }
})();
