'use strict';

// `npm run test` — run the full test suite: Go tests + npm package tests.
//
// Go tests need git identity configured (the BDD git feature commits into temp
// dirs), so we set GIT_AUTHOR_*/GIT_COMMITTER_* if absent.

const path = require('path');
const { spawnSync } = require('child_process');
const { REPO_ROOT, go, goEnv } = require('./go-env');
const { detect } = require('../npm/jixomd/platform.js');

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
  const plat = detect();
  const binName = plat.slug.startsWith('win-') ? 'jixomd.exe' : 'jixomd';
  const pkgBinDir = path.join(REPO_ROOT, 'npm', `jixomd-${plat.slug}`);
  const pkgBinPath = path.join(pkgBinDir, binName);
  console.log(`[test] building host binary → npm/jixomd-${plat.slug}/${binName}`);
  go(['build', '-o', pkgBinPath, './cmd/jixomd']);

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
