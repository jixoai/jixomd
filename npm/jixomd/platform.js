'use strict';

// Platform detection: maps process.platform / process.arch to the
// {platform}-{arch} slug used in GitHub release asset names.
//
// Asset naming convention:
//   jixomd_<version>_<platform>_<arch>.tar.gz
//   jixomd_<version>_<platform>_<arch>.zip   (windows)

const SUPPORTED = new Set([
  'darwin-arm64',
  'darwin-amd64',
  'linux-arm64',
  'linux-amd64',
  'windows-arm64',
  'windows-amd64',
]);

function archMapping(nodeArch) {
  switch (nodeArch) {
    case 'arm64': return 'arm64';
    case 'x64': return 'amd64';
    case 'arm': return 'arm';
    case 'ia32':
    case 'x32': return '386';
    default: return nodeArch;
  }
}

function platformMapping(nodePlatform) {
  switch (nodePlatform) {
    case 'darwin': return 'darwin';
    case 'linux': return 'linux';
    case 'win32': return 'windows';
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
 * The asset file name for a given version + slug.
 * Windows uses .zip, everything else .tar.gz.
 */
function assetName(version, slug) {
  const ext = slug.startsWith('windows-') ? 'zip' : 'tar.gz';
  return `jixomd_${version}_${slug}.${ext}`;
}

/**
 * The binary name inside the archive (without extension for non-windows,
 * .exe for windows).
 */
function binaryName(slug) {
  return slug.startsWith('windows-') ? 'jixomd.exe' : 'jixomd';
}

module.exports = { detect, assetName, binaryName, SUPPORTED };
