// act.js drives the two-step action buttons (WL-SPEC-66 §3.1) outside the
// Progress page: the document page's Accept and Publish and the task page's
// Publish. The markup and write gate are progress.js's; what differs is that
// data-route is an absolute path, a disabled button's reason is shown as its
// native title, and a successful write reloads the page, since these pages
// follow no event stream.
// Served at /assets/act.js by internal/api's assetHandler.
(function () {
  var armed = null;
  var sending = false;

  var disabled = document.querySelectorAll("button.act[data-tip]");
  for (var i = 0; i < disabled.length; i++) disabled[i].title = disabled[i].getAttribute("data-tip");

  function arm(a) {
    a.dataset.label = a.textContent;
    a.style.width = a.getBoundingClientRect().width + "px";
    a.textContent = "";
    var box = document.createElement("span");
    box.className = "confirm-box";
    var q = document.createElement("span");
    q.textContent = (a.getAttribute("data-confirm") || "Confirm") + "?";
    box.appendChild(q);
    box.appendChild(step("confirm", "Confirm"));
    box.appendChild(step("cancel", "Cancel"));
    a.appendChild(box);
    a.setAttribute("aria-expanded", "true");
    armed = a;
    box.querySelector(".confirm").focus();
  }

  function step(cls, label) {
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

  function send(a) {
    sending = true;
    a.setAttribute("aria-busy", "true");
    var fail = function (msg) {
      sending = false;
      a.removeAttribute("aria-busy");
      restore(true);
      var note = document.createElement("span");
      note.className = "act-err";
      note.setAttribute("role", "status");
      note.textContent = " " + msg;
      a.parentNode.insertBefore(note, a.nextSibling);
      setTimeout(function () { if (note.parentNode) note.parentNode.removeChild(note); }, 5000);
    };
    fetch(a.getAttribute("data-route"), {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-Requested-With": "lode-cockpit" },
      body: a.getAttribute("data-body") || "{}"
    }).then(function (res) {
      if (res.ok) {
        location.reload();
        return;
      }
      return res.text().then(function (text) {
        var reply = {};
        try { reply = JSON.parse(text); } catch (err) { /* a reply the page cannot read */ }
        fail(reply.error || "the request was refused");
      });
    }, function () {
      fail("the request could not be sent");
    });
  }

  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") restore(true);
  });

  document.addEventListener("click", function (e) {
    var a = e.target.closest("button.act");
    if (!a) {
      restore(false);
      return;
    }
    if (a.disabled || sending) return;
    if (e.target.closest(".cancel")) restore(true);
    else if (e.target.closest(".confirm")) send(a);
    else if (a !== armed) {
      restore(false);
      arm(a);
    }
  });
})();
