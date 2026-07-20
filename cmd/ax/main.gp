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
	"math"
	"os"
	"runtime/debug"

	"goforge.dev/assayxport/internal/emit"
	"goforge.dev/assayxport/internal/explorer"
	"goforge.dev/assayxport/internal/extract/golang"
	"goforge.dev/assayxport/internal/extract/registry"
	"goforge.dev/assayxport/internal/schema"
)

// version is the released version, set for prebuilt binaries via
//
//	go build -ldflags "-X main.version=vX.Y.Z"
//
// When empty (the usual `go install goforge.dev/assayxport/cmd/ax@vX.Y.Z` path),
// resolvedVersion reads the tag back out of the embedded build info.
var version = ""

// resolvedVersion reports ax's version: the ldflags override if set, else the
// module version stamped into the binary by `go install ...@vX.Y.Z`, else
// "(devel)" for a plain `go build`/`go run` from a checkout.
func resolvedVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "(devel)"
}

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
  version print the ax version (also: ax --version)

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
	case "version", "--version", "-v":
		fmt.Println("ax", resolvedVersion())
		return nil
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

// capAssayMemory sets a soft memory limit for the streaming assay so the GC
// keeps its transient parse garbage in check -- on a very large repo the peak
// RSS roughly halves. Streaming keeps live data small (index metadata plus a few
// in-flight shards), so the limit caps garbage without thrashing. A limit the
// user set via GOMEMLIMIT is respected rather than overridden. The limit is a
// soft target, never a hard cap, so a repo that genuinely needs more slows down
// rather than failing.
func capAssayMemory() {
	if debug.SetMemoryLimit(-1) == math.MaxInt64 { // unset by the user
		debug.SetMemoryLimit(1 << 30) // 1 GiB
	}
}

// assayToDir streams the manifest for path directly into dir, one shard file at
// a time, and returns the index. Peak memory is the index metadata plus the
// in-flight packages -- not the whole symbol graph -- so it scales to very large
// repos. It is the extraction path `ax serve` (lazy mode) uses; the in-RAM
// assayOnce remains for --stdout and --no-wasm, which need the whole manifest at
// once anyway. The produced files are identical to assayOnce + emit.WriteDir.
func assayToDir(path string, langs stringsFlag, quiet bool, dir string) (schema.Index, error) {
	capAssayMemory()
	exts, err := registry.Select(langs)
	if err != nil {
		return schema.Index{}, err
	}
	w, err := emit.NewWriter(dir)
	if err != nil {
		return schema.Index{}, err
	}
	languages, warnings, err := registry.RunStream(exts, path, w.Add)
	if err != nil {
		return schema.Index{}, err
	}
	if !quiet {
		for _, wn := range warnings {
			fmt.Fprintln(os.Stderr, "ax: warning:", wn)
		}
	}
	module := ""
	for _, e := range exts {
		if ge, ok := e.(*golang.Extractor); ok {
			module = ge.Module()
			break
		}
	}
	return w.Finalize(module, languages)
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

	if *stdout {
		idx, shards, err := assayOnce(path, langs, *quiet)
		if err != nil {
			return err
		}
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

	// --no-html writes only the JSON manifest, so stream it straight to disk and
	// never hold the whole symbol graph in RAM -- this is what lets a repo the
	// size of Kafka be assayed without a multi-gigabyte peak. Producing the HTML
	// explorer needs the whole manifest inlined, so that path stays in-RAM.
	if *noHTML {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return err
		}
		idx, err := assayToDir(path, langs, *quiet, outDir)
		if err != nil {
			return err
		}
		if !*quiet {
			fmt.Fprintf(os.Stderr, "ax: wrote %d packages (assayxport.json + shards) to %s\n", len(idx.Packages), outDir)
		}
		return nil
	}

	idx, shards, err := assayOnce(path, langs, *quiet)
	if err != nil {
		return err
	}
	if err := writeAll(outDir, idx, shards, false); err != nil {
		return err
	}
	if !*quiet {
		fmt.Fprintf(os.Stderr, "ax: wrote %d packages (assayxport.json + shards + assayxport.html) to %s\n", len(idx.Packages), outDir)
	}
	return nil
}
