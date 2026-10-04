package main

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The end-to-end plan is a document, so nothing compiles against it and
// nothing noticed when two rows came to share a number — twice in two days,
// both times because branches that could not see each other each took what
// looked like the next free one. A run below the tables, and the plans, say
// "9.62 passed"; with two rows called 9.62 that sentence means nothing.
//
// This is the only test here that reads a file of the repository rather
// than its own package. It lives in cmd/conch because a test has to live in
// some package and this one is about the repository, and it skips when the
// plan is not beside it, so a checkout without the docs still tests.
const e2ePlan = "../../docs/testing/end-to-end.md"

var e2eRow = regexp.MustCompile(`^\|\s*(\d+)\.(\d+)\s*[^|]*\|\s*([^|]*)`)

// statusMarks end a row of the plan. The file explains the plan as well as
// holding it, and those explanations have tables whose first column is a
// row's number too, so the mark is what tells a row from a sentence about
// one — found when a table added here was read as a duplicate of the row it
// was describing.
const statusMarks = "☐◐✅✓"

// planRow reports whether a line is a row of a plan table rather than prose
// about one, and gives its number and title.
func planRow(line string) (sec, n int, title string, ok bool) {
	m := e2eRow.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, "", false
	}
	// \| is an escaped pipe inside a cell, not a cell of its own.
	cells := strings.Split(strings.ReplaceAll(line, `\|`, "\x00"), "|")
	last := ""
	for i := len(cells) - 1; i >= 0; i-- {
		if c := strings.TrimSpace(cells[i]); c != "" {
			last = c
			break
		}
	}
	if !strings.ContainsAny(last, statusMarks) {
		return 0, 0, "", false
	}
	sec, _ = strconv.Atoi(m[1])
	n, _ = strconv.Atoi(m[2])
	return sec, n, strings.TrimSpace(m[3]), true
}

func TestEndToEndPlanNumbersAreUnique(t *testing.T) {
	b, err := os.ReadFile(e2ePlan)
	if os.IsNotExist(err) {
		t.Skip("no end-to-end plan in this tree")
	}
	if err != nil {
		t.Fatal(err)
	}
	type where struct {
		line  int
		title string
	}
	lines := strings.Split(string(b), "\n")

	// The highest number in each section first, so the hint a duplicate
	// prints is the next free one in the whole plan and not merely the next
	// after the rows read so far.
	highest := map[int]int{}
	rows := 0
	for _, line := range lines {
		if sec, n, _, ok := planRow(line); ok {
			if n > highest[sec] {
				highest[sec] = n
			}
			rows++
		}
	}
	if rows == 0 {
		t.Fatal("no rows found: the plan's tables have changed shape")
	}

	seen := map[string]where{}
	for i, line := range lines {
		sec, n, title, ok := planRow(line)
		if !ok {
			continue
		}
		id := strconv.Itoa(sec) + "." + strconv.Itoa(n)
		if first, dup := seen[id]; dup {
			t.Errorf("row %s is used twice: line %d %q and line %d %q\n"+
				"the row that had it first keeps it; the next free number in section %d is %d.%d",
				id, first.line, first.title, i+1, title, sec, sec, highest[sec]+1)
			continue
		}
		seen[id] = where{i + 1, title}
	}
}
