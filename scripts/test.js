'use strict';

// `npm run test` — run the full test suite: Go tests + npm package tests.
//
// Go tests need git identity configured (the BDD git feature commits into temp
// dirs), so we set GIT_AUTHOR_*/GIT_COMMITTER_* if absent.

const path = require('path');
const { spawnSync } = require('child_process');
const { REPO_ROOT, go, goEnv } = require('./go-env');

function runNode(script) {
  const r = spawnSync('node', [script], { cwd: REPO_ROOT, stdio: 'inherit' });
  if (r.status !== 0) {
    throw new Error(`${script} exited with ${r.status}`);
  }
}

function main() {
  // Go tests (vet + test), with git identity for the BDD git feature.
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

  // npm package tests.
  console.log('[test] npm/test/install.test.js');
  runNode(path.join('npm', 'test', 'install.test.js'));

  // resolve-packed.test.js needs a binary; build it if missing.
  const binPath = path.join(REPO_ROOT, 'bin', 'jixomd');
  if (!require('fs').existsSync(binPath)) {
    console.log('[test] building binary for resolve-packed test...');
    go(['build', '-o', binPath, './cmd/jixomd']);
  }
  console.log('[test] npm/test/resolve-packed.test.js');
  // resolve-packed.test.js needs JIXOMD_BINARY_PATH pointing at a binary.
  const env2 = { ...process.env, JIXOMD_BINARY_PATH: binPath };
  const r2 = spawnSync('node', [path.join('npm', 'test', 'resolve-packed.test.js')], {
    cwd: REPO_ROOT, env: env2, stdio: 'inherit',
  });
  if (r2.status !== 0) throw new Error('resolve-packed.test.js failed');

  console.log('[test] all green ✅');
}

main();
