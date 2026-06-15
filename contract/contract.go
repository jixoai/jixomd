// Package contract defines the tool_call I/O shapes for the resolve command
// and the doc-mode boundaries. It is intentionally free of IO: it only carries
// JSON-serializable types and the packed-transport codec (gzip/zstd + base64).
//
// This is the "wire contract" of SPEC §4: directive arrays in, block arrays out.
// The pure core never imports this (core works on documents); the CLI imports
// it to drive resolve.
package contract

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/klauspost/compress/zstd"
)

// Directive is a single request item: one [target](@MODE) the host wants
// resolved. Mirrors a parsed directive without the source-span bookkeeping.
type Directive struct {
	ID        string              `json:"id"`
	Target    string              `json:"target"`
	Directive string              `json:"directive"` // mode, e.g. "FILE", "INJECT", "GIT_DIFF"
	Bang      bool                `json:"bang,omitempty"`
	Params    map[string][]string `json:"params,omitempty"`
}

// Block is a single response item: the fully formatted output (with
// START/END markers per SPEC §3) for the directive whose ID matches.
type Block struct {
	ID    string `json:"id"`
	Block string `json:"block"`
	// DataB64 carries raw bytes for binary content in packed mode (host opt-in).
	// Empty in raw mode, where binary content is skipped with a comment.
	DataB64 string `json:"data_b64,omitempty"`
}

// Algo is the compression algorithm for the packed transport.
type Algo string

const (
	AlgoGzip Algo = "gzip"
	AlgoZstd Algo = "zstd"
)

// EncodePacked wraps a JSON payload as base64(<algo>(json)). SPEC §4.3.
func EncodePacked(payload any, algo Algo) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	var comp bytes.Buffer
	switch algo {
	case AlgoGzip, "":
		w, err := gzip.NewWriterLevel(&comp, gzip.BestSpeed)
		if err != nil {
			return "", err
		}
		if _, err := w.Write(raw); err != nil {
			return "", err
		}
		if err := w.Close(); err != nil {
			return "", err
		}
	case AlgoZstd:
		enc, err := zstd.NewWriter(&comp)
		if err != nil {
			return "", err
		}
		if _, err := enc.Write(raw); err != nil {
			return "", err
		}
		if err := enc.Close(); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("contract: unknown algo %q", algo)
	}
	return base64.StdEncoding.EncodeToString(comp.Bytes()), nil
}

// DecodePacked reverses EncodePacked: base64 -> <algo> decompress -> JSON into v.
func DecodePacked(packed string, algo Algo, v any) error {
	comp, err := base64.StdEncoding.DecodeString(packed)
	if err != nil {
		return fmt.Errorf("contract: bad base64: %w", err)
	}
	var raw []byte
	switch algo {
	case AlgoGzip, "":
		r, err := gzip.NewReader(bytes.NewReader(comp))
		if err != nil {
			return err
		}
		raw = bytes.NewBuffer(nil).Bytes()
		buf := bytes.NewBuffer(nil)
		if _, err := buf.ReadFrom(r); err != nil {
			return err
		}
		r.Close()
		raw = buf.Bytes()
	case AlgoZstd:
		dec, err := zstd.NewReader(bytes.NewReader(comp))
		if err != nil {
			return err
		}
		raw, err = dec.DecodeAll(comp, nil)
		dec.Close()
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("contract: unknown algo %q", algo)
	}
	return json.Unmarshal(raw, v)
}
