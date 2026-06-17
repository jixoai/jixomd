// jixomd — TypeScript SDK.
//
// Spawns the native jixomd binary under the hood. The binary is provided by a
// per-platform optional dependency (@jixo/md-{os}-{arch}). For local dev, set
// JIXOMD_BINARY_PATH to point at a locally-built Go binary.

import { spawnSync } from 'node:child_process';
import path from 'node:path';
import fs from 'node:fs';
import { detect, binaryName } from './platform.js';
import { encodePacked, decodePacked, type Algo } from './packed.js';

export { detect, binaryName } from './platform.js';
export { encodePacked, decodePacked, type Algo } from './packed.js';

// ── Types ──

export interface ExpandOptions {
  baseDir?: string;
  maxDepth?: number;
  output?: string; // expandFile only
}

export interface ResolveOptions {
  baseDir?: string;
  packed?: boolean;
  algo?: Algo;
}

export type ParamValue = string | string[] | number | boolean;

export interface Directive {
  id: string;
  target: string;
  directive: string; // FILE, INJECT, FILE_LIST, FILE_TREE, GIT_FILE, GIT_DIFF
  bang?: boolean;
  params?: Record<string, ParamValue>;
}

export interface Block {
  id: string;
  block: string;
}

// ── Binary resolution ──

/**
 * Resolve the native binary path.
 *
 * Resolution order:
 *  1. JIXOMD_BINARY_PATH env (dev escape hatch)
 *  2. require.resolve('@jixo/md-{slug}/{binary}') — installed optional dep
 *  3. ../jixomd-{slug}/{binary} — local workspace (dev)
 */
export function binaryPath(): string {
  const override = process.env.JIXOMD_BINARY_PATH;
  if (override) return override;

  let slug: string;
  let binName: string;
  try {
    const plat = detect();
    slug = plat.slug;
    binName = binaryName(plat.slug);
  } catch {
    throw new Error(
      'jixomd: unsupported platform. Set JIXOMD_BINARY_PATH to a locally-built binary.'
    );
  }

  // 2. Resolve via node_modules (installed optional dependency).
  const pkgName = `@jixo/md-${slug}`;
  try {
    return require.resolve(`${pkgName}/${binName}`);
  } catch {
    // Fall through to local workspace path.
  }

  // 3. Local workspace dev path.
  const local = path.join(__dirname, '..', `jixomd-${slug}`, binName);
  if (fs.existsSync(local)) return local;

  throw new Error(
    `jixomd: native binary not found for ${slug}. ` +
    'Ensure the platform package installed correctly, or set JIXOMD_BINARY_PATH.'
  );
}

// ── Public API ──

/**
 * Expand a Markdown document (doc mode). Returns the expanded text.
 */
export function expand(doc: string, opts: ExpandOptions = {}): string {
  const args: string[] = ['-'];
  if (opts.baseDir) args.push('--base', opts.baseDir);
  if (opts.maxDepth) args.push('--max-depth', String(opts.maxDepth));
  const result = spawnSync(binaryPath(), args, { input: doc, encoding: 'utf8' });
  if (result.status !== 0) {
    throw new Error(`jixomd expand failed (exit ${result.status}): ${result.stderr || ''}`);
  }
  return result.stdout;
}

/**
 * Expand a Markdown file (doc mode). Returns the expanded text.
 */
export function expandFile(filePath: string, opts: ExpandOptions = {}): string {
  const args: string[] = [filePath];
  if (opts.baseDir) args.push('--base', opts.baseDir);
  if (opts.maxDepth) args.push('--max-depth', String(opts.maxDepth));
  if (opts.output) args.push('--output', opts.output);
  const result = spawnSync(binaryPath(), args, { encoding: 'utf8' });
  if (result.status !== 0) {
    throw new Error(`jixomd expand failed (exit ${result.status}): ${result.stderr || ''}`);
  }
  return result.stdout;
}

/**
 * Resolve a batch of directives (resolve mode). Returns an array of blocks.
 */
export function resolve(directives: Directive[], opts: ResolveOptions = {}): Block[] {
  const args: string[] = ['resolve'];
  if (opts.baseDir) args.push('--base', opts.baseDir);
  if (opts.packed) {
    args.push('--packed');
    if (opts.algo) args.push('--algo', opts.algo);
  }
  let input: string;
  if (opts.packed) {
    input = encodePacked(directives, opts.algo || 'gzip');
  } else {
    input = JSON.stringify(directives);
  }
  const result = spawnSync(binaryPath(), args, { input, encoding: 'utf8' });
  if (result.status !== 0) {
    throw new Error(`jixomd resolve failed (exit ${result.status}): ${result.stderr || ''}`);
  }
  if (opts.packed) {
    return decodePacked<Block[]>(result.stdout.trim(), opts.algo || 'gzip');
  }
  return JSON.parse(result.stdout.trim()) as Block[];
}
