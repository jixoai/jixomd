package cli

import "strings"

// docBoolFlags is the set of doc-mode flags that do NOT take a value.
// Every other recognized flag consumes the next token as its value.
var docBoolFlags = map[string]bool{
	"watch": true,
}

// reorderFlags moves recognized doc-mode flags (and their values) before any
// positional arguments in args. This works around Go's flag package, which
// stops parsing at the first non-flag token, so users can write
// `jixomd file.md --watch --base x` naturally.
func reorderFlags(args []string) []string {
	var flags, positionals []string
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "-" || !strings.HasPrefix(a, "-") {
			positionals = append(positionals, a)
			i++
			continue
		}
		name := strings.TrimLeft(a, "-")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			// -name=value: self-contained token.
			flags = append(flags, a)
			i++
			continue
		}
		if docBoolFlags[name] {
			flags = append(flags, a)
			i++
			continue
		}
		// Value flag: consume this token and the next as its value.
		flags = append(flags, a)
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i += 2
		} else {
			i++
		}
	}
	return append(flags, positionals...)
}
