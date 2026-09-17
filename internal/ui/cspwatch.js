// cspwatch.js is served by editorbrowser_test.go's fixture server and injected
// into the page's <head> ahead of every other script. It is a file rather than
// an inline script for the reason the check exists at all: the page runs under
// `script-src 'self'` with no nonce, so an inline listener would itself be the
// first thing refused.
window.__cspViolations = [];
document.addEventListener("securitypolicyviolation", function (e) {
  window.__cspViolations.push({
    directive: e.effectiveDirective || e.violatedDirective,
    blockedURI: e.blockedURI,
    sourceFile: e.sourceFile,
    line: e.lineNumber,
    sample: e.sample,
  });
});
