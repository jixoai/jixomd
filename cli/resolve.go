package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/jixoai/jixomd/contract"
)

// resolveParams carries the resolve subcommand's parsed flags, captured in the
// command-tree closure and passed to runResolve.
type resolveParams struct {
	packed   bool
	algo     string
	baseFlag string
}

// runResolve handles resolve mode: JSON directive array in -> JSON block array
// out. Always JSON; --packed wraps both sides in base64(<algo>(json)). SPEC §4.3.
func (app *rootApp) runResolve(ctx context.Context, p resolveParams) error {
	raw, err := io.ReadAll(app.stdin)
	if err != nil {
		fmt.Fprintln(app.errw, "jixomd: read stdin:", err)
		app.exitCode = ExitUsage
		return nil
	}

	var reqs []contract.Directive
	if p.packed {
		if err := contract.DecodePacked(string(raw), contract.Algo(p.algo), &reqs); err != nil {
			fmt.Fprintln(app.errw, "jixomd: decode packed:", err)
			app.exitCode = ExitUsage
			return nil
		}
	} else {
		if err := json.Unmarshal(raw, &reqs); err != nil {
			fmt.Fprintln(app.errw, "jixomd: parse json:", err)
			app.exitCode = ExitUsage
			return nil
		}
	}

	blocks, err := resolveBatch(reqs, p.baseFlag)
	if err != nil {
		fmt.Fprintln(app.errw, "jixomd: resolve:", err)
		app.exitCode = ExitIO
		return nil
	}

	var payload string
	if p.packed {
		s, err := contract.EncodePacked(blocks, contract.Algo(p.algo))
		if err != nil {
			fmt.Fprintln(app.errw, "jixomd: encode packed:", err)
			app.exitCode = ExitInternal
			return nil
		}
		payload = s
	} else {
		b, err := json.Marshal(blocks)
		if err != nil {
			fmt.Fprintln(app.errw, "jixomd: marshal:", err)
			app.exitCode = ExitInternal
			return nil
		}
		payload = string(b)
	}
	fmt.Fprintln(app.out, payload)
	return nil
}
