// editorcheck.js is what editorbrowser_test.go evaluates once the document
// page has loaded: it waits for the island to parse the stored markdown, then
// reports what the round trip produced and how the page's CSP treated the
// styles BlockNote injects at runtime.
//
// A refused <style> stays in the DOM with a null .sheet, so counting those
// says what the policy blocked. Whether that matters is a separate question,
// answered by the probes below: the island copies every refused sheet into a
// constructed stylesheet, and a rule that only exists in an injected sheet
// either computes or it does not.
(async () => {
  const report = {
    violations: [],
    mounted: false,
    injectedStyles: [],
    blockedStyles: 0,
    adoptedSheets: 0,
    inlineStyleAttrs: 0,
    editorPadding: "",
    proseMirrorWhiteSpace: "",
    mantineHiddenFromXs: "",
    markdown: null,
    error: null,
  };

  try {
    if (!window.lodeEditorReady) throw new Error("the island never ran: window.lodeEditorReady is unset");
    await window.lodeEditorReady;
    const editor = document.querySelector(".bn-editor");
    report.mounted = !!editor;
    if (editor) report.editorPadding = getComputedStyle(editor).paddingInlineStart;
    report.markdown = await window.lodeEditorMarkdown();
  } catch (e) {
    report.error = String(e && e.stack ? e.stack : e);
  }

  // Two rules that ship only in a runtime-injected sheet: prosemirror-view's
  // own stylesheet, and Mantine's responsive helpers. If either computes, the
  // sheet it came from reached the page in some form.
  const pm = document.createElement("div");
  pm.className = "ProseMirror";
  const mantine = document.createElement("div");
  mantine.className = "mantine-hidden-from-xs";
  document.body.append(pm, mantine);
  report.proseMirrorWhiteSpace = getComputedStyle(pm).whiteSpace;
  report.mantineHiddenFromXs = getComputedStyle(mantine).display;
  pm.remove();
  mantine.remove();

  for (const el of document.querySelectorAll("style")) {
    const blocked = el.sheet === null;
    report.injectedStyles.push({
      blocked: blocked,
      bytes: (el.textContent || "").length,
      attrs: el.getAttributeNames().map((n) => n + "=" + el.getAttribute(n)).join(" "),
      sample: (el.textContent || "").slice(0, 120),
    });
    if (blocked) report.blockedStyles++;
  }
  report.adoptedSheets = document.adoptedStyleSheets.length;
  // A style attribute written into markup or through setAttribute is refused
  // by style-src without 'unsafe-inline'; one written through el.style is not,
  // because CSP does not police the CSSOM.
  report.inlineStyleAttrs = document.querySelectorAll("[style]").length;
  report.violations = window.__cspViolations || [];
  return JSON.stringify(report);
})()
