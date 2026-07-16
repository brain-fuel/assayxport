// Package hierarchy turns the flat manifest index into a navigable package
// tree, so the explorer lays out and renders one level at a time instead of
// every package at once. The tree is derived purely from the index (package
// paths and their per-package aggregates) -- no on-disk schema change -- and
// exposes one level at a time via Level, the unit GET /api/tree returns.
//
// It has no dependency on syscall/js, so it builds and tests on every GOOS;
// the browser only ever consumes the JSON Level produces.
package hierarchy

import (
	"sort"
	"strings"

	"goforge.dev/assayxport/internal/schema"
)

// Node is one node of the package tree. A node is a group (an intermediate
// path segment with children), a package (an assayed PackageEntry), or both --
// a package can itself contain nested sub-packages, so IsPackage and Children
// are independent. Aggregates are rolled up over the whole subtree so a level
// view can size and summarize a node without descending into it.
type Node struct {
	Path     string           // full slash path from the root ("" is the root)
	Name     string           // last path segment (display label)
	IsPkg    bool             // this path is itself an assayed package
	PkgID    string           // package id, when IsPkg
	Shard    string           // shard path, when IsPkg
	OwnSyms  int              // symbols in this package alone, when IsPkg
	Children map[string]*Node // by child name

	// Subtree aggregates (include this node).
	Symbols       int  // total symbols in the subtree
	Packages      int  // total assayed packages in the subtree
	HasEntrypoint bool // any package in the subtree is an entrypoint
}

// Tree is the root of a built hierarchy plus a path index for O(1) lookup of
// any node when serving a specific level.
type Tree struct {
	Root   *Node
	byPath map[string]*Node
	idx    schema.Index
}

// Build constructs the hierarchy from idx by splitting each package's path into
// segments and inserting it, then rolls subtree aggregates up from the leaves.
func Build(idx schema.Index) *Tree {
	root := &Node{Path: "", Name: "", Children: map[string]*Node{}}
	t := &Tree{Root: root, byPath: map[string]*Node{"": root}, idx: idx}

	for _, pe := range idx.Packages {
		segs := splitPath(pe.Path)
		cur := root
		path := ""
		for _, seg := range segs {
			if path == "" {
				path = seg
			} else {
				path += "/" + seg
			}
			child, ok := cur.Children[seg]
			if !ok {
				child = &Node{Path: path, Name: seg, Children: map[string]*Node{}}
				cur.Children[seg] = child
				t.byPath[path] = child
			}
			cur = child
		}
		// cur is the node for this package's path. Two packages can share a
		// path only if the manifest is malformed; last write wins, harmless.
		cur.IsPkg = true
		cur.PkgID = pe.ID
		cur.Shard = pe.Shard
		cur.OwnSyms = pe.SymbolCount
		if pe.IsEntrypoint {
			cur.HasEntrypoint = true
		}
	}
	rollup(root)
	return t
}

// rollup fills Symbols/Packages/HasEntrypoint for n from its own package (if
// any) plus every child's already-rolled aggregates, depth-first.
func rollup(n *Node) {
	n.Symbols = n.OwnSyms
	if n.IsPkg {
		n.Packages = 1
	}
	for _, c := range n.Children {
		rollup(c)
		n.Symbols += c.Symbols
		n.Packages += c.Packages
		if c.HasEntrypoint {
			n.HasEntrypoint = true
		}
	}
}

// LevelEntry is one child in a Level: a group or package with the aggregates a
// client needs to size, label, and decide whether to descend into it, but
// without any per-symbol detail (that is a shard fetch).
type LevelEntry struct {
	Path          string `json:"path"`
	Name          string `json:"name"`
	Kind          string `json:"kind"` // "group" or "package"
	Symbols       int    `json:"symbols"`
	Packages      int    `json:"packages"`
	Children      int    `json:"children"`
	HasEntrypoint bool   `json:"has_entrypoint"`
	PkgID         string `json:"pkg_id,omitempty"`
	Shard         string `json:"shard,omitempty"`
}

// Level is what GET /api/tree returns for one node: the node's own path and its
// immediate children (one level), sorted deterministically. The payload is
// O(this node's breadth), not O(all packages), which is the whole point.
type Level struct {
	Path     string       `json:"path"`
	Name     string       `json:"name"`
	IsPkg    bool         `json:"is_pkg"`
	Children []LevelEntry `json:"children"`
}

// Level returns the immediate children of the node at path (path "" is the
// root). ok is false if no such node exists. A node that is both a package and
// a group returns its sub-packages as children; its own symbols come from a
// shard fetch, not the level.
func (t *Tree) Level(path string) (Level, bool) {
	n, ok := t.byPath[path]
	if !ok {
		return Level{}, false
	}
	lv := Level{Path: n.Path, Name: n.Name, IsPkg: n.IsPkg}
	for _, c := range n.Children {
		e := LevelEntry{
			Path: c.Path, Name: c.Name, Symbols: c.Symbols,
			Packages: c.Packages, Children: len(c.Children),
			HasEntrypoint: c.HasEntrypoint,
		}
		// A node is a "package" for the client when it is an assayed package
		// with no sub-packages to descend into; a package that also groups
		// sub-packages is shown as a group so it stays enterable (its own
		// symbols are reached by opening it).
		if c.IsPkg && len(c.Children) == 0 {
			e.Kind = "package"
			e.PkgID = c.PkgID
			e.Shard = c.Shard
		} else {
			e.Kind = "group"
			if c.IsPkg {
				// A group that is also a package: carry its shard so the client
				// can still open its own symbols.
				e.PkgID = c.PkgID
				e.Shard = c.Shard
			}
		}
		lv.Children = append(lv.Children, e)
	}
	sort.Slice(lv.Children, func(i, j int) bool {
		return lv.Children[i].Path < lv.Children[j].Path
	})
	return lv, true
}

// splitPath splits a package path into clean slash segments, tolerating "."
// (the scan root), leading "./", backslashes, and empty segments.
func splitPath(p string) []string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	if p == "" || p == "." {
		return nil
	}
	raw := strings.Split(p, "/")
	segs := raw[:0]
	for _, s := range raw {
		if s != "" && s != "." {
			segs = append(segs, s)
		}
	}
	return segs
}
