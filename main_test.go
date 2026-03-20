package main

import (
	"testing"
)

const testDiff = `diff --git a/foo.go b/foo.go
index abc1234..def5678 100644
--- a/foo.go
+++ b/foo.go
@@ -10,6 +10,8 @@ func example() {
 	existing := code
+	added := line
+	another := addition
 	more := context
 	still := here
 	end := block
@@ -30,4 +32,3 @@ func other() {
 	keep := this
-	remove := this
 	also := keep
`

func TestParseDiff(t *testing.T) {
	hunks := parseDiff(testDiff)

	if len(hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(hunks))
	}

	h := hunks[0]
	if h.file != "foo.go" {
		t.Errorf("expected file foo.go, got %s", h.file)
	}
	if h.oldStart != 10 {
		t.Errorf("expected oldStart 10, got %d", h.oldStart)
	}
	if h.newStart != 10 {
		t.Errorf("expected newStart 10, got %d", h.newStart)
	}
	if len(h.lines) != 6 {
		t.Errorf("expected 6 lines in first hunk, got %d", len(h.lines))
	}

	h2 := hunks[1]
	if h2.oldStart != 30 {
		t.Errorf("expected oldStart 30, got %d", h2.oldStart)
	}
	if h2.newStart != 32 {
		t.Errorf("expected newStart 32, got %d", h2.newStart)
	}
}

func TestParseDiffNoPrefix(t *testing.T) {
	diff := `diff --git foo.go foo.go
index abc1234..def5678 100644
--- foo.go
+++ foo.go
@@ -1,3 +1,4 @@
 line one
+new line
 line two
 line three
`
	hunks := parseDiff(diff)
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	if hunks[0].file != "foo.go" {
		t.Errorf("expected file foo.go, got %s", hunks[0].file)
	}
}

func TestFirstNewLine(t *testing.T) {
	hunks := parseDiff(testDiff)

	got := hunks[0].firstNewLine()
	if got != 11 {
		t.Errorf("expected first new line 11, got %d", got)
	}

	// Second hunk has only a deletion, so falls back to newStart.
	got = hunks[1].firstNewLine()
	if got != 32 {
		t.Errorf("expected first new line 32, got %d", got)
	}
}

func TestSplitHunk(t *testing.T) {
	// A hunk with two separate change groups.
	diff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,11 +1,13 @@
 context one
 context two
+addition one
 context three
 context four
 context five
 context six
-removal one
+replacement one
 context seven
 context eight
+addition two
 context nine
`
	hunks := parseDiff(diff)
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}

	sub := splitHunk(hunks[0])
	if sub == nil {
		t.Fatal("expected splitHunk to return sub-hunks, got nil")
	}
	if len(sub) != 3 {
		t.Fatalf("expected 3 sub-hunks, got %d", len(sub))
	}

	// Each sub-hunk should have at least one change line.
	for i, s := range sub {
		hasChange := false
		for _, l := range s.lines {
			if len(l) > 0 && (l[0] == '+' || l[0] == '-') {
				hasChange = true
				break
			}
		}
		if !hasChange {
			t.Errorf("sub-hunk %d has no change lines", i)
		}
	}
}

func TestSplitHunkSingleGroup(t *testing.T) {
	diff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 context
+added
 more context
`
	hunks := parseDiff(diff)
	sub := splitHunk(hunks[0])
	if sub != nil {
		t.Errorf("expected nil for single change group, got %d sub-hunks", len(sub))
	}
}

func TestHunkHasAnnotation(t *testing.T) {
	hunks := parseDiff(testDiff)
	a := annotations{
		"foo.go:11": "looks good",
	}

	if !hunkHasAnnotation(hunks[0], a) {
		t.Error("expected first hunk to have annotation")
	}
	if hunkHasAnnotation(hunks[1], a) {
		t.Error("expected second hunk to not have annotation")
	}
}

func TestDiffScope(t *testing.T) {
	s1 := diffScope("diff one")
	s2 := diffScope("diff two")
	s3 := diffScope("diff one")

	if s1 == s2 {
		t.Error("expected different scopes for different diffs")
	}
	if s1 != s3 {
		t.Error("expected same scope for same diff")
	}
	if len(s1) != 64 {
		t.Errorf("expected 64-char hex hash, got %d chars", len(s1))
	}
}
