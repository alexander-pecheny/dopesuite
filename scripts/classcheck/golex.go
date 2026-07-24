package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// The Go side gets a real AST — go/parser is stdlib, so unlike the TypeScript
// half there is nothing to hand-roll. Expanders put classes on elements through
// a small set of shapes, and each is a node type rather than a pattern:
//
//	ClassAttr("btn", "btn-ghost")     a call whose name ends in "Classes"/"ClassAttr"
//	Classes: []string{"card-title"}   a composite-literal field
//	classes = append(classes, "btn")  an append onto an identifier named classes
//
// viewer-header-main lived in the third shape and survived a TypeScript-only
// check for exactly as long as one existed.

func scanGo(src, path string, emitted sites) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return // not our job to report syntax errors; the build does that
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			name := calleeName(v.Fun)
			switch {
			case name == "ClassAttr" || strings.HasSuffix(name, "Classes"):
				addLiteralArgs(v.Args, path, emitted)
			case name == "append" && len(v.Args) > 1 && isClassSlice(v.Args[0]):
				addLiteralArgs(v.Args[1:], path, emitted)
			}
		case *ast.KeyValueExpr:
			if key, ok := v.Key.(*ast.Ident); ok && key.Name == "Classes" {
				if lit, ok := v.Value.(*ast.CompositeLit); ok {
					addLiteralArgs(lit.Elts, path, emitted)
				}
			}
		case *ast.AssignStmt:
			// The slice's seed value, not just what gets appended to it:
			// `mainCls := []string{"game-header-main"}`.
			for i, lhs := range v.Lhs {
				if i >= len(v.Rhs) || !isClassSlice(lhs) {
					continue
				}
				if lit, ok := v.Rhs[i].(*ast.CompositeLit); ok {
					addLiteralArgs(lit.Elts, path, emitted)
				}
			}
		}
		return true
	})
}

func calleeName(fun ast.Expr) string {
	switch v := fun.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}

// isClassSlice reports whether the append target is the local slice expanders
// accumulate class names into.
func isClassSlice(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && (id.Name == "classes" || id.Name == "cls" || strings.HasSuffix(id.Name, "Cls"))
}

func addLiteralArgs(args []ast.Expr, path string, emitted sites) {
	for _, a := range args {
		lit, ok := a.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		val, err := strconv.Unquote(lit.Value)
		if err != nil {
			continue
		}
		addStatic(emitted, val, path)
	}
}
