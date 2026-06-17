#!/usr/bin/env node
// CLI entry shim. Resolves the native binary and forwards all args.
import { spawnSync } from 'node:child_process';
import { binaryPath } from './index.js';

const bin = binaryPath();
const result = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
process.exit(result.status || 0);
