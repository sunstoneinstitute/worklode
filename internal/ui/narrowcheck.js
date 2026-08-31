// narrowcheck.js is the measurement half of the narrow-width audit
// (scripts/narrow-check.sh, internal/ui/narrowbrowser_test.go). It runs once
// per page per viewport inside a headless browser and returns the four
// measurements the WL-140 audit made, as JSON:
//
//   overflow   1.4.10 Reflow — content wider than the viewport, so the page
//              scrolls horizontally. A table inside a .tablewrap (or any other
//              ancestor that scrolls in x) is the criterion's own exception and
//              is not reported.
//   truncated  1.4.10 again — text clipped or ellipsised because its box was
//              compressed instead of reflowing.
//   targets    2.5.8 Target Size — a pointer target whose box is under 24x24
//              CSS px. Inline links in flowing text carry the criterion's
//              inline exception and are marked `inline`, not failed.
//   focus      2.4.11 Focus Not Obscured — a focused control, or the target of
//              an in-page jump, that lands entirely under a sticky or fixed
//              element.
//
// It is deliberately dependency-free and returns data, never verdicts: the Go
// side decides what fails.

(function () {
  var VW = window.innerWidth;
  var LIMIT = 6; // per-category cap, so one bad page cannot bury the rest
  var MIN_TARGET = 24;

  function selector(el) {
    var path = [];
    for (var e = el; e && e.nodeType === 1 && path.length < 3; e = e.parentElement) {
      var part = e.tagName.toLowerCase();
      if (e.id) {
        path.unshift(part + "#" + e.id);
        break;
      }
      var cls = (e.getAttribute("class") || "").trim().split(/\s+/).filter(Boolean).slice(0, 2);
      if (cls.length) part += "." + cls.join(".");
      path.unshift(part);
    }
    return path.join(" > ");
  }

  function visible(el) {
    var r = el.getBoundingClientRect();
    if (r.width === 0 && r.height === 0) return false;
    var cs = getComputedStyle(el);
    return cs.visibility !== "hidden" && cs.display !== "none" && cs.opacity !== "0";
  }

  function scrollsX(el) {
    var ox = getComputedStyle(el).overflowX;
    return ox === "auto" || ox === "scroll";
  }

  function insideScroller(el) {
    for (var e = el.parentElement; e; e = e.parentElement) {
      if (scrollsX(e)) return true;
    }
    return false;
  }

  var all = Array.prototype.slice.call(document.querySelectorAll("body *"));

  // --- 1.4.10 Reflow: page-level horizontal overflow -------------------------
  var doc = document.documentElement;
  var pageWidth = Math.max(doc.scrollWidth, document.body.scrollWidth);
  var overflow = [];
  if (pageWidth > VW + 1) {
    var wide = all.filter(function (el) {
      if (!visible(el) || insideScroller(el)) return false;
      // Only rightward overflow: in an LTR document, content placed to the
      // left of the viewport is clipped and unreachable, which is exactly how
      // the visually-hidden skip link is positioned.
      return el.getBoundingClientRect().right > VW + 1;
    });
    // Report the deepest offenders only: an ancestor is wide because its child
    // is, and naming the child is what tells you what to fix.
    overflow = wide
      .filter(function (el) {
        return !wide.some(function (o) {
          return o !== el && el.contains(o);
        });
      })
      .slice(0, LIMIT)
      .map(function (el) {
        var r = el.getBoundingClientRect();
        return { sel: selector(el), right: Math.round(r.right), width: Math.round(r.width) };
      });
  }

  // --- 1.4.10 Reflow: text clipped instead of wrapped ------------------------
  var truncated = all
    .filter(function (el) {
      if (!visible(el) || el.scrollWidth <= el.clientWidth + 1) return false;
      var cs = getComputedStyle(el);
      if (cs.overflowX !== "hidden" && cs.textOverflow !== "ellipsis") return false;
      return cs.whiteSpace === "nowrap" || cs.textOverflow === "ellipsis";
    })
    .slice(0, LIMIT)
    .map(function (el) {
      return {
        sel: selector(el),
        shown: Math.round(el.clientWidth),
        needed: Math.round(el.scrollWidth),
        text: (el.textContent || "").trim().slice(0, 40),
      };
    });

  // --- 2.5.8 Target Size -----------------------------------------------------
  var TARGETS = 'a[href], button, input, select, textarea, summary, [role="button"], [role="checkbox"], [tabindex]:not([tabindex="-1"])';
  var seenTarget = {};
  var targets = Array.prototype.slice
    .call(document.querySelectorAll(TARGETS))
    .filter(function (el) {
      if (el.disabled || (el.type === "hidden") || !visible(el)) return false;
      var r = el.getBoundingClientRect();
      return r.width < MIN_TARGET || r.height < MIN_TARGET;
    })
    .map(function (el) {
      var r = el.getBoundingClientRect();
      var cs = getComputedStyle(el);
      return {
        sel: selector(el),
        w: Math.round(r.width * 10) / 10,
        h: Math.round(r.height * 10) / 10,
        // The criterion exempts a target that is inline in a sentence of text.
        inline: cs.display === "inline" && el.tagName === "A",
      };
    })
    .filter(function (t) {
      var key = t.sel + "|" + t.w + "x" + t.h;
      if (seenTarget[key]) return false;
      seenTarget[key] = true;
      return true;
    })
    .slice(0, LIMIT);

  // --- 2.4.11 Focus Not Obscured --------------------------------------------
  // Anything sticky or fixed against the top of the viewport is what a focused
  // control can end up underneath. Measure it before we start scrolling.
  var stickyBottom = 0;
  all.forEach(function (el) {
    var cs = getComputedStyle(el);
    if (cs.position !== "fixed" && cs.position !== "sticky") return;
    if (!visible(el)) return;
    var r = el.getBoundingClientRect();
    if (r.top <= 2 && r.bottom > stickyBottom) stickyBottom = Math.round(r.bottom);
  });

  // coveredBy reports what hides el, or "" when any part of it is reachable by
  // eye. The rect is clamped to the viewport first: an element taller than the
  // viewport has corners off-screen, and sampling those would call a fully
  // visible element hidden.
  function coveredBy(el) {
    var r = el.getBoundingClientRect();
    var left = Math.max(r.left, 0), top = Math.max(r.top, 0);
    var right = Math.min(r.right, window.innerWidth), bottom = Math.min(r.bottom, window.innerHeight);
    if (right - left < 1 || bottom - top < 1) return "scrolled out of view";
    var pts = [
      [left + 1, top + 1],
      [right - 1, top + 1],
      [left + 1, bottom - 1],
      [right - 1, bottom - 1],
      [(left + right) / 2, (top + bottom) / 2],
    ];
    var cover = null;
    for (var i = 0; i < pts.length; i++) {
      var hit = document.elementFromPoint(pts[i][0], pts[i][1]);
      if (!hit) continue;
      if (hit === el || el.contains(hit) || hit.contains(el)) return "";
      cover = hit;
    }
    return cover ? selector(cover) : "";
  }

  var focus = [];
  var tabbable = Array.prototype.slice
    .call(document.querySelectorAll(TARGETS))
    .filter(function (el) {
      if (el.disabled || el.tabIndex < 0 || !visible(el)) return false;
      // A zero-width or zero-height control has no box to be obscured; the
      // target-size measurement above is what reports it.
      var r = el.getBoundingClientRect();
      return r.width >= 1 && r.height >= 1;
    });
  tabbable.forEach(function (el) {
    if (focus.length >= LIMIT) return;
    try {
      el.focus();
    } catch (e) {
      return;
    }
    if (document.activeElement !== el) return;
    var by = coveredBy(el);
    if (by) focus.push({ sel: selector(el), by: by, kind: "tab" });
  });

  // In-page jumps (the skip link, a doc's #sec-N section links) land their
  // target, not a control: the fix is scroll-padding-top clearing the sticky
  // bar.
  window.scrollTo(0, 0);
  var jumps = Array.prototype.slice.call(document.querySelectorAll('a[href^="#"]'));
  var seenJump = {};
  jumps.forEach(function (a) {
    if (focus.length >= LIMIT * 2) return;
    var id = a.getAttribute("href").slice(1);
    if (!id || seenJump[id]) return;
    seenJump[id] = true;
    var target = document.getElementById(id);
    if (!target) return;
    a.click();
    var r = target.getBoundingClientRect();
    if (r.top < stickyBottom - 1) {
      focus.push({
        sel: "#" + id,
        by: "sticky header (target top " + Math.round(r.top) + "px, header bottom " + stickyBottom + "px)",
        kind: "jump",
      });
    }
  });

  return JSON.stringify({
    viewport: VW,
    pageWidth: pageWidth,
    stickyBottom: stickyBottom,
    overflow: overflow,
    truncated: truncated,
    targets: targets,
    focus: focus,
  });
})();
