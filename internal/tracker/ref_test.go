package tracker

import "testing"

func TestParseRef(t *testing.T) {
	cases := []struct {
		in, tracker, key string
		err              bool
	}{
		{"ENG-123", "", "ENG-123", false},
		{"eng-123", "", "ENG-123", false},
		{"  PROJ-42 ", "", "PROJ-42", false},
		{"A1_B-7", "", "A1_B-7", false},
		{"linear:ENG-123", "linear", "ENG-123", false},
		{"Jira:proj-42", "jira", "PROJ-42", false},
		{"https://linear.app/acme/issue/ENG-123/fix-the-thing", "linear", "ENG-123", false},
		{"https://linear.app/acme/issue/ENG-123", "linear", "ENG-123", false},
		{"https://acme.atlassian.net/browse/PROJ-42", "jira", "PROJ-42", false},
		{"https://acme.atlassian.net/jira/software/projects/PROJ/boards/1?selectedIssue=PROJ-9", "jira", "PROJ-9", false},
		{"", "", "", true},
		{"123", "", "", true},
		{"ENG-", "", "", true},
		{"-12", "", "", true},
		{"ENG-12a", "", "", true},
		{"1ENG-12", "", "", true},
		{"github:ENG-1", "", "", true},
		{"linear:", "", "", true},
		{"https://example.com/ENG-1", "", "", true},
	}
	for _, c := range cases {
		tr, key, err := ParseRef(c.in)
		if (err != nil) != c.err || tr != c.tracker || key != c.key {
			t.Errorf("ParseRef(%q) = %q, %q, %v; want %q, %q, err %v", c.in, tr, key, err, c.tracker, c.key, c.err)
		}
	}
}
