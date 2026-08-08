// Package jvm extracts Java API declarations directly from JVM classfiles.
// It never loads target classes and has no Java runtime dependency.
package jvm

import "fmt"

// Type is the lossless intermediate representation shared by descriptor and
// generic-signature parsing. Formatting is deliberately kept at the schema
// boundary so parsing never depends on presentation choices.
type Type struct {
	Kind  string // primitive, class, variable, array, wildcard, void
	Name  string
	Args  []Type
	Elem  *Type
	Bound string // extends or super (wildcards)
}

func (t Type) String() string {
	switch t.Kind {
	case "array":
		return t.Elem.String() + "[]"
	case "wildcard":
		if t.Elem == nil {
			return "?"
		}
		return "? " + t.Bound + " " + t.Elem.String()
	case "class":
		s := t.Name
		if len(t.Args) > 0 {
			s += "<"
			for i, a := range t.Args {
				if i > 0 {
					s += ", "
				}
				s += a.String()
			}
			s += ">"
		}
		return s
	default:
		return t.Name
	}
}

type TypeParameter struct {
	Name   string
	Bounds []Type
}
type MethodType struct {
	TypeParams []TypeParameter
	Params     []Type
	Return     Type
	Throws     []Type
}
type ClassSignature struct {
	TypeParams []TypeParameter
	Super      Type
	Interfaces []Type
}

type Class struct {
	Minor, Major     uint16
	Flags            uint16
	Name, Super      string
	Interfaces       []string
	Signature        *ClassSignature
	Fields           []Field
	RecordComponents []Field
	Methods          []Method
	Annotations      []Annotation
	Permitted        []string
	Inner            *InnerInfo
	Record           bool
	Deprecated       bool
}
type InnerInfo struct {
	Inner, Outer, Simple string
	Flags                uint16
}
type Field struct {
	Flags            uint16
	Name, Descriptor string
	Type             Type
	Generic          *Type
	Constant         any
	Annotations      []Annotation
	Deprecated       bool
}
type Method struct {
	Flags                uint16
	Name, Descriptor     string
	Type                 MethodType
	Generic              *MethodType
	Exceptions           []string
	Parameters           []Parameter
	Annotations          []Annotation
	ParameterAnnotations [][]Annotation
	Deprecated           bool
	Default              any
}
type Parameter struct {
	Name  string
	Flags uint16
}
type Annotation struct {
	Type   string
	Values map[string]any
}

func normalizeClassName(s string) string {
	for i := range s {
		_ = i
	}
	b := []byte(s)
	for i := range b {
		if b[i] == '/' {
			b[i] = '.'
		}
	}
	return string(b)
}

func parseError(kind string, off int, format string, args ...any) error {
	return fmt.Errorf("malformed %s at byte %d: %s", kind, off, fmt.Sprintf(format, args...))
}
