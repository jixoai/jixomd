'use strict';

// packed.js — encode/decode for the jixomd packed transport (base64(gzip|zstd(json))).
// Uses Node's built-in zlib for gzip; zstd requires Node 22+ (zlib.zstdCompress)
// and falls back to gzip when unavailable.

const zlib = require('zlib');

/**
 * Encode a JSON-serializable value as base64(<algo>(json)).
 * @param {*} value
 * @param {'gzip'|'zstd'} algo
 * @returns {string}
 */
function encodePacked(value, algo) {
  const json = Buffer.from(JSON.stringify(value));
  let comp;
  if (algo === 'zstd' && typeof zlib.zstdCompressSync === 'function') {
    comp = zlib.zstdCompressSync(json);
  } else {
    comp = zlib.gzipSync(json);
  }
  return comp.toString('base64');
}

/**
 * Decode a base64(<algo>(json)) string back into a value.
 * @param {string} packed
 * @param {'gzip'|'zstd'} algo
 * @returns {*}
 */
function decodePacked(packed, algo) {
  const comp = Buffer.from(packed, 'base64');
  let json;
  if (algo === 'zstd' && typeof zlib.zstdDecompressSync === 'function') {
    json = zlib.zstdDecompressSync(comp);
  } else {
    json = zlib.gunzipSync(comp);
  }
  return JSON.parse(json.toString());
}

module.exports = { encodePacked, decodePacked };
