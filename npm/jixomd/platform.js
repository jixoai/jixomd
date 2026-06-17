'use strict';

// Platform detection: maps process.platform / process.arch to the slug used in
// the optionalDependencies package names (@jixo/md-{slug}).

const SUPPORTED = new Set([
  'darwin-arm64',
  'darwin-x64',
  'linux-arm64',
  'linux-x64',
  'win-arm64',
  'win-x64',
]);

function archMapping(nodeArch) {
  switch (nodeArch) {
    case 'arm64': return 'arm64';
    case 'x64': return 'x64';
    case 'arm': return 'arm';
    case 'ia32':
    case 'x32': return 'ia32';
    default: return nodeArch;
  }
}

function platformMapping(nodePlatform) {
  switch (nodePlatform) {
    case 'darwin': return 'darwin';
    case 'linux': return 'linux';
    case 'win32': return 'win';
    case 'freebsd': return 'freebsd';
    default: return nodePlatform;
  }
}

/**
 * Returns the {platform, arch, slug} for the current or overridden process.
 * Throws if the combination is not in the supported set.
 */
function detect(platform, arch) {
  platform = platform || process.platform;
  arch = arch || process.arch;
  const p = platformMapping(platform);
  const a = archMapping(arch);
  const slug = `${p}-${a}`;
  if (!SUPPORTED.has(slug)) {
    throw new Error(
      `jixomd: unsupported platform/arch: ${platform}/${arch} (slug ${slug}). ` +
      `Supported: ${Array.from(SUPPORTED).join(', ')}.`
    );
  }
  return { platform: p, arch: a, slug };
}

/**
 * The binary filename inside the platform package.
 */
function binaryName(slug) {
  return slug.startsWith('win-') ? 'jixomd.exe' : 'jixomd';
}

module.exports = { detect, binaryName, SUPPORTED };
