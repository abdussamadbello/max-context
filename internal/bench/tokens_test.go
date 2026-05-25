package bench

import "testing"

func TestCountTokens_NonEmpty(t *testing.T) {
	c, err := NewCounter()
	if err != nil {
		t.Fatalf("NewCounter: %v", err)
	}
	n := c.Count("Hello, world!")
	if n <= 0 {
		t.Fatalf("expected positive token count, got %d", n)
	}
}

func TestCountTokens_LongerStringMoreTokens(t *testing.T) {
	c, err := NewCounter()
	if err != nil {
		t.Fatalf("NewCounter: %v", err)
	}
	short := c.Count("hi")
	long := c.Count("this is a noticeably longer string with many words")
	if long <= short {
		t.Fatalf("longer should produce more tokens (short=%d, long=%d)", short, long)
	}
}
