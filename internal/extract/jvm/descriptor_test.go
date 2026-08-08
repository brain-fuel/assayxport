package jvm

import "testing"

func TestFieldDescriptors(t *testing.T) {
	tests := map[string]string{"I": "int", "Ljava/lang/String;": "java.lang.String", "[[I": "int[][]", "[Ljava/util/List;": "java.util.List[]"}
	for in, want := range tests {
		got, e := ParseFieldDescriptor(in)
		if e != nil || got.String() != want {
			t.Errorf("%s = %q, %v; want %q", in, got.String(), e, want)
		}
	}
}
func TestMethodDescriptor(t *testing.T) {
	m, e := ParseMethodDescriptor("(Ljava/lang/String;I)Ljava/util/List;")
	if e != nil {
		t.Fatal(e)
	}
	if len(m.Params) != 2 || m.Params[0].String() != "java.lang.String" || m.Params[1].String() != "int" || m.Return.String() != "java.util.List" {
		t.Fatalf("unexpected method: %+v", m)
	}
}
func TestMalformedDescriptors(t *testing.T) {
	for _, s := range []string{"", "Lbad", "Q", "(I", "(I)Vx", "V"} {
		if _, e := ParseFieldDescriptor(s); e == nil {
			t.Errorf("ParseFieldDescriptor(%q) succeeded", s)
		}
	}
}

func TestGenericSignatures(t *testing.T) {
	tests := map[string]string{
		"Ljava/util/List<Ljava/lang/String;>;":                         "java.util.List<java.lang.String>",
		"Ljava/util/Map<Ljava/lang/String;Ljava/util/List<Lx/Foo;>;>;": "java.util.Map<java.lang.String, java.util.List<x.Foo>>",
		"Ljava/util/List<+Ljava/lang/Number;>;":                        "java.util.List<? extends java.lang.Number>",
		"Ljava/util/List<-Ljava/lang/Integer;>;":                       "java.util.List<? super java.lang.Integer>",
		"LOuter<TT;>.Inner<TU;>;":                                      "Outer<T>.Inner<U>",
		"[TT;":                                                         "T[]",
	}
	for in, want := range tests {
		got, e := ParseFieldSignature(in)
		if e != nil || got.String() != want {
			t.Errorf("%s = %q, %v; want %q", in, got.String(), e, want)
		}
	}
}
func TestGenericMethodSignature(t *testing.T) {
	m, e := ParseMethodSignature("<T::Ljava/lang/Comparable<-TT;>;>(Ljava/util/Collection<+TT;>;)TT;^Ljava/io/IOException;^TT;")
	if e != nil {
		t.Fatal(e)
	}
	if got := formatTypeParams(m.TypeParams)[0]; got != "T extends java.lang.Comparable<? super T>" {
		t.Fatalf("type param = %q", got)
	}
	if m.Params[0].String() != "java.util.Collection<? extends T>" || m.Return.String() != "T" || len(m.Throws) != 2 {
		t.Fatalf("method = %+v", m)
	}
}

func FuzzDescriptorNoPanic(f *testing.F) {
	for _, s := range []string{"I", "[[Ljava/lang/String;", "(I)V", "", "L;"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { ParseFieldDescriptor(s); ParseMethodDescriptor(s) })
}
func FuzzSignatureNoPanic(f *testing.F) {
	for _, s := range []string{"TT;", "Ljava/util/List<*>;", "<T:Ljava/lang/Object;>(TT;)TT;", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { ParseFieldSignature(s); ParseMethodSignature(s); ParseClassSignature(s) })
}
