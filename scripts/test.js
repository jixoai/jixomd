'use strict';

// `npm run test` — run the full test suite: Go tests + npm package tests.
//
// Go tests need git identity configured (the BDD git feature commits into temp
// dirs), so we set GIT_AUTHOR_*/GIT_COMMITTER_* if absent.

const path = require('path');
const { spawnSync } = require('child_process');
const { REPO_ROOT, go, goEnv } = require('./go-env');

// Inline platform detection (avoid depending on the TS-built dist/ at this
// stage — the test script runs BEFORE the TS build).
function hostSlug() {
  const p = process.platform === 'win32' ? 'win' : process.platform;
  const a = process.arch === 'x64' ? 'x64' : process.arch;
  return `${p}-${a}`;
}

function runNode(script, env) {
  const r = spawnSync('node', [script], { cwd: REPO_ROOT, env, stdio: 'inherit' });
  if (r.status !== 0) {
    throw new Error(`${script} exited with ${r.status}`);
  }
}

function main() {
  const env = { ...goEnv() };
  if (!env.GIT_AUTHOR_NAME) {
    env.GIT_AUTHOR_NAME = 'jixomd-test';
    env.GIT_AUTHOR_EMAIL = 'jixomd@test';
    env.GIT_COMMITTER_NAME = 'jixomd-test';
    env.GIT_COMMITTER_EMAIL = 'jixomd@test';
  }
  console.log('[test] go vet ./...');
  let r = spawnSync('go', ['vet', './...'], { cwd: REPO_ROOT, env, stdio: 'inherit' });
  if (r.status !== 0) throw new Error('go vet failed');

  console.log('[test] go test ./...');
  r = spawnSync('go', ['test', '-count=1', './...'], { cwd: REPO_ROOT, env, stdio: 'inherit' });
  if (r.status !== 0) throw new Error('go test failed');

  // Build the host binary into the matching platform package dir so
  // binaryPath() resolves via the local workspace fallback.
  const slug = hostSlug();
  const binName = slug.startsWith('win-') ? 'jixomd.exe' : 'jixomd';
  const pkgBinDir = path.join(REPO_ROOT, 'npm', `jixomd-${slug}`);
  const pkgBinPath = path.join(pkgBinDir, binName);
  console.log(`[test] building host binary → npm/jixomd-${slug}/${binName}`);
  go(['build', '-o', pkgBinPath, './cmd/jixomd']);

  // Build the TypeScript SDK package (tsdown + tsc d.ts).
  console.log('[test] building npm/jixomd (tsdown + tsc)');
  const npmDir = path.join(REPO_ROOT, 'npm', 'jixomd');
  let bn = spawnSync('pnpm', ['run', 'build'], { cwd: npmDir, stdio: 'inherit' });
  if (bn.status !== 0) throw new Error('npm/jixomd build failed');

  console.log('[test] npm/test/resolve-platform.test.js');
  runNode(path.join('npm', 'test', 'resolve-platform.test.js'));

  console.log('[test] npm/test/resolve-packed.test.js');
  runNode(path.join('npm', 'test', 'resolve-packed.test.js'), {
    ...env,
    JIXOMD_BINARY_PATH: pkgBinPath,
  });

  console.log('[test] all green ✅');
}

main();
