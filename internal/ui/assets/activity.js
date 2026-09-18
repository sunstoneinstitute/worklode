// activity.js follows one task's activity log (WL-SPEC-71 §4). Each frame's
// data is the <li> the server rendered, so the page inserts it verbatim and
// builds no markup of its own — an inserted row can never disagree with a
// reloaded one.
//
// EventSource reconnects on its own and resumes from Last-Event-ID, so a
// dropped connection needs no retry logic here.
//
// The list is capped at 500 rows: a long-running agent produces more activity
// than a page has any use for, and the oldest rows are the ones already read.
// Served at /assets/activity.js by internal/api's assetHandler.
(function () {
  var card = document.getElementById("activity");
  if (!card) return;
  var list = card.querySelector("ol.activity");
  if (!list) return;

  // The card names its own task, so the stream's URL does not depend on how
  // the page was reached.
  var task = card.getAttribute("data-task");
  if (!task) return;
  var stream = new EventSource("/tasks/" + encodeURIComponent(task) + "/activity/events");

  stream.addEventListener("activity", function (e) {
    list.insertAdjacentHTML("afterbegin", e.data);
    // The card renders an honest "nothing yet" line while the log is empty;
    // the first row makes it untrue.
    var empty = card.querySelector(".activity-empty");
    if (empty) empty.remove();
    while (list.children.length > 500) list.removeChild(list.lastElementChild);
  });
})();
