'use strict';

// Integration test for the npm install.js download path.
//
// Spins up a tiny HTTP server serving a fake release asset, sets
// JIXOMD_MIRROR to point at it, runs install.js (async, so the server can
// serve during the child's fetch), and asserts the binary was extracted.
// Run with: node test/install.test.js

const fs = require('fs');
const http = require('http');
const os = require('os');
const path = require('path');
const { execFileSync, spawn } = require('child_process');

const PKG_DIR = path.join(__dirname, '..', 'jixomd');
const PKG = require(path.join(PKG_DIR, 'package.json'));
const { detect, assetName, binaryName } = require(path.join(PKG_DIR, 'platform.js'));

function tmpdir() {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'jixomd-install-test-'));
}

async function main() {
  const plat = detect();
  const version = PKG.version;
  const asset = assetName(version, plat.slug);

  // Build a fake archive containing a dummy binary.
  const serverDir = tmpdir();
  const dummyBin = path.join(serverDir, binaryName(plat.slug));
  fs.writeFileSync(dummyBin, '#!/bin/sh\necho "fake jixomd binary"\n', { mode: 0o755 });

  const archivePath = path.join(serverDir, asset);
  if (plat.slug.startsWith('windows-')) {
    try {
      execFileSync('zip', ['-q', '-j', archivePath, dummyBin]);
    } catch (_) {
      console.log('SKIP: zip not available on this host');
      return;
    }
  } else {
    execFileSync('tar', ['-czf', archivePath, '-C', serverDir, binaryName(plat.slug)]);
  }

  // Serve the archive over HTTP. The server MUST keep handling requests while
  // install.js runs, so we use async spawn (not spawnSync, which would freeze
  // this process and starve the server).
  const server = http.createServer((req, res) => {
    if (req.url.endsWith(asset)) {
      fs.createReadStream(archivePath).pipe(res);
    } else {
      res.writeHead(404);
      res.end('not found');
    }
  });

  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  const mirror = `http://127.0.0.1:${port}`;

  // Prepare a temp copy of the package dir for install.js to run in.
  const testPkgDir = tmpdir();
  for (const f of ['package.json', 'install.js', 'platform.js', 'index.js']) {
    fs.copyFileSync(path.join(PKG_DIR, f), path.join(testPkgDir, f));
  }

  // Run install.js asynchronously so the server stays alive.
  await new Promise((resolve) => {
    const child = spawn('node', ['install.js'], {
      cwd: testPkgDir,
      env: {
        ...process.env,
        JIXOMD_MIRROR: mirror,
        JIXOMD_REPO: 'jixoai/jixomd',
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (d) => { stdout += d; });
    child.stderr.on('data', (d) => { stderr += d; });
    const timer = setTimeout(() => {
      child.kill('SIGKILL');
      resolve({ status: null, killed: true, stdout, stderr });
    }, 15000);
    child.on('close', (status) => {
      clearTimeout(timer);
      resolve({ status, killed: false, stdout, stderr });
    });
  }).then((result) => {
    server.close();

    if (result.killed) {
      console.error('FAIL: install.js timed out');
      console.error('stdout:', result.stdout);
      console.error('stderr:', result.stderr);
      process.exit(1);
    }

    const shimPath = path.join(testPkgDir, 'bin', 'jixomd');
    if (!fs.existsSync(shimPath)) {
      console.error('FAIL: shim not created at', shimPath);
      console.error('stdout:', result.stdout);
      console.error('stderr:', result.stderr);
      process.exit(1);
    }
    const shim = fs.readFileSync(shimPath, 'utf8');
    if (!shim.includes('spawnSync')) {
      console.error('FAIL: shim does not forward to binary');
      process.exit(1);
    }

    const binPath = path.join(testPkgDir, 'bin', binaryName(plat.slug));
    if (!fs.existsSync(binPath)) {
      console.error('FAIL: binary not extracted at', binPath);
      console.error('stdout:', result.stdout);
      console.error('stderr:', result.stderr);
      process.exit(1);
    }

    console.log('PASS: install.js download + extract + shim flow works');
  });
}

main().catch((e) => {
  console.error('TEST ERROR:', e);
  process.exit(1);
});
