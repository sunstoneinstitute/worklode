// Bundles the editor island straight into internal/ui/assets, which is what
// the Go binary embeds and serves at /assets/. Two outputs land there:
// editor.js and the editor.css esbuild extracts from the stylesheet the island
// imports — the cockpit runs under `style-src 'self'`, so BlockNote's CSS has
// to be a file the page links, never a <style> the bundle injects at runtime.
import esbuild from "esbuild";

await esbuild.build({
  entryPoints: ["src/main.tsx"],
  bundle: true,
  format: "iife",
  target: "es2022",
  jsx: "automatic",
  minify: true,
  sourcemap: false,
  // React reads this at module scope; without it the bundle carries the
  // development build, warnings and all.
  define: { "process.env.NODE_ENV": '"production"' },
  // BlockNote's stylesheets @import each other by bare specifier, and the
  // export those specifiers resolve through is keyed on "style".
  conditions: ["style"],
  loader: { ".woff2": "dataurl", ".woff": "dataurl", ".ttf": "dataurl", ".svg": "dataurl" },
  outfile: "../../internal/ui/assets/editor.js",
  logLevel: "info",
});
