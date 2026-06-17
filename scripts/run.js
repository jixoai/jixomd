'use strict';

// `npm run jixomd` — debug build then run with the user's args.
//
// Builds ./cmd/jixomd WITHOUT release flags (-s -w / -trimpath) so the binary
// keeps symbols for debugging, then execs it, forwarding all extra npm args:
//
//   npm run jixomd -- file.md                # expand a file
//   npm run jixomd -- file.md --watch        # watch mode
//   npm run jixomd -- resolve                # resolve from stdin
//   npm run jixomd -- - < in.md > out.md     # stdin/stdout
//
// The binary is cached in bin/ and only rebuilt when sources change (newer
// than the binary), so repeated runs are instant.

const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');
const { REPO_ROOT, go, goEnv, hostPlatform, hostBinaryName } = require('./go-env');

const BIN_DIR = path.join(REPO_ROOT, 'bin');
const SRC_DIRS = ['cmd', 'core', 'grammar', 'io', 'contract', 'backend', 'cli'];

function buildNeeded(binPath) {
  if (!fs.existsSync(binPath)) return true;
  const binMtime = fs.statSync(binPath).mtimeMs;
  for (const dir of SRC_DIRS) {
    const root = path.join(REPO_ROOT, dir);
    if (!fs.existsSync(root)) continue;
    let newest = 0;
    walk(root, (p, st) => {
      if (p.endsWith('.go') && st.mtimeMs > newest) newest = st.mtimeMs;
    });
    if (newest > binMtime) return true;
  }
  return false;
}

function walk(dir, fn) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name.startsWith('.')) continue;
    const p = path.join(dir, entry.name);
    const st = fs.statSync(p);
    if (entry.isDirectory()) {
      walk(p, fn);
    } else {
      fn(p, st);
    }
  }
}

function main() {
  const { goos } = hostPlatform();
  const binName = hostBinaryName(goos);
  const binPath = path.join(BIN_DIR, binName);

  fs.mkdirSync(BIN_DIR, { recursive: true });

  if (buildNeeded(binPath)) {
    console.error('[jixomd] building (debug)...');
    go(['build', '-o', binPath, './cmd/jixomd']);
    if (goos !== 'windows') fs.chmodSync(binPath, 0o755);
  }

  // Forward everything after `--` to the binary.
  const passArgs = process.argv.slice(2);
  if (passArgs.length === 0) {
    console.error('usage: npm run jixomd -- <args...>   (e.g. file.md, --watch, resolve, -)');
    process.exit(2);
  }

  const r = spawnSync(binPath, passArgs, { stdio: 'inherit', env: goEnv() });
  process.exit(r.status || 0);
}

main();
