'use strict';

// postinstall downloader for the jixomd binary.
//
// Fetches the platform-appropriate binary from GitHub Releases and extracts it
// into this package's bin/ directory. Designed to work behind corporate
// proxies and in regions where github.com is slow/blocked.
//
// Environment variables (all optional):
//   JIXOMD_VERSION   - pin a version (default: the version in package.json)
//   JIXOMD_REPO      - override owner/repo, e.g. "myorg/jixomd-fork"
//   JIXOMD_MIRROR    - base URL replacing https://github.com (see below)
//   JIXOMD_BINARY_PATH - skip download; use a pre-built binary at this path
//   HTTPS_PROXY / HTTP_PROXY / NO_PROXY - standard proxy env (honored by the
//                      https-proxy-agent fallback, and by node fetch in newer
//                      runtimes)
//   JIXOMD_SKIP_DOWNLOAD - "1"/"true" to skip entirely (offline/dev)
//
// JIXOMD_MIRROR: if set, download URLs become
//   ${JIXOMD_MIRROR}/<owner>/<repo>/releases/download/<version>/<asset>
// This lets users point at a mirror (e.g. a ghproxy, a self-hosted artifact
// server, or a Gitea instance) without touching the proxy stack.

const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');
const { detect, assetName, binaryName } = require('./platform');

const pkg = require('./package.json');

function envTrue(name) {
  const v = process.env[name];
  return v === '1' || v === 'true' || v === 'yes';
}

function log(msg) {
  process.stdout.write(`[jixomd] ${msg}\n`);
}

function warn(msg) {
  process.stderr.write(`[jixomd] WARN: ${msg}\n`);
}

async function main() {
  // Skip entirely?
  if (envTrue('JIXOMD_SKIP_DOWNLOAD')) {
    log('JIXOMD_SKIP_DOWNLOAD set; skipping binary download.');
    return;
  }

  // Pre-built binary override.
  const override = process.env.JIXOMD_BINARY_PATH;
  if (override) {
    log(`JIXOMD_BINARY_PATH set; linking to ${override}`);
    installShim(override);
    return;
  }

  const version = process.env.JIXOMD_VERSION || pkg.version;
  const repo = process.env.JIXOMD_REPO || `${pkg.jixomd.owner}/${pkg.jixomd.repo}`;
  const mirror = process.env.JIXOMD_MIRROR || 'https://github.com';

  let plat;
  try {
    plat = detect();
  } catch (e) {
    warn(e.message);
    warn('The native binary will not be available. JS fallback only.');
    return;
  }

  const asset = assetName(version, plat.slug);
  const url = `${mirror}/${repo}/releases/download/v${version}/${asset}`;
  log(`Downloading ${url}`);

  const destDir = path.join(__dirname, 'bin');
  fs.mkdirSync(destDir, { recursive: true });
  const archivePath = path.join(destDir, asset);

  try {
    await download(url, archivePath);
    extract(archivePath, destDir, plat.slug);
    // Clean up the archive.
    try { fs.unlinkSync(archivePath); } catch (_) {}
    const binPath = path.join(destDir, binaryName(plat.slug));
    // Ensure executable.
    try { fs.chmodSync(binPath, 0o755); } catch (_) {}
    log(`Installed binary to ${binPath}`);
    // Install the version-agnostic shim so `jixomd` on PATH resolves here.
    installShim(binPath);
  } catch (e) {
    warn(`Download failed: ${e.message}`);
    warn('You can set JIXOMD_BINARY_PATH to a locally-built binary, or re-run later.');
    // Don't fail the install — the JS fallback still works for some uses.
  }
}

/**
 * Download a URL to a file, honoring HTTPS_PROXY/HTTP_PROXY.
 * Uses global fetch when available (Node 18+), falls back to https module.
 */
async function download(url, dest) {
  const proxy = process.env.HTTPS_PROXY || process.env.https_proxy || process.env.HTTP_PROXY || process.env.http_proxy;

  // Node 18+ has global fetch with proxy-less support; for proxies we still
  // need an agent. To keep this dependency-free, we shell out to curl when a
  // proxy is set (curl respects *_proxy env natively). Otherwise use fetch.
  if (proxy) {
    try {
      execFileSync('curl', ['-fsSL', '--proxy', proxy, '-o', dest, url], { stdio: 'inherit' });
      return;
    } catch (e) {
      // fall through to fetch
    }
  }

  if (typeof fetch === 'function') {
    const res = await fetch(url, { redirect: 'follow' });
    if (!res.ok) throw new Error(`HTTP ${res.status} for ${url}`);
    const buf = Buffer.from(await res.arrayBuffer());
    fs.writeFileSync(dest, buf);
    return;
  }

  // Legacy: https.get (no proxy support).
  await new Promise((resolve, reject) => {
    const https = require('https');
    const file = fs.createWriteStream(dest);
    const get = (u) => {
      https.get(u, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          res.resume();
          get(res.headers.location);
          return;
        }
        if (res.statusCode !== 200) {
          reject(new Error(`HTTP ${res.statusCode} for ${url}`));
          return;
        }
        res.pipe(file);
        file.on('finish', () => { file.close(resolve); });
      }).on('error', reject);
    };
    get(url);
  });
}

/**
 * Extract a .tar.gz or .zip archive.
 */
function extract(archivePath, destDir, slug) {
  if (archivePath.endsWith('.zip')) {
    // Windows: use tar (available on Win10+) or PowerShell Expand-Archive.
    try {
      execFileSync('tar', ['-xf', archivePath, '-C', destDir], { stdio: 'inherit' });
    } catch (_) {
      execFileSync('powershell', ['-NoProfile', '-Command',
        `Expand-Archive -Force -Path '${archivePath}' -DestinationPath '${destDir}'`],
        { stdio: 'inherit' });
    }
  } else {
    execFileSync('tar', ['-xzf', archivePath, '-C', destDir], { stdio: 'inherit' });
  }
}

/**
 * Write bin/jixomd (the npm `bin` entry) as a small Node shim that execs the
 * downloaded platform binary. This keeps PATH resolution consistent across
 * platforms (no .exe suffix issues on Windows).
 */
function installShim(binaryPath) {
  const shimDir = path.join(__dirname, 'bin');
  fs.mkdirSync(shimDir, { recursive: true });
  const shimPath = path.join(shimDir, 'jixomd');
  const shim = `#!/usr/bin/env node
'use strict';
// Auto-generated shim. Forwards all args to the native jixomd binary.
const { spawnSync } = require('child_process');
const path = require('path');
const bin = ${JSON.stringify(binaryPath)};
const result = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
process.exit(result.status || 0);
`;
  fs.writeFileSync(shimPath, shim, { mode: 0o755 });
  try { fs.chmodSync(shimPath, 0o755); } catch (_) {}
}

main().catch((e) => {
  warn(`install error: ${e.message}`);
  process.exit(0); // non-fatal
});
