// Platform detection: maps process.platform / process.arch to the slug used in
// the optionalDependencies package names (@jixo/md-{slug}).

export const SUPPORTED = new Set<string>([
  'darwin-arm64',
  'darwin-x64',
  'linux-arm64',
  'linux-x64',
  'win-arm64',
  'win-x64',
]);

function archMapping(nodeArch: string): string {
  switch (nodeArch) {
    case 'arm64': return 'arm64';
    case 'x64': return 'x64';
    case 'arm': return 'arm';
    case 'ia32':
    case 'x32': return 'ia32';
    default: return nodeArch;
  }
}

function platformMapping(nodePlatform: string): string {
  switch (nodePlatform) {
    case 'darwin': return 'darwin';
    case 'linux': return 'linux';
    case 'win32': return 'win';
    case 'freebsd': return 'freebsd';
    default: return nodePlatform;
  }
}

export interface PlatformInfo {
  platform: string;
  arch: string;
  slug: string;
}

/**
 * Detect the current platform. Throws if unsupported.
 */
export function detect(platform?: string, arch?: string): PlatformInfo {
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
export function binaryName(slug: string): string {
  return slug.startsWith('win-') ? 'jixomd.exe' : 'jixomd';
}
