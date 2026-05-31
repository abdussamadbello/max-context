package grade

import "testing"

func TestParseJudgeJSON_Clean(t *testing.T) {
	j, err := parseJudgeJSON(`{"items":{"file":1,"locus":2},"reasoning":"correct file and locus"}`)
	if err != nil {
		t.Fatal(err)
	}
	if j.Items["file"] != 1 || j.Items["locus"] != 2 {
		t.Errorf("items wrong: %+v", j.Items)
	}
}

func TestParseJudgeJSON_Fenced(t *testing.T) {
	raw := "Here is my scoring:\n```json\n{\"items\":{\"file\":1},\"reasoning\":\"ok\"}\n```\nDone."
	j, err := parseJudgeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if j.Items["file"] != 1 {
		t.Errorf("fenced parse failed: %+v", j)
	}
}

func TestParseJudgeJSON_Garbage(t *testing.T) {
	if _, err := parseJudgeJSON("no json here"); err == nil {
		t.Error("expected error on garbage")
	}
}
