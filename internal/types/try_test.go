package types

import (
	"strings"
	"testing"

	"github.com/lumen-lang/lumen/internal/parser"
)

func TestTryOptionInOptionFunction(t *testing.T) {
	parseAndCheck(t, `
func inc_if_some(x Option<i64>) Option<i64> {
	v := x?;
    return Option::Some(v + 1)
}
`)
}

func TestTryOptionWrongReturnType(t *testing.T) {
	prog, errs := parser.Parse("test.lm", `
func bad(x Option<i64>) i64 {
	v := x?;
    return v
}
`)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	_, terrs := Check(prog)
	if len(terrs) == 0 {
		t.Fatalf("expected type error for ? in non-Option-returning function")
	}
	got := terrs[0].Error()
	if !strings.Contains(got, "requires enclosing function to return Option") {
		t.Fatalf("unexpected error: %q", got)
	}
}
