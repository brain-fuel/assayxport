// Package jvm is the compiled-Java acquisition layer for AssayXport.
//
// The adapter intentionally produces the same schema.Package and schema.Symbol
// values as Java source extraction. Class, interface, enum, annotation, and
// record declarations map to kind=type plus type_kind; JVM constructors map
// from <init> to kind=constructor; fields and methods retain Java's four-way
// visibility idiom. Descriptor and Signature data become structured Params,
// Returns, and TypeParams, with the erased descriptor retained as optional
// binary metadata. Exceptions, inheritance, permitted subclasses, constants,
// modifiers, and annotations use additive fields on those same records.
//
// A classfile does not contain Javadoc or a source tree. Doc therefore stays
// empty and Location.File names the stable JAR entry (never an absolute cache
// path); line/column stay zero. Calls stay absent and complexity is deferred:
// declaration extraction deliberately does not interpret Code attributes.
// module-info and package-info are metadata carriers, not ordinary type symbols.
// This boundary leaves bytecode call analysis and optional source/Javadoc merges
// as later acquisition stages without changing the manifest product.
package jvm
