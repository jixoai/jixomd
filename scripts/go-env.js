"use strict";

// Shared helpers for the project scripts.
//
// This machine has a stale GOROOT env var (points at an old Homebrew Cellar
// while the go binary is newer). We detect that and unset GOROOT before
// invoking go, matching the `env -u GOROOT` we use in the shell.
const { spawnSync } = require("child_process");
const fs = require("fs");
const path = require("path");

const REPO_ROOT = path.resolve(__dirname, "..");

/**
 * The go binary path, discovered via PATH. Returns 'go' if not found (spawn
 * will then fail with a clear error).
 */
function goBin() {
  const r = spawnSync("which", ["go"], { encoding: "utf8" });
  if (r.status === 0 && r.stdout.trim()) return r.stdout.trim();
  return "go";
}

/**
 * A clean environment for invoking go: copies process.env but unsets GOROOT so
 * the go binary discovers its own (correct) GOROOT. Also pins GOTOOLCHAIN=local
 * to avoid go auto-downloading a newer toolchain.
 */
function goEnv() {
  const env = { ...process.env };
  delete env.GOROOT;
  env.GOTOOLCHAIN = "local";
  return env;
}

/**
 * Run a go command, inheriting stdio. Throws on non-zero exit. Returns stdout.
 */
function go(args, opts) {
  opts = opts || {};
  const r = spawnSync(opts.goBin ?? "go", args, {
    cwd: opts.cwd || REPO_ROOT,
    env: goEnv(),
    stdio: opts.stdio || "inherit",
    encoding: opts.encoding,
  });
  if (r.status !== 0) {
    const msg = `go ${args.join(" ")} exited with ${r.status}`;
    throw new Error(msg);
  }
  return r.stdout;
}

/**
 * Current GOOS/GOARCH (host).
 */
function hostPlatform() {
  const out = go(["env", "GOOS", "GOARCH"], {
    stdio: "pipe",
    encoding: "utf8",
  });
  const [goos, goarch] = out.trim().split(/\s+/);
  return { goos, goarch };
}

/**
 * The binary name for the host platform (jixomd or jixomd.exe).
 */
function hostBinaryName(goos) {
  return goos === "windows" ? "jixomd.exe" : "jixomd";
}

module.exports = { REPO_ROOT, goBin, goEnv, go, hostPlatform, hostBinaryName };
