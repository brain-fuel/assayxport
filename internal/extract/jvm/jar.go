package jvm

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"

	"goforge.dev/assayxport/internal/schema"
)

const DefaultJavaRelease = 25

type Options struct {
	JavaRelease  int
	ArtifactName string
}
type candidate struct {
	file    *zip.File
	logical string
	release int
}

// ExtractJAR performs one archive traversal, selects multi-release definitions,
// parses declarations directly from entries, and adapts them to schema.Package.
func ExtractJAR(jarPath string, opt Options) ([]schema.Package, error) {
	zr, err := zip.OpenReader(jarPath)
	if err != nil {
		return nil, fmt.Errorf("invalid JAR %q: %w", jarPath, err)
	}
	defer zr.Close()
	if opt.JavaRelease == 0 {
		opt.JavaRelease = DefaultJavaRelease
	}
	if opt.JavaRelease < 8 {
		return nil, fmt.Errorf("java release must be at least 8")
	}
	multi := false
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, "META-INF/MANIFEST.MF") {
			b, e := readZip(f)
			if e != nil {
				return nil, e
			}
			multi = manifestMultiRelease(b)
			break
		}
	}
	chosen := map[string]candidate{}
	for _, f := range zr.File {
		logical, rel, ok, er := classEntry(f.Name, multi)
		if er != nil {
			return nil, er
		}
		if !ok || rel > opt.JavaRelease {
			continue
		}
		old, exists := chosen[logical]
		if exists && rel == old.release {
			return nil, fmt.Errorf("duplicate class definition %q at release %d", logical, rel)
		}
		if !exists || rel > old.release {
			chosen[logical] = candidate{f, logical, rel}
		}
	}
	names := make([]string, 0, len(chosen))
	for n := range chosen {
		names = append(names, n)
	}
	sort.Strings(names)
	type parsedClass struct {
		class Class
		entry string
	}
	var parsed []parsedClass
	flags := map[string]uint16{}
	for _, n := range names {
		if n == "module-info.class" || strings.HasSuffix(n, "/module-info.class") {
			continue
		}
		b, e := readZip(chosen[n].file)
		if e != nil {
			return nil, e
		}
		c, e := ParseClass(b)
		if e != nil {
			return nil, fmt.Errorf("%s: %w", chosen[n].file.Name, e)
		}
		if strings.HasSuffix(n, "package-info.class") {
			continue
		}
		if c.Flags&AccModule != 0 {
			continue
		}
		parsed = append(parsed, parsedClass{c, n})
		flags[c.Name] = c.Flags
	}
	byPkg := map[string][]schema.Symbol{}
	for _, pc := range parsed {
		c, n := pc.class, pc.entry
		if !externallyAccessible(c, flags) {
			continue
		}
		syms := classSymbols(c, n)
		if len(syms) == 0 {
			continue
		}
		pkg := packageName(c.Name)
		byPkg[pkg] = append(byPkg[pkg], syms...)
	}
	pkgs := make([]schema.Package, 0, len(byPkg))
	for pkg, syms := range byPkg {
		sortSymbols(syms)
		p := schema.Package{ID: pkg, Language: "java", Path: strings.ReplaceAll(pkg, ".", "/"), Name: simplePackage(pkg), Level: "package", Symbols: syms}
		if pkg == "" {
			p.ID = "(default)"
			p.Name = "(default)"
			p.Path = ""
		}
		pkgs = append(pkgs, p)
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].ID < pkgs[j].ID })
	return pkgs, nil
}

func externallyAccessible(c Class, flags map[string]uint16) bool {
	if c.Flags&AccPublic == 0 || c.Flags&AccSynthetic != 0 {
		return false
	}
	outer := ""
	if c.Inner != nil {
		outer = c.Inner.Outer
	}
	if outer == "" {
		return true
	}
	for outer != "" {
		if f, ok := flags[outer]; ok && f&AccPublic == 0 {
			return false
		}
		if i := strings.LastIndex(outer, "$"); i >= 0 {
			outer = outer[:i]
		} else {
			outer = ""
		}
	}
	return true
}
func readZip(f *zip.File) ([]byte, error) {
	r, e := f.Open()
	if e != nil {
		return nil, fmt.Errorf("%s: %w", f.Name, e)
	}
	defer r.Close()
	b, e := io.ReadAll(io.LimitReader(r, int64(f.UncompressedSize64)+1))
	if e != nil {
		return nil, fmt.Errorf("%s: %w", f.Name, e)
	}
	if uint64(len(b)) != f.UncompressedSize64 {
		return nil, fmt.Errorf("%s: corrupt ZIP entry", f.Name)
	}
	return b, nil
}
func manifestMultiRelease(b []byte) bool {
	sc := bufio.NewScanner(strings.NewReader(strings.ReplaceAll(string(b), "\r\n", "\n")))
	key := ""
	val := ""
	flush := func() bool {
		return strings.EqualFold(key, "Multi-Release") && strings.EqualFold(strings.TrimSpace(val), "true")
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, " ") {
			val += strings.TrimPrefix(line, " ")
			continue
		}
		if flush() {
			return true
		}
		key, val = "", ""
		if i := strings.IndexByte(line, ':'); i >= 0 {
			key = line[:i]
			val = strings.TrimSpace(line[i+1:])
		}
	}
	return flush()
}
func classEntry(name string, multi bool) (string, int, bool, error) {
	name = path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") {
		return "", 0, false, fmt.Errorf("invalid JAR entry %q", name)
	}
	if !strings.HasSuffix(name, ".class") {
		return "", 0, false, nil
	}
	const p = "META-INF/versions/"
	if !strings.HasPrefix(name, p) {
		return name, 0, true, nil
	}
	if !multi {
		return "", 0, false, nil
	}
	rest := strings.TrimPrefix(name, p)
	i := strings.IndexByte(rest, '/')
	if i < 1 {
		return "", 0, false, fmt.Errorf("invalid multi-release entry %q", name)
	}
	v, e := strconv.Atoi(rest[:i])
	if e != nil || v < 9 {
		return "", 0, false, fmt.Errorf("invalid multi-release entry %q", name)
	}
	return rest[i+1:], v, true, nil
}

func classSymbols(c Class, entry string) []schema.Symbol {
	pkg := packageName(c.Name)
	binarySimple := strings.TrimPrefix(c.Name, pkg)
	binarySimple = strings.TrimPrefix(binarySimple, ".")
	display := strings.ReplaceAll(binarySimple, "$", ".")
	ownerParent := ""
	if i := strings.LastIndex(display, "."); i >= 0 {
		ownerParent = display[:i]
	}
	mods := classModifiers(c.Flags)
	if len(c.Permitted) > 0 {
		mods = append(mods, "sealed")
	}
	var tp []schema.TypeParam
	var ext, impl []string
	if c.Signature != nil {
		tp = schemaTypeParams(c.Signature.TypeParams)
		if c.Signature.Super.Name != "java.lang.Object" {
			ext = []string{c.Signature.Super.String()}
		}
		for _, x := range c.Signature.Interfaces {
			impl = append(impl, x.String())
		}
	} else {
		if c.Super != "" && c.Super != "java.lang.Object" {
			ext = []string{c.Super}
		}
		impl = append(impl, c.Interfaces...)
	}
	kind := "class"
	if c.Record {
		kind = "record"
	} else if c.Flags&AccAnnotation != 0 {
		kind = "annotation"
	} else if c.Flags&AccEnum != 0 {
		kind = "enum"
	} else if c.Flags&AccInterface != 0 {
		kind = "interface"
	}
	if c.Flags&AccInterface != 0 {
		ext = append(ext, impl...)
		impl = nil
	}
	var sig *schema.Signature
	if len(tp) > 0 || len(mods) > 0 {
		sig = &schema.Signature{Params: []schema.Param{}, Returns: []schema.Param{}, TypeParams: tp, Modifiers: mods}
	}
	loc := schema.Location{File: entry}
	classAnnotations := annotationStrings(c.Annotations)
	if c.Deprecated {
		classAnnotations = ensureDeprecated(classAnnotations)
	}
	out := []schema.Symbol{{ID: display, Name: simpleType(display), Kind: "type", Visibility: "public", VisibilityIdiom: "access-modifier", Location: loc, Owner: ownerParent, Complexity: schema.DeferredComplexity(), Signature: sig, TypeKind: kind, BinaryName: c.Name, Extends: ext, Implements: impl, Permits: c.Permitted, Annotations: classAnnotations}}
	fields := append(append([]Field(nil), c.Fields...), c.RecordComponents...)
	for _, f := range fields {
		if !apiMember(f.Flags) || f.Flags&(AccSynthetic) != 0 {
			continue
		}
		typ := f.Type
		if f.Generic != nil {
			typ = *f.Generic
		}
		fk := "field"
		if f.Flags&AccEnum != 0 {
			fk = "enum-constant"
		}
		cv, ck := constantString(f.Constant)
		ann := annotationStrings(f.Annotations)
		if f.Deprecated {
			ann = ensureDeprecated(ann)
		}
		out = append(out, schema.Symbol{ID: display + "." + f.Name, Name: f.Name, Kind: fk, Visibility: visibility(f.Flags), VisibilityIdiom: "access-modifier", Location: loc, Owner: display, Complexity: schema.DeferredComplexity(), Type: typ.String(), Constant: cv, ConstantKind: ck, Descriptor: f.Descriptor, Modifiers: fieldModifiers(f.Flags), Annotations: ann})
	}
	for _, m := range c.Methods {
		if m.Name == "<clinit>" || !apiMember(m.Flags) || m.Flags&(AccSynthetic|AccBridge) != 0 {
			continue
		}
		mt := m.Type
		if m.Generic != nil {
			mt = *m.Generic
		}
		visibleMeta := m.Parameters
		paramTypes := mt.Params
		visibleIndices := make([]int, len(mt.Params))
		for i := range visibleIndices {
			visibleIndices[i] = i
		}
		if len(m.Parameters) == len(m.Type.Params) {
			visibleMeta = nil
			visibleIndices = nil
			if len(mt.Params) == len(m.Type.Params) {
				paramTypes = nil
			}
			for i, p := range m.Parameters {
				if p.Flags&(AccSynthetic|0x8000) == 0 {
					visibleMeta = append(visibleMeta, p)
					visibleIndices = append(visibleIndices, i)
					if len(mt.Params) == len(m.Type.Params) {
						paramTypes = append(paramTypes, mt.Params[i])
					}
				}
			}
		}
		params := make([]schema.Param, len(paramTypes))
		for i, t := range paramTypes {
			name := "arg" + strconv.Itoa(i)
			synthetic := true
			if i < len(visibleMeta) && visibleMeta[i].Name != "" {
				name = visibleMeta[i].Name
				synthetic = false
			}
			var ann []string
			annotationIndex := i
			if i < len(visibleIndices) {
				annotationIndex = visibleIndices[i]
			}
			if annotationIndex < len(m.ParameterAnnotations) {
				ann = annotationStrings(m.ParameterAnnotations[annotationIndex])
			}
			params[i] = schema.Param{Name: name, Type: t.String(), Annotations: ann, NameSynthetic: synthetic}
		}
		returns := []schema.Param{}
		if mt.Return.Kind != "void" {
			returns = []schema.Param{{Type: mt.Return.String()}}
		}
		throws := append([]string(nil), m.Exceptions...)
		if len(mt.Throws) > 0 {
			throws = nil
			for _, t := range mt.Throws {
				throws = append(throws, t.String())
			}
		}
		mm := methodModifiers(m.Flags)
		sg := &schema.Signature{Params: params, Returns: returns, TypeParams: schemaTypeParams(mt.TypeParams), Variadic: m.Flags&AccVarargs != 0, Modifiers: mm, Descriptor: m.Descriptor, Throws: throws}
		name := m.Name
		sk := "method"
		if name == "<init>" {
			name = simpleType(display)
			sk = "constructor"
		}
		ann := annotationStrings(m.Annotations)
		if m.Deprecated {
			ann = ensureDeprecated(ann)
		}
		def := ""
		if m.Default != nil {
			def = annotationValue(m.Default)
		}
		out = append(out, schema.Symbol{ID: display + "." + name, Name: name, Kind: sk, Visibility: visibility(m.Flags), VisibilityIdiom: "access-modifier", Location: loc, Owner: display, Complexity: schema.DeferredComplexity(), Signature: sg, Annotations: ann, DefaultValue: def})
	}
	return out
}
func packageName(n string) string {
	if i := strings.LastIndex(n, "."); i >= 0 {
		return n[:i]
	}
	return ""
}
func simplePackage(n string) string {
	if i := strings.LastIndex(n, "."); i >= 0 {
		return n[i+1:]
	}
	return n
}
func simpleType(n string) string {
	if i := strings.LastIndex(n, "."); i >= 0 {
		return n[i+1:]
	}
	return n
}
func apiMember(f uint16) bool { return f&(AccPublic|AccProtected) != 0 }
func visibility(f uint16) string {
	if f&AccPublic != 0 {
		return "public"
	}
	if f&AccProtected != 0 {
		return "protected"
	}
	if f&AccPrivate != 0 {
		return "private"
	}
	return "package-private"
}
func classModifiers(f uint16) []string {
	var x []string
	if f&AccAbstract != 0 {
		x = append(x, "abstract")
	}
	if f&AccStatic != 0 {
		x = append(x, "static")
	}
	if f&AccFinal != 0 {
		x = append(x, "final")
	}
	if len(x) == 0 {
		return nil
	}
	return x
}
func methodModifiers(f uint16) []string {
	x := classModifiers(f)
	if f&AccSynchronized != 0 {
		x = append(x, "synchronized")
	}
	if f&AccNative != 0 {
		x = append(x, "native")
	}
	if f&AccStrict != 0 {
		x = append(x, "strictfp")
	}
	if f&AccVarargs != 0 {
		x = append(x, "varargs")
	}
	return x
}
func fieldModifiers(f uint16) []string {
	var x []string
	if f&AccStatic != 0 {
		x = append(x, "static")
	}
	if f&AccFinal != 0 {
		x = append(x, "final")
	}
	if f&AccVolatile != 0 {
		x = append(x, "volatile")
	}
	if f&AccTransient != 0 {
		x = append(x, "transient")
	}
	return x
}
func constantString(v any) (string, string) {
	if v == nil {
		return "", ""
	}
	switch x := v.(type) {
	case string:
		return x, "string"
	case int32:
		return strconv.FormatInt(int64(x), 10), "integer"
	case int64:
		return strconv.FormatInt(x, 10), "long"
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 32), "float"
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64), "double"
	default:
		return fmt.Sprint(x), "unknown"
	}
}
func schemaTypeParams(xs []TypeParameter) []schema.TypeParam {
	out := make([]schema.TypeParam, len(xs))
	for i, x := range xs {
		constraint := ""
		for j, b := range x.Bounds {
			if j > 0 {
				constraint += " & "
			}
			constraint += b.String()
		}
		out[i] = schema.TypeParam{Name: x.Name, Constraint: constraint}
	}
	return out
}
func annotationStrings(xs []Annotation) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = annotationString(x)
	}
	sort.Strings(out)
	return out
}
func annotationString(a Annotation) string {
	if len(a.Values) == 0 {
		return a.Type
	}
	keys := make([]string, 0, len(a.Values))
	for k := range a.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := a.Type + "("
	for i, k := range keys {
		if i > 0 {
			s += ", "
		}
		s += k + "=" + annotationValue(a.Values[k])
	}
	return s + ")"
}
func annotationValue(v any) string {
	switch x := v.(type) {
	case string:
		return strconv.Quote(x)
	case Annotation:
		return "@" + annotationString(x)
	case []any:
		s := "{"
		for i, v := range x {
			if i > 0 {
				s += ", "
			}
			s += annotationValue(v)
		}
		return s + "}"
	default:
		return fmt.Sprint(x)
	}
}
func ensureDeprecated(xs []string) []string {
	for _, x := range xs {
		if strings.HasPrefix(x, "java.lang.Deprecated") {
			return xs
		}
	}
	return append(xs, "java.lang.Deprecated")
}
func sortSymbols(x []schema.Symbol) {
	sort.SliceStable(x, func(i, j int) bool {
		if x[i].ID != x[j].ID {
			return x[i].ID < x[j].ID
		}
		di, dj := "", ""
		if x[i].Signature != nil {
			di = x[i].Signature.Descriptor
		}
		if x[j].Signature != nil {
			dj = x[j].Signature.Descriptor
		}
		return di < dj
	})
}
