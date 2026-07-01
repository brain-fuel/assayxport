package java

import (
	"os"
	"testing"

	"goforge.dev/assayxport/internal/schema"
)

func loadCU(t *testing.T, rel string) cuResult {
	t.Helper()
	src, err := os.ReadFile("testdata/proj/" + rel)
	if err != nil {
		t.Fatal(err)
	}
	res, err := compilationUnit(rel, src)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func symByID(syms []schema.Symbol, id string) *schema.Symbol {
	for i := range syms {
		if syms[i].ID == id {
			return &syms[i]
		}
	}
	return nil
}

func TestCompilationUnitPackage(t *testing.T) {
	res := loadCU(t, "com/foo/Bar.java")
	if res.PackageName != "com.foo" {
		t.Fatalf("PackageName = %q, want com.foo", res.PackageName)
	}
}

func TestCompilationUnitTypesAndMembers(t *testing.T) {
	res := loadCU(t, "com/foo/Bar.java")

	bar := symByID(res.Syms, "Bar")
	if bar == nil || bar.Kind != "type" || bar.TypeKind != "class" {
		t.Fatalf("Bar = %+v", bar)
	}
	if bar.Visibility != "public" || bar.VisibilityIdiom != "access-modifier" {
		t.Fatalf("Bar visibility = %q/%q", bar.Visibility, bar.VisibilityIdiom)
	}
	if len(bar.Annotations) != 1 || bar.Annotations[0] != "Deprecated" {
		t.Fatalf("Bar annotations = %v", bar.Annotations)
	}
	if bar.Signature == nil || len(bar.Signature.TypeParams) != 1 || bar.Signature.TypeParams[0].Name != "T" {
		t.Fatalf("Bar type params = %+v", bar.Signature)
	}
	if bar.Doc.Raw == "" || bar.Doc.Format != "javadoc" {
		t.Fatalf("Bar doc = %+v", bar.Doc)
	}

	ctor := symByID(res.Syms, "Bar.Bar")
	if ctor == nil || ctor.Kind != "constructor" {
		t.Fatalf("constructor = %+v", ctor)
	}

	get := symByID(res.Syms, "Bar.getCount")
	if get == nil || get.Kind != "method" || get.Visibility != "public" {
		t.Fatalf("getCount = %+v", get)
	}
	if len(get.Annotations) != 1 || get.Annotations[0] != "Override" {
		t.Fatalf("getCount annotations = %v", get.Annotations)
	}

	count := symByID(res.Syms, "Bar.count")
	if count == nil || count.Kind != "field" || count.Visibility != "private" || count.Type != "int" {
		t.Fatalf("count = %+v", count)
	}

	ratio := symByID(res.Syms, "Bar.RATIO")
	if ratio == nil || ratio.Visibility != "package-private" {
		t.Fatalf("RATIO = %+v (want package-private)", ratio)
	}

	inner := symByID(res.Syms, "Bar.Inner")
	if inner == nil || inner.TypeKind != "interface" || inner.Owner != "Bar" {
		t.Fatalf("Inner = %+v", inner)
	}
	ping := symByID(res.Syms, "Bar.Inner.ping")
	if ping == nil || ping.Kind != "method" || ping.Owner != "Bar.Inner" {
		t.Fatalf("ping = %+v", ping)
	}

	color := symByID(res.Syms, "Color")
	if color == nil || color.TypeKind != "enum" {
		t.Fatalf("Color = %+v", color)
	}
	red := symByID(res.Syms, "Color.RED")
	if red == nil || red.Kind != "enum-constant" {
		t.Fatalf("RED = %+v", red)
	}
}

func TestCompilationUnitMain(t *testing.T) {
	res := loadCU(t, "com/foo/Bar.java")
	if !res.HasMain || res.MainType != "Bar" {
		t.Fatalf("HasMain=%v MainType=%q", res.HasMain, res.MainType)
	}
	m := symByID(res.Syms, "Bar.main")
	if m == nil || m.Kind != "method" {
		t.Fatalf("main symbol = %+v", m)
	}
	static := false
	if m != nil && m.Signature != nil {
		for _, mod := range m.Signature.Modifiers {
			if mod == "static" {
				static = true
			}
		}
	}
	if !static {
		t.Fatalf("main missing static modifier: %+v", m.Signature)
	}
}
