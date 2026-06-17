'use strict';

// jixomd — JavaScript API.
//
// Spawns the native jixomd binary under the hood. The binary is provided by a
// per-platform optional dependency (@jixo/md-{os}-{arch}) installed alongside
// this package. For local development, set JIXOMD_BINARY_PATH to point at a
// locally-built Go binary.

const { spawnSync } = require('child_process');
const path = require('path');
const fs = require('fs');

/**
 * Resolve the native binary path.
 *
 * Resolution order:
 *  1. JIXOMD_BINARY_PATH env (dev escape hatch)
 *  2. require.resolve('@jixo/md-{slug}/{binary}') — the installed optional dep
 *  3. ../jixomd-{slug}/{binary} — local pnpm workspace (dev)
 */
function binaryPath() {
  const override = process.env.JIXOMD_BINARY_PATH;
  if (override) return override;

  const { detect, binaryName } = require('./platform');
  let slug, binName;
  try {
    const plat = detect();
    slug = plat.slug;
    binName = binaryName(plat.slug);
  } catch (_) {
    throw new Error(
      'jixomd: unsupported platform. Set JIXOMD_BINARY_PATH to a locally-built binary.'
    );
  }

  // 2. Resolve via node_modules (installed optional dependency).
  const pkgName = `@jixo/md-${slug}`;
  try {
    return require.resolve(`${pkgName}/${binName}`);
  } catch (_) {
    // Fall through to local workspace path.
  }

  // 3. Local workspace dev path (npm/jixomd-{slug}/jixomd).
  const local = path.join(__dirname, '..', `jixomd-${slug}`, binName);
  if (fs.existsSync(local)) return local;

  throw new Error(
    `jixomd: native binary not found for ${slug}. ` +
    'Ensure the platform package installed correctly, or set JIXOMD_BINARY_PATH.'
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
