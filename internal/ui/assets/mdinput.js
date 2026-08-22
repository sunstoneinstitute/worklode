// mdinput.js — behaviour for the MarkdownInput component (WL-299).
//
// Preview POSTs the draft to /preview and injects the fragment the server
// rendered and sanitized (internal/mdrender); nothing here interprets
// markdown, so the preview can never disagree with how the stored body will
// render. Dictation records audio with MediaRecorder and POSTs it to
// /dictate, which proxies the configured speech-to-text provider; the
// transcription is appended to the draft. Every failure path returns to the
// Write tab with the draft untouched — the component degrades to a plain
// textarea, never blocks one.
(function () {
	"use strict";

	function setTab(root, writing) {
		var write = root.querySelector("[data-md-write]");
		var preview = root.querySelector("[data-md-preview]");
		var ta = root.querySelector("textarea");
		var pane = root.querySelector(".mdpreview");
		write.classList.toggle("active", writing);
		write.setAttribute("aria-selected", writing ? "true" : "false");
		preview.classList.toggle("active", !writing);
		preview.setAttribute("aria-selected", writing ? "false" : "true");
		ta.hidden = !writing;
		pane.hidden = writing;
	}

	function showPreview(root) {
		var ta = root.querySelector("textarea");
		var pane = root.querySelector(".mdpreview");
		pane.textContent = "Rendering…";
		setTab(root, false);
		fetch("/preview", {
			method: "POST",
			headers: { "Content-Type": "application/x-www-form-urlencoded" },
			body: new URLSearchParams({ body: ta.value }),
		})
			.then(function (res) {
				if (!res.ok) throw new Error("preview failed: " + res.status);
				return res.text();
			})
			.then(function (html) {
				// Server-rendered and server-sanitized: the same
				// mdrender pipeline every stored body goes through.
				pane.innerHTML = html;
			})
			.catch(function () {
				pane.textContent = "Preview unavailable.";
			});
	}

	function wireDictation(root) {
		var btn = root.querySelector("[data-md-dictate]");
		if (!btn) return;
		if (!navigator.mediaDevices || !window.MediaRecorder) {
			btn.hidden = true;
			return;
		}
		var recorder = null;

		function stopTracks(rec) {
			rec.stream.getTracks().forEach(function (t) { t.stop(); });
		}

		btn.addEventListener("click", function () {
			if (recorder) {
				recorder.stop();
				return;
			}
			navigator.mediaDevices.getUserMedia({ audio: true }).then(function (stream) {
				var chunks = [];
				recorder = new MediaRecorder(stream);
				btn.setAttribute("aria-pressed", "true");
				btn.classList.add("recording");
				recorder.addEventListener("dataavailable", function (e) {
					if (e.data && e.data.size) chunks.push(e.data);
				});
				recorder.addEventListener("stop", function () {
					var rec = recorder;
					recorder = null;
					btn.setAttribute("aria-pressed", "false");
					btn.classList.remove("recording");
					stopTracks(rec);
					var blob = new Blob(chunks, { type: rec.mimeType || "audio/webm" });
					if (!blob.size) return;
					btn.disabled = true;
					fetch("/dictate", { method: "POST", headers: { "Content-Type": blob.type }, body: blob })
						.then(function (res) {
							if (!res.ok) throw new Error("dictation failed: " + res.status);
							return res.json();
						})
						.then(function (out) {
							if (!out.text) return;
							var ta = root.querySelector("textarea");
							ta.value = ta.value ? ta.value.replace(/\s*$/, "") + "\n\n" + out.text : out.text;
							setTab(root, true);
						})
						.catch(function () { /* the draft is untouched; nothing to undo */ })
						.finally(function () { btn.disabled = false; });
				});
				recorder.start();
			}).catch(function () { /* permission refused: the textarea still works */ });
		});
	}

	document.querySelectorAll("[data-mdinput]").forEach(function (root) {
		root.querySelector("[data-md-write]").addEventListener("click", function () {
			setTab(root, true);
		});
		root.querySelector("[data-md-preview]").addEventListener("click", function () {
			showPreview(root);
		});
		wireDictation(root);
	});
})();
