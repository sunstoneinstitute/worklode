import esbuild from "esbuild";
import process from "node:process";
import builtins from "builtin-modules";

const production = process.argv[2] === "production";

const context = await esbuild.context({
  entryPoints: ["src/main.ts"],
  bundle: true,
  // src/ imports no Node builtin -- the plugin runs on mobile, where none
  // exists. The list stays as a guard: an accidental new import fails at load
  // time on mobile rather than being silently bundled into main.js and
  // shipped. builtin-modules lists bare specifiers ("crypto") only, and
  // esbuild treats the "node:"-prefixed form as a distinct specifier, so both
  // forms are listed.
  external: ["obsidian", "electron", ...builtins, ...builtins.map((m) => `node:${m}`)],
  format: "cjs",
  target: "es2022",
  logLevel: "info",
  sourcemap: production ? false : "inline",
  treeShaking: true,
  outfile: "main.js",
  minify: production,
});

if (production) {
  await context.rebuild();
  process.exit(0);
} else {
  await context.watch();
}
