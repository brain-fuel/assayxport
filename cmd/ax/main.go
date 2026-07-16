// Command ax (AssayXport) assays a source tree and writes a deterministic
// JSON manifest (assayxport.json + .assayxport/ shards) of its API and docs,
// plus a self-contained interactive explorer (assayxport.html).
//
//	ax assay .              write manifest + explorer at the project root
//	ax serve .              assay, serve the explorer, re-assay on save
//	ax watch .              re-assay on save, writing files each time
package main

import (
	"flag"
	"fmt"
	"os"

	"goforge.dev/assayxport/internal/emit"
	"goforge.dev/assayxport/internal/explorer"
	"goforge.dev/assayxport/internal/extract/golang"
	"goforge.dev/assayxport/internal/extract/registry"
	"goforge.dev/assayxport/internal/schema"
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

func usage() {
	fmt.Fprintln(os.Stderr, `usage: ax <command> [path] [flags]

commands:
  assay   write assayxport.json, .assayxport/ shards, and assayxport.html
  serve   assay and serve the explorer over HTTP (watches by default)
  watch   re-run assay whenever source files change

run "ax <command> -h" for that command's flags.`)
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("expected a subcommand")
	}
	cmd := args[0]
	rest := args[1:]
	switch cmd {
	case "assay":
		return runAssayCmd(rest)
	case "scan":
		// Deprecated alias for assay. Kept so existing scripts and docs
		// keep working; the notice goes to stderr so piped stdout stays
		// clean.
		fmt.Fprintln(os.Stderr, `ax: "scan" is now "assay"; the alias still works but prefer "ax assay"`)
		return runAssayCmd(rest)
	case "serve":
		return runServeCmd(rest)
	case "watch":
		return runWatchCmd(rest)
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown subcommand %q", cmd)
	}
}

// splitPath pulls an optional leading positional path out of args so
// "assay ./pkg --out /tmp/x" works alongside "assay --out /tmp/x ./pkg".
func splitPath(args []string) (string, []string) {
	path := "."
	if len(args) > 0 && args[0] != "--" && args[0] != "" && args[0][0] != '-' {
		path = args[0]
		args = args[1:]
	}
	return path, args
}

// assayOnce runs every selected extractor over path and returns the
// manifest. It is the single extraction entry point shared by assay,
// serve, and watch, so all three produce identical output for equal
// inputs.
func assayOnce(path string, langs stringsFlag, quiet bool) (schema.Index, map[string]schema.Shard, error) {
	exts, err := registry.Select(langs)
	if err != nil {
		return schema.Index{}, nil, err
	}
	pkgs, languages, warnings, err := registry.Run(exts, path)
	if err != nil {
		return schema.Index{}, nil, err
	}
	// A tolerated per-language failure (some other language still produced
	// output) is surfaced on stderr so a partial manifest is never silently
	// mistaken for a complete one.
	if !quiet {
		for _, w := range warnings {
			fmt.Fprintln(os.Stderr, "ax: warning:", w)
		}
	}
	// Derive module hint from the Go extractor if it was used. Module() is
	// only populated after Extract runs, so we read it from the same
	// extractor instance that registry.Run called Extract on.
	module := ""
	for _, e := range exts {
		if ge, ok := e.(*golang.Extractor); ok {
			module = ge.Module()
			break
		}
	}
	idx, shards := emit.Manifest(pkgs, module, languages)
	return idx, shards, nil
}

// writeAll writes the JSON manifest and, unless noHTML, the explorer.
func writeAll(outDir string, idx schema.Index, shards map[string]schema.Shard, noHTML bool) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := emit.WriteDir(outDir, idx, shards); err != nil {
		return err
	}
	if noHTML {
		return nil
	}
	combined, err := emit.Combined(idx, shards)
	if err != nil {
		return err
	}
	html := explorer.Render(combined, false)
	return os.WriteFile(outDir+string(os.PathSeparator)+"assayxport.html", html, 0o644)
}

func runAssayCmd(args []string) error {
	fs := flag.NewFlagSet("ax assay", flag.ContinueOnError)
	out := fs.String("out", "", "output directory (default: assay path)")
	stdout := fs.Bool("stdout", false, "print combined JSON to stdout; write no files")
	quiet := fs.Bool("quiet", false, "suppress progress on stderr")
	noHTML := fs.Bool("no-html", false, "skip writing assayxport.html")
	var langs stringsFlag
	fs.Var(&langs, "lang", "language to assay (repeatable; default: all)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: ax assay [path] [flags]")
		fs.PrintDefaults()
	}
	path, rest := splitPath(args)
	if err := fs.Parse(rest); err != nil {
		return err
	}
	// Allow flags-then-path ordering too.
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}

	idx, shards, err := assayOnce(path, langs, *quiet)
	if err != nil {
		return err
	}

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
	if err := writeAll(outDir, idx, shards, *noHTML); err != nil {
		return err
	}
	if !*quiet {
		artifacts := "assayxport.json + shards + assayxport.html"
		if *noHTML {
			artifacts = "assayxport.json + shards"
		}
		fmt.Fprintf(os.Stderr, "ax: wrote %d packages (%s) to %s\n", len(idx.Packages), artifacts, outDir)
	}
	return nil
}
