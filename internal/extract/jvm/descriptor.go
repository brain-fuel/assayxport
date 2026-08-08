package jvm

import "fmt"

type typeParser struct {
	s         string
	i         int
	signature bool
}

func ParseFieldDescriptor(s string) (Type, error) {
	p := typeParser{s: s}
	t, err := p.descriptorType(false)
	if err == nil && p.i != len(s) {
		err = parseError("descriptor", p.i, "trailing data")
	}
	return t, err
}

func ParseMethodDescriptor(s string) (MethodType, error) {
	p := typeParser{s: s}
	var m MethodType
	if !p.take('(') {
		return m, parseError("descriptor", 0, "expected (")
	}
	for p.i < len(s) && s[p.i] != ')' {
		t, e := p.descriptorType(false)
		if e != nil {
			return m, e
		}
		m.Params = append(m.Params, t)
	}
	if !p.take(')') {
		return m, parseError("descriptor", p.i, "expected )")
	}
	t, e := p.descriptorType(true)
	if e != nil {
		return m, e
	}
	m.Return = t
	if p.i != len(s) {
		return m, parseError("descriptor", p.i, "trailing data")
	}
	return m, nil
}

func (p *typeParser) take(c byte) bool {
	if p.i < len(p.s) && p.s[p.i] == c {
		p.i++
		return true
	}
	return false
}
func (p *typeParser) descriptorType(allowVoid bool) (Type, error) {
	if p.i >= len(p.s) {
		return Type{}, parseError("descriptor", p.i, "unexpected end")
	}
	c := p.s[p.i]
	p.i++
	names := map[byte]string{'B': "byte", 'C': "char", 'D': "double", 'F': "float", 'I': "int", 'J': "long", 'S': "short", 'Z': "boolean"}
	if n, ok := names[c]; ok {
		return Type{Kind: "primitive", Name: n}, nil
	}
	if c == 'V' {
		if allowVoid {
			return Type{Kind: "void", Name: "void"}, nil
		}
		return Type{}, parseError("descriptor", p.i-1, "void is not a field type")
	}
	if c == '[' {
		e, err := p.descriptorType(false)
		return Type{Kind: "array", Elem: &e}, err
	}
	if c == 'L' {
		start := p.i
		for p.i < len(p.s) && p.s[p.i] != ';' {
			p.i++
		}
		if !p.take(';') {
			return Type{}, parseError("descriptor", start, "unterminated class")
		}
		return Type{Kind: "class", Name: normalizeClassName(p.s[start : p.i-1])}, nil
	}
	return Type{}, parseError("descriptor", p.i-1, "unknown tag %q", c)
}

func ParseClassSignature(s string) (ClassSignature, error) {
	p := typeParser{s: s, signature: true}
	var out ClassSignature
	var err error
	out.TypeParams, err = p.formals()
	if err != nil {
		return out, err
	}
	out.Super, err = p.sigType()
	if err != nil {
		return out, err
	}
	for p.i < len(s) {
		t, e := p.sigType()
		if e != nil {
			return out, e
		}
		out.Interfaces = append(out.Interfaces, t)
	}
	return out, nil
}
func ParseMethodSignature(s string) (MethodType, error) {
	p := typeParser{s: s, signature: true}
	var m MethodType
	var err error
	m.TypeParams, err = p.formals()
	if err != nil {
		return m, err
	}
	if !p.take('(') {
		return m, parseError("Signature", p.i, "expected (")
	}
	for p.i < len(s) && p.s[p.i] != ')' {
		t, e := p.sigType()
		if e != nil {
			return m, e
		}
		m.Params = append(m.Params, t)
	}
	if !p.take(')') {
		return m, parseError("Signature", p.i, "expected )")
	}
	m.Return, err = p.sigType()
	if err != nil {
		return m, err
	}
	for p.take('^') {
		t, e := p.sigType()
		if e != nil {
			return m, e
		}
		m.Throws = append(m.Throws, t)
	}
	if p.i != len(s) {
		return m, parseError("Signature", p.i, "trailing data")
	}
	return m, nil
}
func ParseFieldSignature(s string) (Type, error) {
	p := typeParser{s: s, signature: true}
	t, e := p.sigType()
	if e == nil && p.i != len(s) {
		e = parseError("Signature", p.i, "trailing data")
	}
	return t, e
}

func (p *typeParser) formals() ([]TypeParameter, error) {
	if !p.take('<') {
		return nil, nil
	}
	var out []TypeParameter
	for p.i < len(p.s) && p.s[p.i] != '>' {
		start := p.i
		for p.i < len(p.s) && p.s[p.i] != ':' {
			p.i++
		}
		if !p.take(':') || start == p.i-1 {
			return nil, parseError("Signature", p.i, "bad type parameter")
		}
		tp := TypeParameter{Name: p.s[start : p.i-1]}
		if p.i < len(p.s) && p.s[p.i] != ':' {
			t, e := p.sigType()
			if e != nil {
				return nil, e
			}
			tp.Bounds = append(tp.Bounds, t)
		}
		for p.take(':') {
			t, e := p.sigType()
			if e != nil {
				return nil, e
			}
			tp.Bounds = append(tp.Bounds, t)
		}
		out = append(out, tp)
	}
	if !p.take('>') {
		return nil, parseError("Signature", p.i, "unterminated type parameters")
	}
	return out, nil
}
func (p *typeParser) sigType() (Type, error) {
	if p.i >= len(p.s) {
		return Type{}, parseError("Signature", p.i, "unexpected end")
	}
	c := p.s[p.i]
	if c == 'T' {
		p.i++
		start := p.i
		for p.i < len(p.s) && p.s[p.i] != ';' {
			p.i++
		}
		if !p.take(';') {
			return Type{}, parseError("Signature", start, "unterminated type variable")
		}
		return Type{Kind: "variable", Name: p.s[start : p.i-1]}, nil
	}
	if c == '[' {
		p.i++
		e, err := p.sigType()
		return Type{Kind: "array", Elem: &e}, err
	}
	if c == 'L' {
		return p.classSig()
	}
	return p.descriptorType(true)
}
func (p *typeParser) classSig() (Type, error) {
	p.i++
	start := p.i
	name := ""
	var args []Type
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ';':
			if start < p.i {
				name += p.s[start:p.i]
			}
			p.i++
			return Type{Kind: "class", Name: normalizeClassName(name), Args: args}, nil
		case '<':
			name += p.s[start:p.i]
			p.i++
			for p.i < len(p.s) && p.s[p.i] != '>' {
				a, e := p.typeArg()
				if e != nil {
					return Type{}, e
				}
				args = append(args, a)
			}
			if !p.take('>') {
				return Type{}, parseError("Signature", p.i, "unterminated type arguments")
			}
			start = p.i
		case '.':
			if start < p.i {
				name += p.s[start:p.i]
			}
			if len(args) > 0 {
				name += "<"
				for i, a := range args {
					if i > 0 {
						name += ", "
					}
					name += a.String()
				}
				name += ">"
				args = nil
			}
			name += "."
			p.i++
			start = p.i
		default:
			p.i++
		}
	}
	return Type{}, parseError("Signature", p.i, "unterminated class type")
}
func (p *typeParser) typeArg() (Type, error) {
	if p.take('*') {
		return Type{Kind: "wildcard"}, nil
	}
	if p.take('+') {
		t, e := p.sigType()
		return Type{Kind: "wildcard", Bound: "extends", Elem: &t}, e
	}
	if p.take('-') {
		t, e := p.sigType()
		return Type{Kind: "wildcard", Bound: "super", Elem: &t}, e
	}
	return p.sigType()
}

func formatTypeParams(xs []TypeParameter) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		v := x.Name
		if len(x.Bounds) > 0 {
			v += " extends "
			for j, b := range x.Bounds {
				if j > 0 {
					v += " & "
				}
				v += b.String()
			}
		}
		out[i] = v
	}
	return out
}

var _ = fmt.Sprintf
