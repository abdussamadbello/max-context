package indexer

import (
	"context"
	"testing"
)

const goScopeSample = `package widget

import (
	"fmt"
	cfgpkg "example.com/app/config"
)

type Engine struct{}

func (e *Engine) Start(db *Conn) {
	db.Query()
	e.Stop()
	fmt.Println("hi")
}

func (e *Engine) Stop() {}

func Build() *Engine {
	var local Engine
	local.Start(nil)
	cfgpkg.Load()
	return &local
}
`

func findCall(calls []CallRecord, callee, recv string) *CallRecord {
	for i := range calls {
		if calls[i].CalleeName == callee && calls[i].ReceiverName == recv {
			return &calls[i]
		}
	}
	return nil
}

func TestParseGo_ScopeAndReceivers(t *testing.T) {
	res, err := ParseFile(context.Background(), "widget/engine.go", []byte(goScopeSample))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	// Package stamped on every function.
	for _, f := range res.Functions {
		if f.Package != "widget" {
			t.Errorf("func %q package = %q, want widget", f.Name, f.Package)
		}
	}

	// Methods carry receiver type + kind.
	var start, stop, build *FuncRecord
	for i := range res.Functions {
		switch res.Functions[i].Name {
		case "Start":
			start = &res.Functions[i]
		case "Stop":
			stop = &res.Functions[i]
		case "Build":
			build = &res.Functions[i]
		}
	}
	if start == nil || stop == nil || build == nil {
		t.Fatalf("missing functions: start=%v stop=%v build=%v", start, stop, build)
	}
	if start.Kind != "method" || start.ReceiverType != "Engine" {
		t.Errorf("Start kind=%q recv=%q, want method/Engine", start.Kind, start.ReceiverType)
	}
	if build.Kind != "func" || build.ReceiverType != "" {
		t.Errorf("Build kind=%q recv=%q, want func/empty", build.Kind, build.ReceiverType)
	}

	// db.Query(): receiver is a param of type *Conn -> classified var/Conn.
	if c := findCall(res.Calls, "Query", "db"); c == nil {
		t.Error("missing call db.Query")
	} else if c.ReceiverKind != "var" || c.ReceiverType != "Conn" {
		t.Errorf("db.Query receiverKind=%q type=%q, want var/Conn", c.ReceiverKind, c.ReceiverType)
	}

	// e.Stop(): receiver is the method receiver e of type Engine.
	if c := findCall(res.Calls, "Stop", "e"); c == nil {
		t.Error("missing call e.Stop")
	} else if c.ReceiverKind != "var" || c.ReceiverType != "Engine" {
		t.Errorf("e.Stop receiverKind=%q type=%q, want var/Engine", c.ReceiverKind, c.ReceiverType)
	}

	// fmt.Println(): receiver is an imported package alias -> import.
	if c := findCall(res.Calls, "Println", "fmt"); c == nil {
		t.Error("missing call fmt.Println")
	} else if c.ReceiverKind != "import" {
		t.Errorf("fmt.Println receiverKind=%q, want import", c.ReceiverKind)
	}

	// cfgpkg.Load(): renamed import alias -> import.
	if c := findCall(res.Calls, "Load", "cfgpkg"); c == nil {
		t.Error("missing call cfgpkg.Load")
	} else if c.ReceiverKind != "import" {
		t.Errorf("cfgpkg.Load receiverKind=%q, want import", c.ReceiverKind)
	}

	// local.Start(): local var of type Engine (var local Engine) -> var/Engine.
	if c := findCall(res.Calls, "Start", "local"); c == nil {
		t.Error("missing call local.Start")
	} else if c.ReceiverKind != "var" || c.ReceiverType != "Engine" {
		t.Errorf("local.Start receiverKind=%q type=%q, want var/Engine", c.ReceiverKind, c.ReceiverType)
	}
}
