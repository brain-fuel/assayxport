// User-owned LawSpec adapter (S1.j; workspace code).
//
// The native handler of HaskellParser, the only native step of the Haskell
// extractor's logic (spec.md, Architecture). It parses with gotreesitter
// v0.56.0's Haskell grammar (owner's ruling: build on v0.56.0, the bump in
// the PR) and hands the tree to LawSpec as `Syntax`: named nodes only, each
// with its field name in its parent, 1-based positions, its source text,
// and whether it is an error or missing node. The root is the whole file
// (laws HSP-1 to HSP-3). In ax this handler is written in upstream's `.gp`
// (DR-0045).
//
// The extraction itself is LawSpec (checked definitions, generated Go).
package extract

import (
	"strings"
	"math/big"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

type HaskellParserHandler struct{ lang *gts.Language }

func NewHaskellParserHandler() HaskellParser {
	return &HaskellParserHandler{lang: grammars.HaskellLanguage()}
}

func (h *HaskellParserHandler) Parse(source string) Syntax {
	// CRLF as LF, as ax's haskell.gp does (law HSE-7).
	source = strings.ReplaceAll(source, "\r\n", "\n")
	src := []byte(source)
	tree, err := gts.NewParser(h.lang).Parse(src)
	if err != nil {
		panic(err)
	}
	root := h.node(tree.RootNode(), "", source)
	// A hopeless parse can be rooted in an ERROR node. The law (HSP-1) says
	// every parse is rooted at `haskell`, with errors below: wrap it.
	if root.Kind != "haskell" {
		root = Syntax{Kind: "haskell", From: root.From, To: root.To, Children: []Syntax{root}}
	}
	// The root is the whole file; tree-sitter's root span already reaches
	// its end (as in ax's haskell.gp, rootSyntax).
	root.Text = source
	return root
}

func point(p gts.Point) Point {
	return Point{Line: big.NewInt(int64(p.Row) + 1), Col: big.NewInt(int64(p.Column) + 1)}
}

// node mirrors ax's haskell.gp syntaxOf: named children in one linear pass
// over Children() (index loops are O(n^2) in this backend, ts.gp), each
// node's text a slice of the one source string.
func (h *HaskellParserHandler) node(n *gts.Node, field string, src string) Syntax {
	kids := n.Children()
	children := make([]Syntax, 0, len(kids))
	for i, c := range kids {
		if c == nil || !c.IsNamed() {
			continue
		}
		children = append(children, h.node(c, n.FieldNameForChild(i, h.lang), src))
	}
	text := ""
	if a, b := int(n.StartByte()), int(n.EndByte()); a >= 0 && b <= len(src) && a <= b {
		text = src[a:b]
	}
	return Syntax{
		Kind:     n.Type(h.lang),
		Field:    field,
		Text:     text,
		From:     point(n.StartPoint()),
		To:       point(n.EndPoint()),
		IsError:  n.IsError() || n.IsMissing(),
		Children: children,
	}
}
