package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lumen-lang/lumen/internal/borrowck"
	"github.com/lumen-lang/lumen/internal/cbackend"
	"github.com/lumen-lang/lumen/internal/infer"
	"github.com/lumen-lang/lumen/internal/types"
)

func writeModule(t *testing.T, dir, name, src string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(src), 0644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func buildAndRunAOT(t *testing.T, entry string) string {
	t.Helper()
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skipf("cc not available: %v", err)
	}

	prog, perrs := loadProgramWithImports(entry)
	if len(perrs) > 0 {
		t.Fatalf("load/parse errors: %v", perrs)
	}
	info, terrs := types.Check(prog)
	if len(terrs) > 0 {
		t.Fatalf("type errors: %v", terrs)
	}

	infer.SelfMut(prog, info)
	infer.ParamMut(prog, info)
	infer.ParamBorrow(prog, info)
	infer.Mutability(prog, info)
	if berrs := borrowck.Check(prog, info); len(berrs) > 0 {
		t.Fatalf("borrowck errors: %v", berrs)
	}

	out := filepath.Join(t.TempDir(), "aot_bin")
	if err := cbackend.Compile(prog, info, cbackend.Options{Output: out}); err != nil {
		t.Fatalf("AOT compile failed: %v", err)
	}
	b, err := exec.Command(out).CombinedOutput()
	if err != nil {
		t.Fatalf("AOT run failed: %v\noutput:\n%s", err, string(b))
	}
	return string(b)
}

func TestLoadProgramWithImports_MixedFeatureSuccess(t *testing.T) {
	d := t.TempDir()
	writeModule(t, d, "util.lm", `
func util_double(x i64) i64 {
    return x * 2
}
`)
	entry := writeModule(t, d, "main.lm", `
import "util"

func compute() Option<i64> {
    var nums Vec<i64> = Vec::new()
    nums.push(10)
    nums.push(20)

    var sum i64 = 0
    for n in nums {
        sum = sum + n
    }

    var m HashMap<String, i64> = HashMap::new()
    m.insert("sum", sum)

    var got Option<i64> = m.get("sum")
    var base i64 = got?;

    var bump func(i64) i64 = func(x i64) i64 {
        return util_double(x)
    }
    return Option::Some(bump(base))
}

func main() {
    println(compute().unwrap())
}
`)

	prog, perrs := loadProgramWithImports(entry)
	if len(perrs) > 0 {
		t.Fatalf("load/parse errors: %v", perrs)
	}
	_, terrs := types.Check(prog)
	if len(terrs) > 0 {
		t.Fatalf("type errors: %v", terrs)
	}
}

func TestLoadProgramWithImports_MixedFeatureErrorSnapshot(t *testing.T) {
	d := t.TempDir()
	writeModule(t, d, "util.lm", `
func util_id(x i64) i64 { return x }
`)
	entry := writeModule(t, d, "main.lm", `
import "util"

func bad() i64 {
    var m HashMap<String, i64> = HashMap::new()
    m.insert("x", 1)

    var got Option<i64> = m.get("x")
    var v i64 = got?;

    var idf func(i64) i64 = func(x i64) i64 { return x }
    return util_id(idf(v))
}

func main() {
    println(bad())
}
`)

	prog, perrs := loadProgramWithImports(entry)
	if len(perrs) > 0 {
		t.Fatalf("load/parse errors: %v", perrs)
	}
	_, terrs := types.Check(prog)
	if len(terrs) == 0 {
		t.Fatalf("expected type error, got none")
	}

	all := make([]string, 0, len(terrs))
	for _, e := range terrs {
		all = append(all, e.Error())
	}
	snapshot := strings.Join(all, "\n")
	if !strings.Contains(snapshot, "`?` on Option requires enclosing function to return Option") {
		t.Fatalf("unexpected error snapshot:\n%s", snapshot)
	}
}

func TestAOT_LambdaLoweringAndCall(t *testing.T) {
	d := t.TempDir()
	entry := writeModule(t, d, "main.lm", `
func main() {
    var twice func(i64) i64 = func(x i64) i64 { return x * 2 }
    println(twice(21))
}
`)
	out := buildAndRunAOT(t, entry)
	if !strings.Contains(out, "42") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestAOT_TryOptionEarlyReturn(t *testing.T) {
	d := t.TempDir()
	entry := writeModule(t, d, "main.lm", `
func add1(x Option<i64>) Option<i64> {
    var v i64 = x?;
    return Option::Some(v + 1)
}

func main() {
    println(add1(Option::Some(41)).unwrap_or(-1))
	println(add1(Option::None()).unwrap_or(-1))
}
`)
	out := buildAndRunAOT(t, entry)
	if !strings.Contains(out, "42") || !strings.Contains(out, "-1") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestAOT_MultiFileImportTransitive(t *testing.T) {
	d := t.TempDir()
	writeModule(t, d, "a.lm", `
func a_val() i64 { return 7 }
`)
	writeModule(t, d, "b.lm", `
import "a"

func b_val() i64 { return a_val() + 5 }
`)
	entry := writeModule(t, d, "main.lm", `
import "b"

func main() {
    println(b_val())
}
`)
	out := buildAndRunAOT(t, entry)
	if !strings.Contains(out, "12") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestAOT_CapturingClosure(t *testing.T) {
	d := t.TempDir()
	entry := writeModule(t, d, "main.lm", `
func main() {
    var x i64 = 10
    var add_x func(i64) i64 = func(n i64) i64 { return n + x }
    println(add_x(32))

    var a i64 = 3
    var b i64 = 7
    var sum func(i64) i64 = func(n i64) i64 { return n + a + b }
    println(sum(0))
}
`)
	out := buildAndRunAOT(t, entry)
	if !strings.Contains(out, "42") {
		t.Fatalf("capturing closure (single): unexpected output: %q", out)
	}
	if !strings.Contains(out, "10") {
		t.Fatalf("capturing closure (multi): unexpected output: %q", out)
	}
}

func TestLoadProgramWithImports_CycleError(t *testing.T) {
	d := t.TempDir()
	entry := writeModule(t, d, "a.lm", `
import "b"
func a() i64 { return 1 }
`)
	writeModule(t, d, "b.lm", `
import "a"
func b() i64 { return 2 }
`)
	_, perrs := loadProgramWithImports(entry)
	if len(perrs) == 0 {
		t.Fatalf("expected cycle error, got none")
	}
	joined := strings.Join(perrs, "\n")
	if !strings.Contains(joined, "import cycle") {
		t.Fatalf("unexpected errors: %s", joined)
	}
}
