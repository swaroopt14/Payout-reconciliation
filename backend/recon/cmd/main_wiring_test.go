package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestRecon_SinksWiredBeforeConsumerStart guards the startup order in main():
// the observation and bank Kafka consumers must start only after the sinks
// their handlers use are wired. main() needs Postgres, Kafka and S3, so this
// checks statement order in the source AST instead of running it.
func TestRecon_SinksWiredBeforeConsumerStart(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	var mainFn *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "main" {
			mainFn = fd
		}
	}
	if mainFn == nil {
		t.Fatal("main() not found")
	}

	src := func(n ast.Node) string {
		var b strings.Builder
		ast.Inspect(n, func(x ast.Node) bool {
			switch v := x.(type) {
			case *ast.SelectorExpr:
				if id, ok := v.X.(*ast.Ident); ok {
					b.WriteString(id.Name + "." + v.Sel.Name + " ")
				}
			}
			return true
		})
		return b.String()
	}

	first := map[string]token.Pos{}
	mark := func(key string, pos token.Pos) {
		if _, ok := first[key]; !ok {
			first[key] = pos
		}
	}
	ast.Inspect(mainFn.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			s := src(v.Lhs[0])
			for _, sink := range []string{"Refunds", "Edges", "Transfers"} {
				if strings.Contains(s, "observationProc."+sink) {
					mark("sink:"+sink, v.Pos())
				}
			}
		case *ast.CallExpr:
			s := src(v)
			if strings.HasPrefix(s, "handlers.SetBankIngestService") {
				mark("sink:BankIngest", v.Pos())
			}
			if strings.HasPrefix(s, "kafka.StartConsumer") {
				if strings.Contains(s, "handlers.HandleProviderObservation") {
					mark("start:observation", v.Pos())
				}
				if strings.Contains(s, "handlers.HandleBankStatementReceived") {
					mark("start:bank", v.Pos())
				}
			}
		}
		return true
	})

	for _, key := range []string{"sink:Refunds", "sink:Edges", "sink:Transfers", "sink:BankIngest", "start:observation", "start:bank"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("%s not found in main()", key)
		}
	}
	for _, start := range []string{"start:observation", "start:bank"} {
		for _, sink := range []string{"sink:Refunds", "sink:Edges", "sink:Transfers", "sink:BankIngest"} {
			if first[start] < first[sink] {
				t.Errorf("%s (line %d) begins before %s is wired (line %d)", start,
					fset.Position(first[start]).Line, sink, fset.Position(first[sink]).Line)
			}
		}
	}
}
