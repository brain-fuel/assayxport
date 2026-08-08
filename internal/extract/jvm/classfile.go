package jvm

import (
	"encoding/binary"
	"fmt"
	"math"
)

const (
	AccPublic       = 0x0001
	AccPrivate      = 0x0002
	AccProtected    = 0x0004
	AccStatic       = 0x0008
	AccFinal        = 0x0010
	AccSuper        = 0x0020
	AccSynchronized = 0x0020
	AccVolatile     = 0x0040
	AccBridge       = 0x0040
	AccTransient    = 0x0080
	AccVarargs      = 0x0080
	AccNative       = 0x0100
	AccInterface    = 0x0200
	AccAbstract     = 0x0400
	AccStrict       = 0x0800
	AccSynthetic    = 0x1000
	AccAnnotation   = 0x2000
	AccEnum         = 0x4000
	AccModule       = 0x8000
)

type cpEntry struct {
	tag  byte
	a, b uint16
	s    string
	v    any
}
type reader struct {
	b  []byte
	n  int
	cp []cpEntry
}

func (r *reader) need(n int) error {
	if n < 0 || r.n+n > len(r.b) {
		return fmt.Errorf("truncated classfile at byte %d", r.n)
	}
	return nil
}
func (r *reader) u1() (byte, error) {
	if e := r.need(1); e != nil {
		return 0, e
	}
	v := r.b[r.n]
	r.n++
	return v, nil
}
func (r *reader) u2() (uint16, error) {
	if e := r.need(2); e != nil {
		return 0, e
	}
	v := binary.BigEndian.Uint16(r.b[r.n:])
	r.n += 2
	return v, nil
}
func (r *reader) u4() (uint32, error) {
	if e := r.need(4); e != nil {
		return 0, e
	}
	v := binary.BigEndian.Uint32(r.b[r.n:])
	r.n += 4
	return v, nil
}
func (r *reader) bytes(n int) ([]byte, error) {
	if e := r.need(n); e != nil {
		return nil, e
	}
	v := r.b[r.n : r.n+n]
	r.n += n
	return v, nil
}
func (r *reader) utf(i uint16) (string, error) {
	if i == 0 || int(i) >= len(r.cp) || r.cp[i].tag != 1 {
		return "", fmt.Errorf("invalid Utf8 constant #%d", i)
	}
	return r.cp[i].s, nil
}
func (r *reader) class(i uint16) (string, error) {
	if i == 0 {
		return "", nil
	}
	if int(i) >= len(r.cp) || r.cp[i].tag != 7 {
		return "", fmt.Errorf("invalid Class constant #%d", i)
	}
	s, e := r.utf(r.cp[i].a)
	return normalizeClassName(s), e
}

func ParseClass(data []byte) (Class, error) {
	r := reader{b: data}
	var c Class
	magic, e := r.u4()
	if e != nil {
		return c, e
	}
	if magic != 0xcafebabe {
		return c, fmt.Errorf("invalid classfile magic 0x%08x", magic)
	}
	c.Minor, e = r.u2()
	if e != nil {
		return c, e
	}
	c.Major, e = r.u2()
	if e != nil {
		return c, e
	}
	if c.Major < 45 || c.Major > 70 {
		return c, fmt.Errorf("unsupported classfile major version %d", c.Major)
	}
	count, e := r.u2()
	if e != nil {
		return c, e
	}
	r.cp = make([]cpEntry, count)
	for i := 1; i < int(count); i++ {
		tag, x := r.u1()
		if x != nil {
			return c, fmt.Errorf("constant pool #%d: %w", i, x)
		}
		ce := cpEntry{tag: tag}
		switch tag {
		case 1:
			n, x := r.u2()
			if x != nil {
				return c, x
			}
			b, x := r.bytes(int(n))
			if x != nil {
				return c, x
			}
			ce.s = string(b)
		case 3:
			u, x := r.u4()
			if x != nil {
				return c, x
			}
			ce.v = int32(u)
		case 4:
			u, x := r.u4()
			if x != nil {
				return c, x
			}
			ce.v = math.Float32frombits(u)
		case 5:
			if i+1 >= int(count) {
				return c, fmt.Errorf("constant pool #%d: wide constant has no reserved slot", i)
			}
			u1, x := r.u4()
			if x != nil {
				return c, x
			}
			u2, x := r.u4()
			if x != nil {
				return c, x
			}
			ce.v = int64(uint64(u1)<<32 | uint64(u2))
			r.cp[i] = ce
			i++
			continue
		case 6:
			if i+1 >= int(count) {
				return c, fmt.Errorf("constant pool #%d: wide constant has no reserved slot", i)
			}
			u1, x := r.u4()
			if x != nil {
				return c, x
			}
			u2, x := r.u4()
			if x != nil {
				return c, x
			}
			ce.v = math.Float64frombits(uint64(u1)<<32 | uint64(u2))
			r.cp[i] = ce
			i++
			continue
		case 7, 8, 16, 19, 20:
			ce.a, x = r.u2()
			if x != nil {
				return c, x
			}
		case 9, 10, 11, 12, 17, 18:
			ce.a, x = r.u2()
			if x != nil {
				return c, x
			}
			ce.b, x = r.u2()
			if x != nil {
				return c, x
			}
		case 15:
			_, x = r.u1()
			if x != nil {
				return c, x
			}
			ce.a, x = r.u2()
			if x != nil {
				return c, x
			}
		default:
			return c, fmt.Errorf("constant pool #%d: unsupported tag %d", i, tag)
		}
		r.cp[i] = ce
	}
	c.Flags, e = r.u2()
	if e != nil {
		return c, e
	}
	this, e := r.u2()
	if e != nil {
		return c, e
	}
	super, e := r.u2()
	if e != nil {
		return c, e
	}
	c.Name, e = r.class(this)
	if e != nil {
		return c, e
	}
	c.Super, e = r.class(super)
	if e != nil {
		return c, e
	}
	ni, e := r.u2()
	if e != nil {
		return c, e
	}
	for range int(ni) {
		x, z := r.u2()
		if z != nil {
			return c, z
		}
		s, z := r.class(x)
		if z != nil {
			return c, z
		}
		c.Interfaces = append(c.Interfaces, s)
	}
	nf, e := r.u2()
	if e != nil {
		return c, e
	}
	for range int(nf) {
		f, z := r.field()
		if z != nil {
			return c, z
		}
		c.Fields = append(c.Fields, f)
	}
	nm, e := r.u2()
	if e != nil {
		return c, e
	}
	for range int(nm) {
		m, z := r.method()
		if z != nil {
			return c, z
		}
		c.Methods = append(c.Methods, m)
	}
	attrs, e := r.attributes()
	if e != nil {
		return c, e
	}
	for _, a := range attrs {
		switch a.name {
		case "Signature":
			s, z := a.signature(&r)
			if z != nil {
				return c, z
			}
			cs, z := ParseClassSignature(s)
			if z != nil {
				return c, z
			}
			c.Signature = &cs
		case "RuntimeVisibleAnnotations", "RuntimeInvisibleAnnotations":
			xs, z := a.annotations(&r)
			if z != nil {
				return c, z
			}
			c.Annotations = append(c.Annotations, xs...)
		case "PermittedSubclasses":
			q := reader{b: a.data, cp: r.cp}
			n, z := q.u2()
			if z != nil {
				return c, z
			}
			for range int(n) {
				i, _ := q.u2()
				s, z := q.class(i)
				if z != nil {
					return c, z
				}
				c.Permitted = append(c.Permitted, s)
			}
		case "Record":
			c.Record = true
			x, z := parseRecord(a.data, &r)
			if z != nil {
				return c, z
			}
			c.RecordComponents = x
		case "InnerClasses":
			if z := parseInner(a.data, &r, &c); z != nil {
				return c, z
			}
		case "Synthetic":
			c.Flags |= AccSynthetic
		case "Deprecated":
			c.Deprecated = true
		}
	}
	if r.n != len(data) {
		return c, fmt.Errorf("trailing classfile data at byte %d", r.n)
	}
	return c, nil
}

func parseRecord(data []byte, pool *reader) ([]Field, error) {
	q := reader{b: data, cp: pool.cp}
	n, err := q.u2()
	if err != nil {
		return nil, err
	}
	out := make([]Field, 0, n)
	for range int(n) {
		ni, err := q.u2()
		if err != nil {
			return nil, err
		}
		di, err := q.u2()
		if err != nil {
			return nil, err
		}
		name, err := q.utf(ni)
		if err != nil {
			return nil, err
		}
		desc, err := q.utf(di)
		if err != nil {
			return nil, err
		}
		typ, err := ParseFieldDescriptor(desc)
		if err != nil {
			return nil, err
		}
		f := Field{Flags: AccPublic | AccFinal, Name: name, Descriptor: desc, Type: typ}
		attrs, err := q.attributes()
		if err != nil {
			return nil, err
		}
		for _, a := range attrs {
			switch a.name {
			case "Signature":
				s, e := a.signature(&q)
				if e != nil {
					return nil, e
				}
				t, e := ParseFieldSignature(s)
				if e != nil {
					return nil, e
				}
				f.Generic = &t
			case "RuntimeVisibleAnnotations", "RuntimeInvisibleAnnotations":
				x, e := a.annotations(&q)
				if e != nil {
					return nil, e
				}
				f.Annotations = append(f.Annotations, x...)
			}
		}
		out = append(out, f)
	}
	if q.n != len(data) {
		return nil, fmt.Errorf("malformed Record attribute")
	}
	return out, nil
}

type attribute struct {
	name string
	data []byte
}

func (r *reader) attributes() ([]attribute, error) {
	n, e := r.u2()
	if e != nil {
		return nil, e
	}
	out := make([]attribute, 0, n)
	for range int(n) {
		i, z := r.u2()
		if z != nil {
			return nil, z
		}
		name, z := r.utf(i)
		if z != nil {
			return nil, z
		}
		ln, z := r.u4()
		if z != nil {
			return nil, z
		}
		if uint64(ln) > uint64(len(r.b)-r.n) {
			return nil, fmt.Errorf("bad %s attribute length %d", name, ln)
		}
		b, z := r.bytes(int(ln))
		if z != nil {
			return nil, z
		}
		out = append(out, attribute{name, b})
	}
	return out, nil
}
func (a attribute) signature(r *reader) (string, error) {
	q := reader{b: a.data, cp: r.cp}
	i, e := q.u2()
	if e != nil || q.n != len(a.data) {
		return "", fmt.Errorf("malformed Signature attribute")
	}
	return q.utf(i)
}
func (r *reader) field() (Field, error) {
	var f Field
	var e error
	f.Flags, e = r.u2()
	if e != nil {
		return f, e
	}
	n, _ := r.u2()
	d, _ := r.u2()
	f.Name, e = r.utf(n)
	if e != nil {
		return f, e
	}
	f.Descriptor, e = r.utf(d)
	if e != nil {
		return f, e
	}
	f.Type, e = ParseFieldDescriptor(f.Descriptor)
	if e != nil {
		return f, e
	}
	as, e := r.attributes()
	if e != nil {
		return f, e
	}
	for _, a := range as {
		switch a.name {
		case "Signature":
			s, z := a.signature(r)
			if z != nil {
				return f, z
			}
			t, z := ParseFieldSignature(s)
			if z != nil {
				return f, z
			}
			f.Generic = &t
		case "ConstantValue":
			q := reader{b: a.data, cp: r.cp}
			i, z := q.u2()
			if z != nil {
				return f, z
			}
			f.Constant, z = constant(r, i)
			if z != nil {
				return f, z
			}
		case "RuntimeVisibleAnnotations", "RuntimeInvisibleAnnotations":
			x, z := a.annotations(r)
			if z != nil {
				return f, z
			}
			f.Annotations = append(f.Annotations, x...)
		case "Deprecated":
			f.Deprecated = true
		case "Synthetic":
			f.Flags |= AccSynthetic
		}
	}
	return f, nil
}
func (r *reader) method() (Method, error) {
	var m Method
	var e error
	m.Flags, e = r.u2()
	if e != nil {
		return m, e
	}
	n, _ := r.u2()
	d, _ := r.u2()
	m.Name, e = r.utf(n)
	if e != nil {
		return m, e
	}
	m.Descriptor, e = r.utf(d)
	if e != nil {
		return m, e
	}
	m.Type, e = ParseMethodDescriptor(m.Descriptor)
	if e != nil {
		return m, e
	}
	as, e := r.attributes()
	if e != nil {
		return m, e
	}
	for _, a := range as {
		switch a.name {
		case "Signature":
			s, z := a.signature(r)
			if z != nil {
				return m, z
			}
			t, z := ParseMethodSignature(s)
			if z != nil {
				return m, z
			}
			m.Generic = &t
		case "Exceptions":
			q := reader{b: a.data, cp: r.cp}
			n, z := q.u2()
			if z != nil {
				return m, z
			}
			for range int(n) {
				i, _ := q.u2()
				s, z := q.class(i)
				if z != nil {
					return m, z
				}
				m.Exceptions = append(m.Exceptions, s)
			}
		case "MethodParameters":
			q := reader{b: a.data, cp: r.cp}
			n, z := q.u1()
			if z != nil {
				return m, z
			}
			for range int(n) {
				i, _ := q.u2()
				fl, _ := q.u2()
				s := ""
				if i != 0 {
					s, z = q.utf(i)
					if z != nil {
						return m, z
					}
				}
				m.Parameters = append(m.Parameters, Parameter{s, fl})
			}
		case "RuntimeVisibleAnnotations", "RuntimeInvisibleAnnotations":
			x, z := a.annotations(r)
			if z != nil {
				return m, z
			}
			m.Annotations = append(m.Annotations, x...)
		case "RuntimeVisibleParameterAnnotations", "RuntimeInvisibleParameterAnnotations":
			x, z := a.parameterAnnotations(r)
			if z != nil {
				return m, z
			}
			mergeParameterAnnotations(&m, x)
		case "Deprecated":
			m.Deprecated = true
		case "Synthetic":
			m.Flags |= AccSynthetic
		case "AnnotationDefault":
			q := reader{b: a.data, cp: r.cp}
			v, z := q.element()
			if z != nil {
				return m, z
			}
			if q.n != len(q.b) {
				return m, fmt.Errorf("malformed AnnotationDefault attribute")
			}
			m.Default = v
		}
	}
	return m, nil
}
func constant(r *reader, i uint16) (any, error) {
	if i == 0 || int(i) >= len(r.cp) {
		return nil, fmt.Errorf("invalid constant #%d", i)
	}
	e := r.cp[i]
	if e.tag == 8 {
		return r.utf(e.a)
	}
	if e.tag >= 3 && e.tag <= 6 {
		return e.v, nil
	}
	return nil, fmt.Errorf("invalid ConstantValue tag %d", e.tag)
}
func parseInner(data []byte, r *reader, c *Class) error {
	q := reader{b: data, cp: r.cp}
	n, e := q.u2()
	if e != nil {
		return e
	}
	for range int(n) {
		ii, _ := q.u2()
		oi, _ := q.u2()
		ni, _ := q.u2()
		fl, _ := q.u2()
		in, e := q.class(ii)
		if e != nil {
			return e
		}
		if in != c.Name {
			continue
		}
		out, _ := q.class(oi)
		simple := ""
		if ni != 0 {
			simple, e = q.utf(ni)
			if e != nil {
				return e
			}
		}
		c.Inner = &InnerInfo{in, out, simple, fl}
	}
	return nil
}

func (a attribute) annotations(r *reader) ([]Annotation, error) {
	q := reader{b: a.data, cp: r.cp}
	n, e := q.u2()
	if e != nil {
		return nil, e
	}
	out := make([]Annotation, 0, n)
	for range int(n) {
		x, z := q.annotation()
		if z != nil {
			return nil, z
		}
		out = append(out, x)
	}
	if q.n != len(q.b) {
		return nil, fmt.Errorf("malformed annotation attribute")
	}
	return out, nil
}
func (a attribute) parameterAnnotations(r *reader) ([][]Annotation, error) {
	q := reader{b: a.data, cp: r.cp}
	n, e := q.u1()
	if e != nil {
		return nil, e
	}
	out := make([][]Annotation, n)
	for i := range int(n) {
		k, z := q.u2()
		if z != nil {
			return nil, z
		}
		for range int(k) {
			x, z := q.annotation()
			if z != nil {
				return nil, z
			}
			out[i] = append(out[i], x)
		}
	}
	return out, nil
}
func (r *reader) annotation() (Annotation, error) {
	i, e := r.u2()
	if e != nil {
		return Annotation{}, e
	}
	d, e := r.utf(i)
	if e != nil {
		return Annotation{}, e
	}
	t, e := ParseFieldDescriptor(d)
	if e != nil {
		return Annotation{}, e
	}
	a := Annotation{Type: t.String(), Values: map[string]any{}}
	n, e := r.u2()
	if e != nil {
		return a, e
	}
	for range int(n) {
		k, _ := r.u2()
		name, e := r.utf(k)
		if e != nil {
			return a, e
		}
		v, e := r.element()
		if e != nil {
			return a, e
		}
		a.Values[name] = v
	}
	return a, nil
}
func (r *reader) element() (any, error) {
	tag, e := r.u1()
	if e != nil {
		return nil, e
	}
	switch tag {
	case 'B', 'C', 'D', 'F', 'I', 'J', 'S', 'Z', 's':
		i, _ := r.u2()
		if tag == 's' {
			return r.utf(i)
		}
		return constant(r, i)
	case 'e':
		t, _ := r.u2()
		n, _ := r.u2()
		ts, e := r.utf(t)
		if e != nil {
			return nil, e
		}
		ns, e := r.utf(n)
		if e != nil {
			return nil, e
		}
		typ, e := ParseFieldDescriptor(ts)
		if e != nil {
			return nil, e
		}
		return typ.String() + "." + ns, nil
	case 'c':
		i, _ := r.u2()
		s, e := r.utf(i)
		if e != nil {
			return nil, e
		}
		t, e := ParseFieldDescriptor(s)
		if e != nil {
			return nil, e
		}
		return t.String() + ".class", nil
	case '@':
		return r.annotation()
	case '[':
		n, _ := r.u2()
		x := make([]any, 0, n)
		for range int(n) {
			v, z := r.element()
			if z != nil {
				return nil, z
			}
			x = append(x, v)
		}
		return x, nil
	default:
		return nil, fmt.Errorf("invalid annotation element tag %q", tag)
	}
}
func mergeParameterAnnotations(m *Method, x [][]Annotation) {
	if len(m.ParameterAnnotations) < len(x) {
		m.ParameterAnnotations = append(m.ParameterAnnotations, make([][]Annotation, len(x)-len(m.ParameterAnnotations))...)
	}
	for i := range x {
		m.ParameterAnnotations[i] = append(m.ParameterAnnotations[i], x[i]...)
	}
}
