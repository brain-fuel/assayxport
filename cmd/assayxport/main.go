// Command assayxport scans a Go codebase and writes a deterministic JSON
// manifest (assayxport.json + .assayxport/ shards) of its API and docs.
package main

import (
	"flag"
	"fmt"
	"os"

	"goforge.dev/assayxport/internal/emit"
	"goforge.dev/assayxport/internal/extract/golang"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "assayxport:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("assayxport", flag.ContinueOnError)
	out := fs.String("out", "", "output directory (default: scan path)")
	stdout := fs.Bool("stdout", false, "print combined JSON to stdout; write no files")
	quiet := fs.Bool("quiet", false, "suppress progress on stderr")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: assayxport scan [path] [flags]")
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
	if len(remaining) > 0 && remaining[0] != "--" && remaining[0][0] != '-' {
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

	ex := golang.New()
	pkgs, err := ex.Extract(path)
	if err != nil {
		return err
	}
	idx, shards := emit.Manifest(pkgs, ex.Module(), []string{"go"})

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
		fmt.Fprintf(os.Stderr, "assayxport: wrote %d packages to %s\n", len(idx.Packages), outDir)
	}
	return nil
}
