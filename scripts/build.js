"use strict";

// `npm run build` — release-grade build of the host binary.
//
// Equivalent to: env -u GOROOT GOTOOLCHAIN=local go build -trimpath
// -ldflags="-s -w" -o bin/jixomd ./cmd/jixomd
//
// Produces a stripped, reproducible-path binary in bin/ (the same flags the
// GitHub Actions release workflow uses). Output path is printed at the end.

const fs = require("fs");
const path = require("path");
const { REPO_ROOT, go, hostPlatform, hostBinaryName } = require("./go-env");

const BIN_DIR = path.join(REPO_ROOT, "bin");

function main() {
  const { goos } = hostPlatform();
  const binName = hostBinaryName(goos);
  const outPath = path.join(BIN_DIR, binName);

  fs.mkdirSync(BIN_DIR, { recursive: true });

  console.log(
    "[build] release-grade build →",
    path.relative(REPO_ROOT, outPath),
  );
  go([
    "build",
    "-trimpath",
    "-ldflags=-s -w",
    "-gcflags=all=-l",
    "-o",
    outPath,
    "./cmd/jixomd",
  ]);

  // Ensure the binary is executable on unix.
  if (goos !== "windows") {
    fs.chmodSync(outPath, 0o755);
  }

  const stat = fs.statSync(outPath);
  const mb = (stat.size / (1024 * 1024)).toFixed(1);
  console.log(`[build] done: ${path.relative(REPO_ROOT, outPath)} (${mb} MB)`);
}

main();
