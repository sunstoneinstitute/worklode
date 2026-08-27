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
//
// Typing ":sm" opens an emoji completion popup listing the matching
// shortcodes with their glyphs; accepting one inserts the shortcode, which
// is what mdrender substitutes when the body is rendered. The name list is
// /assets/emoji.json, generated from goldmark-emoji's own table by
// scripts/gen-emoji.py, and fetched once on the first ":" typed.
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

	// --- emoji shortcode completion ---------------------------------------

	// A shortcode candidate: ":" at a word boundary followed by at least two
	// shortcode characters, ending at the caret. Two, not one, so an ordinary
	// colon in prose does not open the popup.
	var CANDIDATE = /(^|[\s(\[{>])(:([a-z0-9_+-]{2,}))$/;
	var MAX_HITS = 8;
	var emojiPromise = null;

	function loadEmoji(src) {
		if (!emojiPromise) {
			emojiPromise = fetch(src, { headers: { Accept: "application/json" } })
				.then(function (res) {
					if (!res.ok) throw new Error("emoji list failed: " + res.status);
					return res.json();
				})
				.catch(function () { return {}; });
		}
		return emojiPromise;
	}

	// match returns up to MAX_HITS shortcodes for q: prefix matches first,
	// shortest first within each group, so ":sm" offers "smile" ahead of
	// "small_airplane" and "persevere".
	function match(names, q) {
		var pre = [], sub = [];
		for (var i = 0; i < names.length; i++) {
			var idx = names[i].indexOf(q);
			if (idx === 0) pre.push(names[i]);
			else if (idx > 0) sub.push(names[i]);
		}
		function byLength(a, b) { return a.length - b.length || (a < b ? -1 : 1); }
		return pre.sort(byLength).concat(sub.sort(byLength)).slice(0, MAX_HITS);
	}

	function wireEmoji(root) {
		var src = root.getAttribute("data-emoji-src");
		if (!src) return;
		var ta = root.querySelector("textarea");
		var pane = root.querySelector(".mdsuggest");
		var map = null, names = [], hits = [], active = 0, start = -1;

		function close() {
			hits = [];
			start = -1;
			pane.hidden = true;
			pane.textContent = "";
		}

		function render() {
			pane.textContent = "";
			hits.forEach(function (name, i) {
				var item = document.createElement("div");
				item.className = "mdsuggest-item" + (i === active ? " active" : "");
				item.setAttribute("role", "option");
				item.setAttribute("aria-selected", i === active ? "true" : "false");
				item.textContent = map[name] + "  :" + name + ":";
				// mousedown, not click: the textarea must not lose the caret
				// before the insertion reads it.
				item.addEventListener("mousedown", function (e) {
					e.preventDefault();
					accept(i);
				});
				pane.appendChild(item);
			});
			pane.hidden = false;
		}

		function accept(i) {
			var name = hits[i];
			if (name === undefined || start < 0) return;
			var caret = ta.selectionStart;
			var text = ta.value;
			ta.value = text.slice(0, start) + ":" + name + ": " + text.slice(caret);
			var at = start + name.length + 3;
			ta.setSelectionRange(at, at);
			close();
			ta.focus();
		}

		function refresh() {
			if (!map) return;
			var caret = ta.selectionStart;
			var m = caret === ta.selectionEnd && CANDIDATE.exec(ta.value.slice(0, caret));
			if (!m) return close();
			hits = match(names, m[3]);
			if (!hits.length) return close();
			start = caret - m[2].length;
			active = 0;
			render();
		}

		ta.addEventListener("input", function () {
			if (map) return refresh();
			if (ta.value.indexOf(":") < 0) return;
			loadEmoji(src).then(function (loaded) {
				map = loaded;
				names = Object.keys(map);
				refresh();
			});
		});
		ta.addEventListener("blur", close);
		ta.addEventListener("keydown", function (e) {
			if (pane.hidden) return;
			if (e.key === "Escape") { close(); e.preventDefault(); return; }
			if (e.key === "Enter" || e.key === "Tab") { accept(active); e.preventDefault(); return; }
			if (e.key === "ArrowDown" || e.key === "ArrowUp") {
				active = (active + (e.key === "ArrowDown" ? 1 : hits.length - 1)) % hits.length;
				render();
				e.preventDefault();
			}
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
		wireEmoji(root);
	});
})();
