package types

import (
	"strings"
	"testing"

	"github.com/lumen-lang/lumen/internal/parser"
)

func TestArrayLiteralAndIndexReadTypecheck(t *testing.T) {
	parseAndCheck(t, `
func main() {
    xs := [1, 2, 3]
    x := xs[1]
    println(x)
}
`)
}

func TestArrayIndexWriteTypecheck(t *testing.T) {
	parseAndCheck(t, `
func main() {
    xs := [1, 2, 3]
    xs[1] = 9
    println(xs[1])
}
`)
}

func TestLenBuiltinOnArrayTypecheck(t *testing.T) {
	parseAndCheck(t, `
func main() {
    xs := [1, 2, 3, 4]
    println(len(xs))
}
`)
}

func TestArrayLiteralMixedTypesErrors(t *testing.T) {
	prog, errs := parser.Parse("test.lm", `
func main() {
    xs := [1, true]
    println(xs)
}
`)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	_, terrs := Check(prog)
	if len(terrs) == 0 {
		t.Fatal("expected type error for mixed array literal elements")
	}
	if !strings.Contains(terrs[0].Error(), "array literal element") {
		t.Fatalf("unexpected error: %v", terrs[0])
	}
}

func TestIndexingNonArrayErrors(t *testing.T) {
	prog, errs := parser.Parse("test.lm", `
func main() {
    x := 1
    y := x[0]
    println(y)
}
`)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	_, terrs := Check(prog)
	if len(terrs) == 0 {
		t.Fatal("expected type error for indexing non-array")
	}
	joined := ""
	for _, e := range terrs {
		joined += e.Error() + "\n"
	}
	if !strings.Contains(joined, "cannot index non-array") {
		t.Fatalf("unexpected errors: %s", joined)
	}
}
