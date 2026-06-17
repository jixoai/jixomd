'use strict';

// Test that index.js binaryPath() resolves the native binary correctly via
// the platform-package fallback path (local workspace dir). In CI, a host
// binary is built into npm/jixomd-{slug}/jixomd before this test runs.
// Run with: node test/resolve-platform.test.js

const { binaryPath } = require('../jixomd');
const fs = require('fs');

function main() {
  // JIXOMD_BINARY_PATH takes priority — clear it so we test the real resolution.
  const saved = process.env.JIXOMD_BINARY_PATH;
  delete process.env.JIXOMD_BINARY_PATH;

  let bin;
  try {
    bin = binaryPath();
  } catch (e) {
    console.error('FAIL: binaryPath() threw:', e.message);
    process.exit(1);
  } finally {
    if (saved !== undefined) process.env.JIXOMD_BINARY_PATH = saved;
  }

  if (!fs.existsSync(bin)) {
    console.error('FAIL: resolved binary does not exist:', bin);
    process.exit(1);
  }

  console.log('PASS: binaryPath() resolved to', bin);
}

main();
