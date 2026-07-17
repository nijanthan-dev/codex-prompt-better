// Package reviewpatterns enforces recurring repository-wide review invariants.
package reviewpatterns

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ValidateTree returns deterministic diagnostics for unsafe source patterns.
func ValidateTree(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "vendor") {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	set := token.NewFileSet()
	var failures []string
	for _, path := range paths {
		file, err := parser.ParseFile(set, path, nil, 0)
		if err != nil {
			return nil, err
		}
		jsonNames := importedNames(file, "encoding/json")
		ast.Inspect(file, func(node ast.Node) bool {
			function, ok := node.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				return true
			}
			decoders := make(map[string]int)
			ast.Inspect(function.Body, func(child ast.Node) bool {
				assignment, ok := child.(*ast.AssignStmt)
				if !ok || len(assignment.Lhs) != len(assignment.Rhs) {
					return true
				}
				for index, right := range assignment.Rhs {
					call, ok := right.(*ast.CallExpr)
					identifier, named := assignment.Lhs[index].(*ast.Ident)
					if ok && named && selectorIn(call.Fun, jsonNames, "NewDecoder") {
						decoders[identifier.Name] = 0
					}
				}
				return true
			})
			ast.Inspect(function.Body, func(child ast.Node) bool {
				call, ok := child.(*ast.CallExpr)
				if !ok {
					return true
				}
				selected, ok := call.Fun.(*ast.SelectorExpr)
				identifier, named := selectedExpression(selected)
				if ok && named && selected.Sel.Name == "Decode" {
					if _, tracked := decoders[identifier.Name]; tracked {
						decoders[identifier.Name]++
					}
				}
				return true
			})
			for _, decodes := range decoders {
				if decodes >= 2 {
					continue
				}
				position := set.Position(function.Pos())
				failures = append(failures, fmt.Sprintf("%s:%d: JSON decoder must reject trailing documents", relative(root, path), position.Line))
			}
			return false
		})
	}
	return failures, nil
}

func selectorIn(expression ast.Expr, packageNames map[string]bool, name string) bool {
	selected, ok := expression.(*ast.SelectorExpr)
	if !ok || selected.Sel.Name != name {
		return false
	}
	identifier, ok := selected.X.(*ast.Ident)
	return ok && packageNames[identifier.Name]
}

func selectedExpression(selected *ast.SelectorExpr) (*ast.Ident, bool) {
	if selected == nil {
		return nil, false
	}
	identifier, ok := selected.X.(*ast.Ident)
	return identifier, ok
}

func importedNames(file *ast.File, path string) map[string]bool {
	names := map[string]bool{}
	for _, spec := range file.Imports {
		if strings.Trim(spec.Path.Value, `"`) != path {
			continue
		}
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		names[name] = true
	}
	return names
}

func relative(root, path string) string {
	value, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(value)
}
