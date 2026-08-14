package indexer

import (
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

// newResolverFromFuncs inserts the given functions into a fresh DB and builds a
// resolver from them, mirroring what Index does within its transaction.
func newResolverFromFuncs(t *testing.T, funcs []FuncRecord) *Resolver {
	return newResolverFromFuncsWithMaps(t, funcs, nil, nil)
}

// newResolverFromFuncsWithMaps additionally seeds the 9b struct-field and
// package-var type maps.
func newResolverFromFuncsWithMaps(t *testing.T, funcs []FuncRecord, fields []StructFieldRecord, vars []PackageVarRecord) *Resolver {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for _, f := range funcs {
		if err := insertFunction(tx, f); err != nil {
			t.Fatalf("insertFunction: %v", err)
		}
	}
	for _, sf := range fields {
		if err := insertStructField(tx, sf); err != nil {
			t.Fatalf("insertStructField: %v", err)
		}
	}
	for _, pv := range vars {
		if err := insertPackageVar(tx, pv); err != nil {
			t.Fatalf("insertPackageVar: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	r, err := NewResolver(database)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r
}

func TestResolveCall_DecisionTree(t *testing.T) {
	funcs := []FuncRecord{
		{Name: "Helper", FilePath: "a.go", Language: "go", Kind: "func", Package: "app"},
		{Name: "Run", FilePath: "b.go", Language: "go", Kind: "func", Package: "app"},
		{Name: "Start", FilePath: "c.go", Language: "go", Kind: "method", ReceiverType: "Engine", Package: "app"},
		{Name: "Start", FilePath: "d.go", Language: "go", Kind: "method", ReceiverType: "Server", Package: "app"},
	}
	r := newResolverFromFuncs(t, funcs)

	cases := []struct {
		name       string
		call       CallRecord
		callerPkg  string
		callerFile string
		wantMarker string
		wantLinked bool
	}{
		{
			name:      "same-file bare call",
			call:      CallRecord{CalleeName: "Helper", Language: "go", FilePath: "a.go"},
			callerPkg: "app", callerFile: "a.go",
			wantMarker: resSameFile, wantLinked: true,
		},
		{
			name:      "same-package bare call",
			call:      CallRecord{CalleeName: "Helper", Language: "go", FilePath: "z.go"},
			callerPkg: "app", callerFile: "z.go",
			wantMarker: resSamePackage, wantLinked: true,
		},
		{
			name:      "receiver-typed method call picks the right type",
			call:      CallRecord{CalleeName: "Start", ReceiverName: "e", ReceiverKind: "var", ReceiverType: "Engine", Language: "go", FilePath: "z.go"},
			callerPkg: "app", callerFile: "z.go",
			wantMarker: resReceiverTyped, wantLinked: true,
		},
		{
			name:      "import-qualified call is classified, not linked",
			call:      CallRecord{CalleeName: "Run", ReceiverName: "other", ReceiverKind: "import", Language: "go", FilePath: "z.go"},
			callerPkg: "app", callerFile: "z.go",
			wantMarker: resImportQualified, wantLinked: false,
		},
		{
			name:      "builtin bare call not linked",
			call:      CallRecord{CalleeName: "make", Language: "go", FilePath: "z.go"},
			callerPkg: "app", callerFile: "z.go",
			wantMarker: resBuiltin, wantLinked: false,
		},
		{
			name:      "unknown receiver type unresolved",
			call:      CallRecord{CalleeName: "Frob", ReceiverName: "x", ReceiverKind: "var", ReceiverType: "Mystery", Language: "go", FilePath: "z.go"},
			callerPkg: "app", callerFile: "z.go",
			wantMarker: resUnresolved, wantLinked: false,
		},
		{
			name:      "selector with unclassified receiver unresolved",
			call:      CallRecord{CalleeName: "Do", ReceiverName: "chained", Language: "go", FilePath: "z.go"},
			callerPkg: "app", callerFile: "z.go",
			wantMarker: resUnresolved, wantLinked: false,
		},
		{
			name:      "non-go falls back to name-global",
			call:      CallRecord{CalleeName: "Run", Language: "javascript", FilePath: "z.js"},
			callerPkg: "", callerFile: "z.js",
			wantMarker: resNameGlobal, wantLinked: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, marker := r.ResolveCall(tc.call, tc.callerPkg, tc.callerFile)
			if marker != tc.wantMarker {
				t.Errorf("marker = %q, want %q", marker, tc.wantMarker)
			}
			if linked := id != 0; linked != tc.wantLinked {
				t.Errorf("linked = %v (id=%d), want %v", linked, id, tc.wantLinked)
			}
		})
	}

	// Receiver-typed must disambiguate by type: Engine.Start and Server.Start
	// share a name but must resolve to different definitions.
	idEngine, _ := r.ResolveCall(
		CallRecord{CalleeName: "Start", ReceiverKind: "var", ReceiverType: "Engine", Language: "go", FilePath: "z.go"},
		"app", "z.go",
	)
	idServer, _ := r.ResolveCall(
		CallRecord{CalleeName: "Start", ReceiverKind: "var", ReceiverType: "Server", Language: "go", FilePath: "z.go"},
		"app", "z.go",
	)
	if idEngine == 0 || idServer == 0 {
		t.Fatalf("expected both Start methods to link (engine=%d server=%d)", idEngine, idServer)
	}
	if idEngine == idServer {
		t.Errorf("Engine.Start and Server.Start resolved to same id %d", idEngine)
	}
}
