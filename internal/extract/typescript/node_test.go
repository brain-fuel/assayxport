package typescript

import "testing"

// TestNodeEntrypoints covers the Node entrypoint semantics: a package.json bin
// target (string and map forms, root and workspace manifests) and a node
// shebang both mark the module and its top-level main() as entrypoints, with
// the npm bin winning over the shebang for the invocation hint.
func TestNodeEntrypoints(t *testing.T) {
	ps, err := New().Extract("testdata/nodeproj")
	if err != nil {
		t.Fatal(err)
	}

	cli := pkgByID(ps, "src/cli")
	if cli == nil || !cli.IsEntrypoint {
		t.Fatalf("src/cli should be an entrypoint (bin), got %+v", cli)
	}
	if cli.Invocation == nil || cli.Invocation.Kind != "bin" || cli.Invocation.How != "npx nodeproj" {
		t.Errorf("src/cli invocation = %+v (want bin / npx nodeproj)", cli.Invocation)
	}
	if m := symByID(cli, "main"); m == nil || !m.IsEntrypoint || m.Invocation == nil {
		t.Errorf("src/cli main should mirror the entrypoint, got %+v", m)
	}

	tool := pkgByID(ps, "src/tool")
	if tool == nil || !tool.IsEntrypoint || tool.Invocation == nil ||
		tool.Invocation.Kind != "script" || tool.Invocation.How != "node src/tool.mjs" {
		t.Errorf("src/tool (shebang) invocation = %+v (want script / node src/tool.mjs)", tool.Invocation)
	}

	run := pkgByID(ps, "packages/helper/run")
	if run == nil || !run.IsEntrypoint || run.Invocation == nil || run.Invocation.How != "npx helperctl" {
		t.Errorf("workspace bin map entry = %+v (want npx helperctl)", run.Invocation)
	}

	if idx := pkgByID(ps, "src/index"); idx == nil || idx.IsEntrypoint {
		t.Errorf("src/index is an entry module, not an entrypoint: %+v", idx)
	}
}

// TestNodeEntryDocs: a package entry module (exports map) with no doc of its
// own inherits its package.json description.
func TestNodeEntryDocs(t *testing.T) {
	ps, err := New().Extract("testdata/nodeproj")
	if err != nil {
		t.Fatal(err)
	}
	if idx := pkgByID(ps, "src/index"); idx == nil || idx.Doc != "Fixture package for Node semantics" {
		t.Errorf("src/index doc = %+v", idx)
	}
	if leg := pkgByID(ps, "src/legacy"); leg == nil || leg.Doc != "Fixture package for Node semantics" {
		t.Errorf("src/legacy (exports subpath) doc = %+v", leg)
	}
	if tool := pkgByID(ps, "src/tool"); tool == nil || tool.Doc != "" {
		t.Errorf("src/tool is not an entry module; doc = %q", tool.Doc)
	}
}

// TestCommonJSExports covers exports.name, module.exports.name, and wholesale
// module.exports = {...} assignment, including the mixed-mechanism case.
func TestCommonJSExports(t *testing.T) {
	ps, err := New().Extract("testdata/nodeproj")
	if err != nil {
		t.Fatal(err)
	}

	leg := pkgByID(ps, "src/legacy")
	greet := symByID(leg, "greet")
	if greet == nil || greet.Kind != "function" || greet.Visibility != "exported" ||
		greet.VisibilityIdiom != "commonjs-export" {
		t.Errorf("exports.greet = %+v", greet)
	}
	if twice := symByID(leg, "twice"); twice == nil || twice.Kind != "function" {
		t.Errorf("module.exports.twice = %+v", twice)
	}

	whole := pkgByID(ps, "src/whole")
	if alpha := symByID(whole, "alpha"); alpha == nil || alpha.Kind != "const" ||
		alpha.VisibilityIdiom != "commonjs-export" {
		t.Errorf("module.exports={alpha} = %+v", alpha)
	}
	if beta := symByID(whole, "beta"); beta == nil || beta.Kind != "function" {
		t.Errorf("module.exports={beta: fn} = %+v", beta)
	}
}

// TestDefaultExports: `export default <identifier>` records a default symbol
// with its referent; `export default function (...)` extracts the anonymous
// callable as `default` with its signature.
func TestDefaultExports(t *testing.T) {
	ps, err := New().Extract("testdata/nodeproj")
	if err != nil {
		t.Fatal(err)
	}

	idx := pkgByID(ps, "src/index")
	def := symByID(idx, "default")
	if def == nil || def.Kind != "const" || def.Visibility != "exported" || def.Underlying != "impl" {
		t.Errorf("export default impl = %+v", def)
	}

	anon := pkgByID(ps, "src/anon")
	adef := symByID(anon, "default")
	if adef == nil || adef.Kind != "function" || adef.Signature == nil ||
		len(adef.Signature.Params) != 1 || adef.Signature.Params[0].Type != "number" {
		t.Errorf("export default function = %+v", adef)
	}
}

// TestMemberVisibility: private/protected modifiers and #-private names are
// honored on methods (not only fields), and names that merely start with the
// word "private" stay public.
func TestMemberVisibility(t *testing.T) {
	ps, err := New().Extract("testdata/nodeproj")
	if err != nil {
		t.Fatal(err)
	}
	idx := pkgByID(ps, "src/index")
	cases := map[string]string{
		"Vault.#combo":    "private",
		"Vault.open":      "private",
		"Vault.peek":      "protected",
		"Vault.privateer": "public",
		"Vault.render":    "public",
	}
	for id, want := range cases {
		if s := symByID(idx, id); s == nil || s.Visibility != want {
			t.Errorf("%s visibility = %+v (want %s)", id, s, want)
		}
	}
}
