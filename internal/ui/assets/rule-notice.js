// rule-notice.js (WL-REQ-1809): a document page opened at #rule-<n> for a rule
// it does not arrange says so at the top and links to the rule's own page.
(function () {
  var box = document.getElementById("rule-notice");
  if (!box) return;
  function show() {
    box.hidden = true;
    box.textContent = "";
    var m = /^#rule-(\d+)$/.exec(location.hash);
    if (!m || document.getElementById("rule-" + m[1])) return;
    var key = box.getAttribute("data-key");
    box.append("This document does not arrange rule " + m[1] + ".");
    if (key) {
      var ref = key + "-RULE-" + m[1];
      var a = document.createElement("a");
      a.href = "/" + ref;
      a.textContent = ref;
      box.append(" See ", a, ".");
    }
    box.hidden = false;
    window.scrollTo(0, 0);
  }
  show();
  window.addEventListener("hashchange", show);
})();
