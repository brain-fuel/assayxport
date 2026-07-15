// Command ax (AssayXport) scans a source tree and writes a deterministic JSON
// manifest (assayxport.json + .assayxport/ shards) of its API and docs.
package main

import (
	"flag"
	"fmt"
	"os"

	"goforge.dev/assayxport/internal/emit"
	"goforge.dev/assayxport/internal/extract/golang"
	"goforge.dev/assayxport/internal/extract/registry"
)

// stringsFlag is a repeatable string flag that collects values into a slice.
type stringsFlag []string

func (s *stringsFlag) String() string { return fmt.Sprintf("%v", []string(*s)) }
func (s *stringsFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ax:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("ax", flag.ContinueOnError)
	out := fs.String("out", "", "output directory (default: scan path)")
	stdout := fs.Bool("stdout", false, "print combined JSON to stdout; write no files")
	quiet := fs.Bool("quiet", false, "suppress progress on stderr")
	var langs stringsFlag
	fs.Var(&langs, "lang", "language to scan (repeatable; default: all)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: ax scan [path] [flags]")
		fs.PrintDefaults()
	}
	if len(args) == 0 || args[0] != "scan" {
		fs.Usage()
		return fmt.Errorf("expected subcommand \"scan\"")
	}
	// Pull out the optional positional path before flag parsing so
	// "scan ./pkg --out /tmp/x" works alongside "scan --out /tmp/x ./pkg".
	remaining := args[1:]
	path := "."
	if len(remaining) > 0 && remaining[0] != "--" && remaining[0] != "" && remaining[0][0] != '-' {
		path = remaining[0]
		remaining = remaining[1:]
	}
	if err := fs.Parse(remaining); err != nil {
		return err
	}
	// Allow flags-then-path ordering too.
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}

	exts, err := registry.Select(langs)
	if err != nil {
		return err
	}
	pkgs, languages, warnings, err := registry.Run(exts, path)
	if err != nil {
		return err
	}
	// A tolerated per-language failure (some other language still produced
	// output) is surfaced on stderr so a partial manifest is never silently
	// mistaken for a complete one.
	if !*quiet {
		for _, w := range warnings {
			fmt.Fprintln(os.Stderr, "ax: warning:", w)
		}
	}

	// Derive module hint from the Go extractor if it was used.
	// Module() is only populated after Extract runs, so we read it from
	// the same extractor instance that registry.Run called Extract on.
	module := ""
	for _, e := range exts {
		if ge, ok := e.(*golang.Extractor); ok {
			module = ge.Module()
			break
		}
	}

	idx, shards := emit.Manifest(pkgs, module, languages)

	if *stdout {
		b, err := emit.Combined(idx, shards)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		return err
	}

	outDir := *out
	if outDir == "" {
		outDir = path
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := emit.WriteDir(outDir, idx, shards); err != nil {
		return err
	}
	if !*quiet {
		fmt.Fprintf(os.Stderr, "ax: wrote %d packages to %s\n", len(idx.Packages), outDir)
	}
	return nil
}
