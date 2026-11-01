// SPDX-License-Identifier: AGPL-3.0-only

package advice

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Every suggestion title, "why" text, step and note (Limits) a rule can produce must have a German text, or the
// German Suggestions page shows English sentences (#195). The rule sources are read with go/ast: string literals and
// fmt.Sprintf formats assigned to Title / Why / Limits / Steps / Evidence are matched against the dictionaries in
// web/src/lib/i18n (a placeholder such as %s or %d matches a {name} placeholder of the key).
func TestSuggestionTextsHaveGerman(t *testing.T) {
	dictFiles, _ := filepath.Glob("../../web/src/lib/i18n/*.js")
	if len(dictFiles) == 0 {
		t.Skip("web sources not found")
	}
	reKey := regexp.MustCompile(`(?m)^\s*"((?:[^"\\]|\\.)*)"\s*:`)
	rePH := regexp.MustCompile(`\{\w+\}`)
	have := map[string]bool{}
	for _, f := range dictFiles {
		b, _ := os.ReadFile(f)
		for _, m := range reKey.FindAllStringSubmatch(string(b), -1) {
			k, err := strconv.Unquote(`"` + m[1] + `"`)
			if err != nil {
				k = m[1]
			}
			have[rePH.ReplaceAllString(k, "{}")] = true
		}
	}
	reVerb := regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z]`)

	files, _ := filepath.Glob("rules_*.go")
	fset := token.NewFileSet()
	missing := map[string]bool{}
	check := func(e ast.Expr) {
		var s string
		switch x := e.(type) {
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				return
			}
			s, _ = strconv.Unquote(x.Value)
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Sprintf" || len(x.Args) == 0 {
				return
			}
			lit, ok := x.Args[0].(*ast.BasicLit)
			if !ok {
				return
			}
			s, _ = strconv.Unquote(lit.Value)
			s = strings.ReplaceAll(s, "%%", "\x00")
			s = reVerb.ReplaceAllString(s, "{}")
			s = strings.ReplaceAll(s, "\x00", "%")
		default:
			return
		}
		if strings.TrimSpace(s) == "" || !strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyz") {
			return
		}
		if !have[s] {
			missing[s] = true
		}
	}
	var steps func(e ast.Expr)
	steps = func(e ast.Expr) {
		switch x := e.(type) {
		case *ast.CompositeLit:
			for _, el := range x.Elts {
				check(el)
			}
		case *ast.CallExpr: // append(steps, "…")
			if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "append" {
				for i, a := range x.Args {
					if i == 0 {
						steps(a) // append([]string{"first line"}, rest...)
						continue
					}
					check(a)
				}
			}
		}
	}
	field := func(name string, v ast.Expr) {
		switch name {
		case "Title", "Why", "Limits":
			check(v)
		case "Steps", "Evidence":
			steps(v)
		}
	}
	for _, f := range files {
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.KeyValueExpr:
				if id, ok := x.Key.(*ast.Ident); ok {
					field(id.Name, x.Value)
				}
			case *ast.AssignStmt:
				for i, l := range x.Lhs {
					if sel, ok := l.(*ast.SelectorExpr); ok && i < len(x.Rhs) {
						field(sel.Sel.Name, x.Rhs[i])
					}
				}
			}
			return true
		})
	}
	var list []string
	for s := range missing {
		list = append(list, s)
	}
	sort.Strings(list)
	for _, s := range list {
		t.Errorf("no German text for suggestion text: %q", s)
	}
}
