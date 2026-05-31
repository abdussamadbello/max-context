package indexer

import (
	"context"
	"testing"
)

// --- 9a: return-type inference ---

func TestParseGo_ReturnTypeAndLocalCall(t *testing.T) {
	src := `package app

type Engine struct{}

func (e *Engine) Do() {}

func New() *Engine { return nil }

func twoReturn() (int, error) { return 0, nil }

func run() {
	x := New()
	x.Do()
	y, _ := twoReturn()
	_ = y
}
`
	res, err := ParseFile(context.Background(), "app/a.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	rt := map[string]string{}
	for _, f := range res.Functions {
		rt[f.Name] = f.ReturnType
	}
	if rt["New"] != "Engine" {
		t.Errorf("New return type = %q, want Engine (pointer stripped)", rt["New"])
	}
	if rt["twoReturn"] != "" {
		t.Errorf("twoReturn return type = %q, want empty (multi-return)", rt["twoReturn"])
	}

	if c := findCall(res.Calls, "Do", "x"); c == nil {
		t.Fatal("missing x.Do call")
	} else if c.ReceiverKind != "from-callee" || c.ReceiverFromCallee != "New" {
		t.Errorf("x.Do recvKind=%q fromCallee=%q, want from-callee/New", c.ReceiverKind, c.ReceiverFromCallee)
	}
}

func TestResolve_ReturnTypeInference(t *testing.T) {
	funcs := []FuncRecord{
		{Name: "Do", FilePath: "a.go", Language: "go", Kind: "method", ReceiverType: "Engine", Package: "app"},
		{Name: "New", FilePath: "a.go", Language: "go", Kind: "func", Package: "app", ReturnType: "Engine"},
	}
	r := newResolverFromFuncs(t, funcs)

	// x := New(); x.Do()  -> receiver-typed link to Engine.Do
	id, marker := r.ResolveCall(CallRecord{
		CalleeName: "Do", ReceiverName: "x", ReceiverKind: "from-callee",
		ReceiverFromCallee: "New", Language: "go", FilePath: "a.go",
	}, "app", "a.go")
	if marker != resReceiverTyped || id == 0 {
		t.Errorf("from-callee resolve: id=%d marker=%q, want linked/receiver-typed", id, marker)
	}

	// Unknown callee return -> unresolved.
	_, m2 := r.ResolveCall(CallRecord{
		CalleeName: "Do", ReceiverName: "x", ReceiverKind: "from-callee",
		ReceiverFromCallee: "Mystery", Language: "go", FilePath: "a.go",
	}, "app", "a.go")
	if m2 != resUnresolved {
		t.Errorf("unknown-return resolve marker=%q, want unresolved", m2)
	}
}

// --- 9b: field + global type maps ---

func TestParseGo_StructFieldsAndFieldCall(t *testing.T) {
	src := `package app

type Conn struct{}

func (c *Conn) Query() {}

type Server struct {
	db   *Conn
	name string
}

func (s *Server) handle() {
	s.db.Query()
}
`
	res, err := ParseFile(context.Background(), "app/s.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	// struct field db -> Conn captured (pointer stripped); name -> string captured.
	var dbField *StructFieldRecord
	for i := range res.StructFields {
		if res.StructFields[i].StructType == "Server" && res.StructFields[i].FieldName == "db" {
			dbField = &res.StructFields[i]
		}
	}
	if dbField == nil {
		t.Fatal("missing struct field Server.db")
	}
	if dbField.FieldType != "Conn" {
		t.Errorf("Server.db type = %q, want Conn", dbField.FieldType)
	}

	// s.db.Query() captured as a field call: base s (type Server), field db.
	var fieldCall *CallRecord
	for i := range res.Calls {
		if res.Calls[i].CalleeName == "Query" && res.Calls[i].ReceiverField == "db" {
			fieldCall = &res.Calls[i]
		}
	}
	if fieldCall == nil {
		t.Fatal("missing field call s.db.Query")
	}
	if fieldCall.ReceiverKind != "field" || fieldCall.ReceiverType != "Server" {
		t.Errorf("s.db.Query kind=%q baseType=%q, want field/Server", fieldCall.ReceiverKind, fieldCall.ReceiverType)
	}
}

func TestResolve_FieldAndGlobal(t *testing.T) {
	funcs := []FuncRecord{
		{Name: "Query", FilePath: "a.go", Language: "go", Kind: "method", ReceiverType: "Conn", Package: "app"},
		{Name: "Get", FilePath: "a.go", Language: "go", Kind: "method", ReceiverType: "Store", Package: "app"},
	}
	r := newResolverFromFuncsWithMaps(t, funcs,
		[]StructFieldRecord{{StructType: "Server", FieldName: "db", FieldType: "Conn", Package: "app", FilePath: "a.go"}},
		[]PackageVarRecord{{Name: "store", VarType: "Store", Package: "app", FilePath: "a.go"}},
	)

	// 9b field: s.db.Query() where s is *Server -> Conn.Query
	id, marker := r.ResolveCall(CallRecord{
		CalleeName: "Query", ReceiverName: "s.db", ReceiverField: "db",
		ReceiverKind: "field", ReceiverType: "Server", Language: "go", FilePath: "a.go",
	}, "app", "a.go")
	if marker != resReceiverTyped || id == 0 {
		t.Errorf("field resolve: id=%d marker=%q, want linked/receiver-typed", id, marker)
	}

	// 9b global: store.Get() where package var store is Store -> Store.Get
	id2, m2 := r.ResolveCall(CallRecord{
		CalleeName: "Get", ReceiverName: "store", ReceiverKind: "maybe-global",
		Language: "go", FilePath: "z.go",
	}, "app", "z.go")
	if m2 != resReceiverTyped || id2 == 0 {
		t.Errorf("global resolve: id=%d marker=%q, want linked/receiver-typed", id2, m2)
	}

	// Unknown field type -> unresolved.
	_, m3 := r.ResolveCall(CallRecord{
		CalleeName: "Query", ReceiverName: "s.missing", ReceiverField: "missing",
		ReceiverKind: "field", ReceiverType: "Server", Language: "go", FilePath: "a.go",
	}, "app", "a.go")
	if m3 != resUnresolved {
		t.Errorf("unknown-field marker=%q, want unresolved", m3)
	}
}
