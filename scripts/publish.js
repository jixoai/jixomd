'use strict';

// `npm run publish` — build all platform binaries + TS dist, then publish
// the entire pnpm workspace (7 packages: 1 main + 6 platform) in one go.
//
// This is a LOCAL publish script. For CI, release.yml does the same via
// GitHub Actions on tag push. Use this for manual publishes or testing the
// publish flow locally.
//
// Usage:
//   npm run publish                      # publishes current workspace versions
//   npm run publish -- 0.2.0             # bump all to 0.2.0 then publish
//   npm run publish -- --dry-run         # build + pack, don't actually publish
//   npm run publish -- --tag beta        # publish under a dist-tag
//
// The script:
//   1. (optional) bumps all workspace packages to the given version
//   2. cross-compiles 6 platform binaries into npm/jixomd-{slug}/
//   3. builds the TypeScript SDK (tsdown + tsc)
//   4. runs `pnpm publish -r` (skips the private root package automatically)

const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');
const { REPO_ROOT, go, goEnv } = require('./go-env');

const PLATFORMS = [
  { goos: 'darwin', goarch: 'arm64', slug: 'darwin-arm64', bin: 'jixomd' },
  { goos: 'darwin', goarch: 'amd64', slug: 'darwin-x64',   bin: 'jixomd' },
  { goos: 'linux',  goarch: 'arm64', slug: 'linux-arm64',  bin: 'jixomd' },
  { goos: 'linux',  goarch: 'amd64', slug: 'linux-x64',    bin: 'jixomd' },
  { goos: 'windows', goarch: 'arm64', slug: 'win-arm64',   bin: 'jixomd.exe' },
  { goos: 'windows', goarch: 'amd64', slug: 'win-x64',     bin: 'jixomd.exe' },
];

// Directories whose .go sources are part of the binary. Used for the
// incremental-cache check (skip rebuild if binary is newer than all sources).
const SRC_DIRS = ['cmd', 'core', 'grammar', 'io', 'contract', 'backend', 'cli'];

/**
 * Walk a directory tree, calling fn for every .go file.
 */
function walkGo(dir, fn) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name.startsWith('.')) continue;
    const p = path.join(dir, entry.name);
    const st = fs.statSync(p);
    if (entry.isDirectory()) walkGo(p, fn);
    else if (entry.name.endsWith('.go')) fn(p, st);
  }
}

/**
 * Returns true if the binary at outPath needs rebuilding: it doesn't exist,
 * or any .go source is newer than it.
 */
function needsRebuild(outPath) {
  if (!fs.existsSync(outPath)) return true;
  const binMtime = fs.statSync(outPath).mtimeMs;
  for (const dir of SRC_DIRS) {
    const root = path.join(REPO_ROOT, dir);
    if (!fs.existsSync(root)) continue;
    let stale = false;
    walkGo(root, (_p, st) => {
      if (st.mtimeMs > binMtime) stale = true;
    });
    if (stale) return true;
  }
  return false;
}

/**
 * Force rebuild even if cache says fresh (e.g. version bump changed source).
 */
function forceRebuild() {
  return process.env.JIXOMD_FORCE_REBUILD === '1';
}

function pnpm(args, opts) {
  opts = opts || {};
  const r = spawnSync('pnpm', args, {
    cwd: opts.cwd || REPO_ROOT,
    stdio: opts.stdio || 'inherit',
    encoding: opts.encoding,
  });
  if (r.status !== 0) {
    throw new Error(`pnpm ${args.join(' ')} exited with ${r.status}`);
  }
  return r;
}

function main() {
  // Parse args after `--`.
  const args = process.argv.slice(2);
  const dryRun = args.includes('--dry-run');
  const tagIdx = args.indexOf('--tag');
  const distTag = tagIdx >= 0 ? args[tagIdx + 1] : null;

  // First non-flag arg is the version (if numeric).
  const versionArg = args.find(a => /^\d+\.\d+\.\d+/.test(a));

  // ── Step 1: (optional) version bump ──
  if (versionArg) {
    console.log(`[publish] bumping all workspace packages to ${versionArg}`);
    pnpm(['-r', 'exec', '--', 'npm', 'version', versionArg, '--no-git-tag-version', '--allow-same-version']);
  }

  // ── Step 2: cross-compile platform binaries (incremental) ──
  console.log('[publish] building platform binaries...');
  for (const p of PLATFORMS) {
    const outDir = path.join(REPO_ROOT, 'npm', `jixomd-${p.slug}`);
    const outPath = path.join(outDir, p.bin);

    // Skip rebuild if the binary is already newer than all .go sources.
    if (!forceRebuild() && !needsRebuild(outPath)) {
      const sz = (fs.statSync(outPath).size / 1024 / 1024).toFixed(1);
      console.log(`  ${p.slug}... cached (${sz} MB)`);
      continue;
    }

    process.stdout.write(`  ${p.slug}... `);
    const env = { ...goEnv(), GOOS: p.goos, GOARCH: p.goarch, CGO_ENABLED: '0' };
    const r = spawnSync('go', [
      'build', '-trimpath', '-ldflags=-s -w',
      '-o', outPath, './cmd/jixomd',
    ], { cwd: REPO_ROOT, env, stdio: 'pipe', encoding: 'utf8' });
    if (r.status !== 0) {
      console.error('FAIL');
      console.error(r.stderr);
      process.exit(1);
    }
    const sz = (fs.statSync(outPath).size / 1024 / 1024).toFixed(1);
    console.log(`OK (${sz} MB)`);
  }

  // ── Step 3: build TypeScript SDK ──
  console.log('[publish] building TypeScript SDK (tsdown + tsc)...');
  pnpm(['run', 'build'], { cwd: path.join(REPO_ROOT, 'npm', 'jixomd') });

  // ── Step 4: verify ──
  console.log('[publish] verifying packages pack...');
  if (dryRun) {
    pnpm(['-r', 'pack', '--dry-run']);
    console.log('[publish] dry-run complete — no packages published.');
    return;
  }

  // ── Step 5: publish ──
  console.log('[publish] publishing workspace...');
  const publishArgs = ['-r', 'publish', '--access', 'public', '--no-git-checks'];
  if (distTag) {
    publishArgs.push('--tag', distTag);
  }
  pnpm(publishArgs);
  console.log('[publish] done ✅');
}

main();
