package indexer

import (
	"reflect"
	"sort"
	"testing"
)

// extractInterfaceMethods had no test, which is how it shipped anchored on line
// start only: `interface{ Send(...) }` yielded no methods, so no concrete type
// satisfied that interface and its dispatch fan-out was silently empty. The two
// declaration forms are identical Go — only the formatting differs — so a
// reader of the results had no way to see why one repo resolved and another did
// not. Found by the ceiling probe in
// experiments/eval/benchmarks/in-house/DISPATCH.md.
func TestExtractInterfaceMethods(t *testing.T) {
	for _, tc := range []struct {
		name string
		def  string
		want []string
	}{
		{
			name: "multi-line",
			def:  "interface {\n\tSend(msg string) error\n}",
			want: []string{"Send"},
		},
		{
			name: "single-line",
			def:  "interface{ Send(msg string) error }",
			want: []string{"Send"},
		},
		{
			name: "single-line with two methods",
			def:  "interface{ Send(m string) error; Close() error }",
			want: []string{"Close", "Send"},
		},
		{
			name: "multi-line with several methods",
			def:  "interface {\n\tRead(p []byte) (int, error)\n\tWrite(p []byte) (int, error)\n\tClose() error\n}",
			want: []string{"Close", "Read", "Write"},
		},
		{
			name: "empty interface has no methods",
			def:  "interface{}",
			want: nil,
		},
		{
			name: "embedded interfaces are ignored (documented imprecision)",
			def:  "interface {\n\tReader\n\tWriter\n}",
			want: nil,
		},
		{
			name: "func-typed parameter does not add a phantom method",
			def:  "interface {\n\tDo(f func(int)) error\n}",
			want: []string{"Do"},
		},
		{
			name: "no-arg method on one line",
			def:  "interface{ Close() error }",
			want: []string{"Close"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := dedupeStrings(extractInterfaceMethods(tc.def))
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if len(got) == 0 && len(want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("extractInterfaceMethods(%q) = %v, want %v", tc.def, got, want)
			}
		})
	}
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
