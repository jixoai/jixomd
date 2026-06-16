'use strict';

// jixomd — JavaScript API.
//
// Spawns the native jixomd binary under the hood. The native binary is
// downloaded at install time (see install.js); if it is missing (e.g. an
// unsupported platform, or JIXOMD_SKIP_DOWNLOAD), calls will throw with a
// helpful message.

const { spawnSync } = require('child_process');
const path = require('path');
const fs = require('fs');

function binaryPath() {
  // The postinstall script writes bin/jixomd (a shim) OR the platform binary.
  // JIXOMD_BINARY_PATH overrides everything.
  const override = process.env.JIXOMD_BINARY_PATH;
  if (override) return override;

  const binDir = path.join(__dirname, 'bin');
  const shim = path.join(binDir, 'jixomd');
  if (fs.existsSync(shim)) return shim;

  // Fall back to a platform-named binary if the shim wasn't written.
  const { detect, binaryName } = require('./platform');
  try {
    const plat = detect();
    const named = path.join(binDir, binaryName(plat.slug));
    if (fs.existsSync(named)) return named;
  } catch (_) {}

  throw new Error(
    'jixomd: native binary not found. Set JIXOMD_BINARY_PATH to a locally-built ' +
    'binary, or re-run npm install.'
  );
}

/**
 * Expand a Markdown document (doc mode). Returns the expanded text.
 *
 * @param {string} doc - the Markdown source
 * @param {object} [opts] - { baseDir, maxDepth }
 * @returns {string}
 */
function expand(doc, opts) {
  opts = opts || {};
  const args = ['-'];
  if (opts.baseDir) { args.push('--base', opts.baseDir); }
  if (opts.maxDepth) { args.push('--max-depth', String(opts.maxDepth)); }
  const result = spawnSync(binaryPath(), args, { input: doc, encoding: 'utf8' });
  if (result.status !== 0) {
    throw new Error(`jixomd expand failed (exit ${result.status}): ${result.stderr || ''}`);
  }
  return result.stdout;
}

/**
 * Expand a Markdown file (doc mode). Returns the expanded text.
 *
 * @param {string} filePath - path to the .md file
 * @param {object} [opts] - { baseDir, maxDepth, output }
 * @returns {string}
 */
function expandFile(filePath, opts) {
  opts = opts || {};
  const args = [filePath];
  if (opts.baseDir) { args.push('--base', opts.baseDir); }
  if (opts.maxDepth) { args.push('--max-depth', String(opts.maxDepth)); }
  if (opts.output) { args.push('--output', opts.output); }
  const result = spawnSync(binaryPath(), args, { encoding: 'utf8' });
  if (result.status !== 0) {
    throw new Error(`jixomd expand failed (exit ${result.status}): ${result.stderr || ''}`);
  }
  return result.stdout;
}

/**
 * Resolve a batch of directives (resolve mode). Returns an array of blocks.
 *
 * @param {Array} directives - [{id, target, directive, params}]
 * @param {object} [opts] - { baseDir, packed, algo }
 * @returns {Array} blocks - [{id, block}]
 */
function resolve(directives, opts) {
  opts = opts || {};
  const args = ['resolve'];
  if (opts.baseDir) { args.push('--base', opts.baseDir); }
  if (opts.packed) {
    args.push('--packed');
    if (opts.algo) { args.push('--algo', opts.algo); }
  }
  // In packed mode the input/output is base64(<algo>(json)); use the contract
  // codec so callers get the same wire format the CLI speaks (issue 006).
  let input;
  if (opts.packed) {
    const { encodePacked } = require('./packed');
    input = encodePacked(directives, opts.algo || 'gzip');
  } else {
    input = JSON.stringify(directives);
  }
  const result = spawnSync(binaryPath(), args, { input, encoding: 'utf8' });
  if (result.status !== 0) {
    throw new Error(`jixomd resolve failed (exit ${result.status}): ${result.stderr || ''}`);
  }
  if (opts.packed) {
    const { decodePacked } = require('./packed');
    return decodePacked(result.stdout.trim(), opts.algo || 'gzip');
  }
  return JSON.parse(result.stdout.trim());
}

module.exports = { expand, expandFile, resolve, binaryPath };
