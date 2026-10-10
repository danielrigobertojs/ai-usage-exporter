// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package privacy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allowlist is intentionally empty. Every future exception must name a file
// and include a justification here; an unexplained exception is a leak.
var allowlist = map[string]string{}

var contentNames = map[string]bool{
	"line": true, "raw": true, "content": true, "text": true, "body": true,
	"data": true, "prompt": true, "message": true,
}

func TestPrivacyStaticAudit(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	var files []string
	err := filepath.WalkDir(root, func(filename string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && strings.HasSuffix(filename, ".go") {
			files = append(files, filename)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, filename := range files {
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filename, nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", filename, err)
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if allowed(filename) {
				return true
			}
			if isUnsafeErrorf(call) {
				t.Errorf("%s: fmt.Errorf renders a content variable", filename)
			}
			if isLogPrint(call) {
				t.Errorf("%s: log.Print* bypasses structured logger", filename)
			}
			if strings.Contains(filepath.ToSlash(filename), "/internal/provider/") && isProviderWrite(call) {
				t.Errorf("%s: provider opens or writes a source with write capability", filename)
			}
			return true
		})
	}
}

func allowed(filename string) bool { _, ok := allowlist[filepath.ToSlash(filename)]; return ok }

func isUnsafeErrorf(c *ast.CallExpr) bool {
	s, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := s.X.(*ast.Ident)
	if !ok || x.Name != "fmt" || s.Sel.Name != "Errorf" || len(c.Args) < 2 {
		return false
	}
	format, ok := c.Args[0].(*ast.BasicLit)
	if !ok || !strings.ContainsAny(format.Value, "svq") {
		return false
	}
	for _, arg := range c.Args[1:] {
		if id, ok := arg.(*ast.Ident); ok && contentNames[id.Name] {
			return true
		}
	}
	return false
}

func isLogPrint(c *ast.CallExpr) bool {
	s, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || !strings.HasPrefix(s.Sel.Name, "Print") {
		return false
	}
	x, ok := s.X.(*ast.Ident)
	return ok && x.Name == "log"
}

func isProviderWrite(c *ast.CallExpr) bool {
	s, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := s.X.(*ast.Ident)
	if !ok || x.Name != "os" {
		return false
	}
	if s.Sel.Name == "Create" || s.Sel.Name == "WriteFile" {
		return true
	}
	if s.Sel.Name != "OpenFile" || len(c.Args) < 2 {
		return false
	}
	write := false
	ast.Inspect(c.Args[1], func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || !strings.HasPrefix(sel.Sel.Name, "O_") {
			return true
		}
		if sel.Sel.Name == "O_WRONLY" || sel.Sel.Name == "O_RDWR" || sel.Sel.Name == "O_CREATE" {
			write = true
		}
		return true
	})
	return write
}
