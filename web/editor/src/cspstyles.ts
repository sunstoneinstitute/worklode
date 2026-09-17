// Three of BlockNote's dependencies inject a <style> element at runtime:
// prosemirror-view (through style-mod), Mantine's responsive helpers, and
// Mantine's CSS variables. The cockpit serves `style-src 'self'` with no
// nonce, so the browser refuses all three and the editor renders with no
// layout rules at all — see internal/ui/editorbrowser_test.go, which measures
// exactly this.
//
// A refused <style> keeps its text; only its .sheet is never created. Copying
// that text into a constructed stylesheet puts the rules in the CSSOM, which
// style-src does not police — the same move internal/ui/assets/mermaid-init.js
// makes for the styles mermaid writes into its SVG (WL-851).
//
// The observer watches text changes too, because Mantine populates its
// variables element after inserting it.

const adopted = new Map<HTMLStyleElement, CSSStyleSheet>();

function sync(el: HTMLStyleElement) {
  // A style element the browser accepted needs nothing: .sheet is set.
  if (el.sheet) return;
  const css = el.textContent || "";
  let sheet = adopted.get(el);
  if (!sheet) {
    sheet = new CSSStyleSheet();
    adopted.set(el, sheet);
    document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet];
  }
  sheet.replaceSync(css);
}

function scan(root: ParentNode) {
  for (const el of root.querySelectorAll("style")) sync(el);
}

export function adoptRefusedStyles() {
  if (typeof CSSStyleSheet === "undefined" || !("replaceSync" in CSSStyleSheet.prototype)) return;
  scan(document);
  new MutationObserver((records) => {
    for (const r of records) {
      const target = r.target as Node;
      if (target.nodeType === Node.TEXT_NODE && target.parentElement?.tagName === "STYLE") {
        sync(target.parentElement as HTMLStyleElement);
        continue;
      }
      if (target instanceof Element && target.tagName === "STYLE") {
        sync(target as HTMLStyleElement);
        continue;
      }
      for (const node of r.addedNodes) {
        if (node instanceof HTMLStyleElement) sync(node);
        else if (node instanceof Element) scan(node);
      }
    }
  }).observe(document, { childList: true, subtree: true, characterData: true });
}
