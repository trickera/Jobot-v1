import { context } from "esbuild";
import { fileURLToPath } from "node:url";
import path from "node:path";

const rootDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const desktopDir = path.join(rootDir, "apps", "desktop");
const watch = process.argv.includes("--watch");

const ctx = await context({
  entryPoints: [
    path.join(desktopDir, "electron", "main.ts"),
    path.join(desktopDir, "electron", "preload.ts"),
  ],
  platform: "node",
  format: "cjs",
  bundle: true,
  external: ["electron"],
  sourcemap: true,
  outdir: path.join(desktopDir, "dist-electron"),
  outExtension: { ".js": ".cjs" },
  logLevel: "info",
});

if (watch) {
  await ctx.watch();
  console.log("[build-electron] watching apps/desktop/electron/main.ts and preload.ts...");
} else {
  await ctx.rebuild();
  await ctx.dispose();
}
