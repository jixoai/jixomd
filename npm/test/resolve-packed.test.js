'use strict';

// Test that the JS API resolve() honors opts.packed / opts.algo (issue 006).
// Uses JIXOMD_BINARY_PATH to point at a locally-built binary.
// Run with: node test/resolve-packed.test.js

const { resolve } = require('../jixomd/dist/index.js');
const path = require('path');
const fs = require('fs');
const os = require('os');

function main() {
  // The test needs a real binary. Skip if not available.
  const bin = process.env.JIXOMD_BINARY_PATH;
  if (!bin || !fs.existsSync(bin)) {
    console.log('SKIP: set JIXOMD_BINARY_PATH to test the packed resolve flow');
    return;
  }

  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'jixomd-packed-'));
  fs.writeFileSync(path.join(dir, 'a.txt'), 'PACKED-CONTENT');

  // Raw mode (default).
  const raw = resolve(
    [{ id: 'd1', target: 'a.txt', directive: 'FILE' }],
    { baseDir: dir }
  );
  if (!Array.isArray(raw) || raw.length !== 1) {
    console.error('FAIL: raw resolve did not return 1 block:', raw);
    process.exit(1);
  }
  if (!raw[0].block.includes('PACKED-CONTENT')) {
    console.error('FAIL: raw block missing content:', raw[0].block);
    process.exit(1);
  }

  // Packed mode (gzip).
  const packed = resolve(
    [{ id: 'd1', target: 'a.txt', directive: 'FILE' }],
    { baseDir: dir, packed: true, algo: 'gzip' }
  );
  if (!Array.isArray(packed) || packed.length !== 1) {
    console.error('FAIL: packed resolve did not return 1 block:', packed);
    process.exit(1);
  }
  if (!packed[0].block.includes('PACKED-CONTENT')) {
    console.error('FAIL: packed block missing content:', packed[0].block);
    process.exit(1);
  }

  console.log('PASS: resolve() honors packed + algo (issue 006)');
}

main();
