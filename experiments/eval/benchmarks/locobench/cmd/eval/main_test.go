package main

import (
	"reflect"
	"testing"

	"github.com/maxcontext/eval-locobench/internal/runlog"
)

func TestParseArmsIncludesContext(t *testing.T) {
	want := []runlog.Arm{runlog.ArmGrep, runlog.ArmMaxContext, runlog.ArmContext}
	got, err := parseArms("grep,max-context,context")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arms = %v, want %v", got, want)
	}
}

func TestParseArmsRejectsUnknown(t *testing.T) {
	if _, err := parseArms("grep,typo"); err == nil {
		t.Fatal("expected unknown arm error")
	}
}
