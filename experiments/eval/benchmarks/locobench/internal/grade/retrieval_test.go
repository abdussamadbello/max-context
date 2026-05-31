package grade

import (
	"testing"

	"github.com/maxcontext/eval-locobench/internal/spec"
)

func TestExtractFiles(t *testing.T) {
	ans := "The URL class is defined in `httpx/_urls.py`. See also httpx/_models.py:515 and ./src/core/index.ts."
	got := ExtractFiles(ans)
	want := map[string]bool{"httpx/_urls.py": true, "httpx/_models.py": true, "src/core/index.ts": true}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected extracted path %q", g)
		}
		delete(want, g)
	}
	if len(want) != 0 {
		t.Errorf("missed paths: %v", want)
	}
}

func TestGradeRetrieval_Correct(t *testing.T) {
	key := spec.Key{ExpectedFiles: []string{"httpx/_urls.py"}}
	s := GradeRetrieval("The URL class lives in httpx/_urls.py.", key)
	if !s.Hit || s.Recall != 1.0 || s.Precision != 1.0 {
		t.Errorf("expected perfect score, got %+v", s)
	}
}

func TestGradeRetrieval_SuffixTolerant(t *testing.T) {
	key := spec.Key{ExpectedFiles: []string{"httpx/_urls.py"}}
	s := GradeRetrieval("It's in _urls.py.", key)
	if !s.Hit {
		t.Errorf("suffix match should hit, got %+v", s)
	}
}

func TestGradeRetrieval_WrongFileLowersPrecision(t *testing.T) {
	key := spec.Key{ExpectedFiles: []string{"httpx/_urls.py"}}
	// names the right file plus two unrelated ones
	s := GradeRetrieval("Maybe httpx/_urls.py, or httpx/_models.py, or httpx/_api.py.", key)
	if s.Recall != 1.0 {
		t.Errorf("recall should be 1.0 (gold covered), got %v", s.Recall)
	}
	if s.Precision >= 1.0 {
		t.Errorf("precision should drop below 1.0 with extra files, got %v", s.Precision)
	}
}

func TestGradeRetrieval_Miss(t *testing.T) {
	key := spec.Key{ExpectedFiles: []string{"httpx/_urls.py"}}
	s := GradeRetrieval("I think it's in httpx/_api.py.", key)
	if s.Hit || s.Recall != 0 {
		t.Errorf("expected miss, got %+v", s)
	}
	if len(s.Missed) != 1 {
		t.Errorf("expected 1 missed gold, got %v", s.Missed)
	}
}

// TestGradeRetrieval_LeadFileDismissesExtras locks in the fix: an answer that
// leads with the correct file but names others to DISMISS them is correct.
func TestGradeRetrieval_LeadFileDismissesExtras(t *testing.T) {
	key := spec.Key{ExpectedFiles: []string{"httpx/_urls.py"}}
	ans := "The URL class is defined in httpx/_urls.py. The matches in httpx/_models.py and tests/conftest.py are just methods that return a URL, not the class."
	s := GradeRetrieval(ans, key)
	if s.LeadFile != "httpx/_urls.py" {
		t.Errorf("lead file = %q, want httpx/_urls.py", s.LeadFile)
	}
	if !s.LeadCorrect {
		t.Error("lead should be correct (answer endorses _urls.py)")
	}
	// set-F1 is still deflated (that's why we use LeadCorrect for quality):
	if s.Precision >= 1.0 {
		t.Logf("note: set precision %.2f deflated by dismissed files — lead-file fix bypasses this", s.Precision)
	}
}

// TestGradeRetrieval_WrongLead: leading with the wrong file is not rescued.
func TestGradeRetrieval_WrongLead(t *testing.T) {
	key := spec.Key{ExpectedFiles: []string{"httpx/_urls.py"}}
	ans := "It's defined in httpx/_models.py, though httpx/_urls.py also has URL stuff."
	s := GradeRetrieval(ans, key)
	if s.LeadCorrect {
		t.Error("lead file is _models.py (wrong) — must not be marked correct")
	}
}
