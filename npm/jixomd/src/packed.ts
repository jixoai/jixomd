// packed.ts — encode/decode for the jixomd packed transport:
// base64(gzip|zstd(json)).
// Uses Node's built-in zlib for gzip; zstd requires Node 22+
// (zlib.zstdCompressSync) and falls back to gzip when unavailable.

import { gzipSync, gunzipSync, zstdCompressSync, zstdDecompressSync } from 'node:zlib';

export type Algo = 'gzip' | 'zstd';

/**
 * Encode a JSON-serializable value as base64(<algo>(json)).
 */
export function encodePacked(value: unknown, algo: Algo = 'gzip'): string {
  const json = Buffer.from(JSON.stringify(value));
  let comp: Buffer;
  if (algo === 'zstd' && typeof zstdCompressSync === 'function') {
    comp = zstdCompressSync(json);
  } else {
    comp = gzipSync(json);
  }
  return comp.toString('base64');
}

/**
 * Decode a base64(<algo>(json)) string back into a value.
 */
export function decodePacked<T = unknown>(packed: string, algo: Algo = 'gzip'): T {
  const comp = Buffer.from(packed, 'base64');
  let json: Buffer;
  if (algo === 'zstd' && typeof zstdDecompressSync === 'function') {
    json = zstdDecompressSync(comp);
  } else {
    json = gunzipSync(comp);
  }
  return JSON.parse(json.toString()) as T;
}
