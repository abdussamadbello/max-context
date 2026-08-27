package bench

import "testing"

func TestCountTokens_NonEmpty(t *testing.T) {
	c, err := NewCounter()
	if err != nil {
		t.Fatalf("NewCounter: %v", err)
	}
	n, err := c.Count("Hello, world!")
	if err != nil {
		t.Fatal(err)
	}
	if n <= 0 {
		t.Fatalf("expected positive token count, got %d", n)
	}
}

func TestCountTokens_LongerStringMoreTokens(t *testing.T) {
	c, err := NewCounter()
	if err != nil {
		t.Fatalf("NewCounter: %v", err)
	}
	short, err := c.Count("hi")
	if err != nil {
		t.Fatal(err)
	}
	long, err := c.Count("this is a noticeably longer string with many words")
	if err != nil {
		t.Fatal(err)
	}
	if long <= short {
		t.Fatalf("longer should produce more tokens (short=%d, long=%d)", short, long)
	}
}

func TestCountTokens_CL100KReferenceValue(t *testing.T) {
	c, err := NewCounter()
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Count("hello world")
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 { // cl100k_base token IDs: 15339, 1917
		t.Fatalf("Count(hello world) = %d, want 2", got)
	}
}
