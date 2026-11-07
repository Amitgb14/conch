package config

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func lineText(l []Span) string { return spansText(l) }

// Colours, bold and resets come through as spans; every other escape and
// control character does not.
func TestParseSGRLines(t *testing.T) {
	if ls := ParseSGRLines(""); len(ls) != 1 || len(ls[0]) != 0 {
		t.Fatalf("empty: %+v", ls)
	}
	ls := ParseSGRLines("\x1b[01;32m➜  \x1b[36mmy-project\x1b[00m $ ")
	want := []Span{{Text: "➜  ", FG: ansi16[2], Bold: true}, {Text: "my-project", FG: ansi16[6], Bold: true}, {Text: " $ "}}
	if len(ls) != 1 || len(ls[0]) != len(want) {
		t.Fatalf("robbyrussell: %+v", ls)
	}
	for i, w := range want {
		if ls[0][i] != w {
			t.Errorf("span %d: %+v, want %+v", i, ls[0][i], w)
		}
	}
	for in, w := range map[string]Span{
		"\x1b[38;5;32mx":       {Text: "x", FG: "#0087d7"},
		"\x1b[38;5;3mx":        {Text: "x", FG: ansi16[3]},
		"\x1b[38;5;240mx":      {Text: "x", FG: "#585858"},
		"\x1b[38;2;1;2;3mx":    {Text: "x", FG: "#010203"},
		"\x1b[38:2:1:2:3mx":    {Text: "x", FG: "#010203"},
		"\x1b[48;5;196mx":      {Text: "x", BG: "#ff0000"},
		"\x1b[44;97mx":         {Text: "x", BG: ansi16[4], FG: ansi16[15]},
		"\x1b[103mx":           {Text: "x", BG: ansi16[11]},
		"\x1b[1;31m\x1b[22mx":  {Text: "x", FG: ansi16[1]},
		"\x1b[31;41m\x1b[39mx": {Text: "x", BG: ansi16[1]},
		"\x1b[31;41m\x1b[49mx": {Text: "x", FG: ansi16[1]},
		"\x1b[31m\x1b[mx":      {Text: "x"},
		"\x1b[38;5;999mx":      {Text: "x"},
		"\x1b[38;2;300;0;0mx":  {Text: "x"},
		"\x1b[38;9mx":          {Text: "x"},
		"\x1b[38mx":            {Text: "x"},
	} {
		ls := ParseSGRLines(in)
		if len(ls) != 1 || len(ls[0]) != 1 || ls[0][0] != w {
			t.Errorf("%q: %+v, want %+v", in, ls, w)
		}
	}
	// Titles, cursor moves, charset shifts' neighbours, CR, BEL: gone.
	ls = ParseSGRLines("\x1b]0;title\x07a\x1b]2;t\x1b\\b\x1b[1Ac\rd\x07e\x1b7f\tg")
	if len(ls) != 1 || lineText(ls[0]) != "abcdef    g" {
		t.Fatalf("other escapes: %q", lineText(ls[0]))
	}
	// Cut short: what came before stays.
	for _, in := range []string{"ab\x1b[31", "ab\x1b]0;x", "ab\x1b"} {
		if ls := ParseSGRLines(in); lineText(ls[0]) != "ab" {
			t.Errorf("%q: %q", in, lineText(ls[0]))
		}
	}
	// Lines, and the same style run together.
	ls = ParseSGRLines("\x1b[31ma\x1b[31mb\n\nc")
	if len(ls) != 3 || len(ls[0]) != 1 || ls[0][0].Text != "ab" || len(ls[1]) != 0 || lineText(ls[2]) != "c" {
		t.Fatalf("lines: %+v", ls)
	}
	// DEC line drawing, as jonathan's bar.
	ls = ParseSGRLines("\x1b(0lqk\x1b(Blqk\x1b)0x")
	if lineText(ls[0]) != "┌─┐lqkx" {
		t.Fatalf("line drawing: %q", lineText(ls[0]))
	}
}

func TestColor256(t *testing.T) {
	for n, want := range map[int]string{-1: "", 256: "", 0: ansi16[0], 15: ansi16[15], 16: "#000000", 21: "#0000ff", 231: "#ffffff", 232: "#080808", 255: "#eeeeee"} {
		if got := color256(n); got != want {
			t.Errorf("%d: %s, want %s", n, got, want)
		}
	}
}

func TestTrimLines(t *testing.T) {
	ls := trimLines(ParseSGRLines("\n  \nA\n\nB\n\n"))
	if len(ls) != 3 || lineText(ls[0]) != "A" || lineText(ls[2]) != "B" {
		t.Fatalf("%+v", ls)
	}
	if ls := trimLines(ParseSGRLines("\n \n")); len(ls) != 0 {
		t.Fatalf("all blank: %+v", ls)
	}
}

// A theme of your own shadows Oh My Zsh's of the same name, as it does
// when zsh loads it; a name that is a path is no theme.
func TestOMZThemeFile(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"themes/a.zsh-theme", "themes/b.zsh-theme", "custom/b.zsh-theme", "themes/c.zsh-theme", "custom/themes/c.zsh-theme", "custom/c.zsh-theme"} {
		p := filepath.Join(dir, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, nil, 0o644)
	}
	for name, want := range map[string]string{"a": "themes/a.zsh-theme", "b": "custom/b.zsh-theme", "c": "custom/themes/c.zsh-theme"} {
		got, err := OMZThemeFile(dir, name)
		if err != nil || got != filepath.Join(dir, want) {
			t.Errorf("%s: %s %v", name, got, err)
		}
	}
	for _, name := range []string{"", "missing", "../themes/a", "custom/b", ".hidden", `a\b`} {
		if got, err := OMZThemeFile(dir, name); err == nil {
			t.Errorf("%q: %s", name, got)
		}
	}
}

// fakeOMZ is an Oh My Zsh of the themes given: name to the theme's text,
// and a lib/git.zsh whose git_prompt_info asks git, as the real one does.
func fakeOMZ(t *testing.T, themes map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("no zsh")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "themes"), 0o755)
	os.MkdirAll(filepath.Join(dir, "lib"), 0o755)
	os.WriteFile(filepath.Join(dir, "oh-my-zsh.sh"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "lib", "git.zsh"),
		[]byte("git_prompt_info() { local b=$(git branch --show-current 2>/dev/null) && [[ -n $b ]] && print -rn -- \"git:($b)\"; [[ -n $(git status --porcelain 2>/dev/null) ]] && print -rn -- ' *' }\n"), 0o644)
	for name, text := range themes {
		os.WriteFile(filepath.Join(dir, "themes", name+".zsh-theme"), []byte(text), 0o644)
	}
	return dir
}

// A theme is drawn by zsh in a home of its own, in a checkout with a
// change: its colours, its git, its right prompt, its precmd hooks.
func TestOMZPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := fakeOMZ(t, map[string]string{
		"plain": `PROMPT='%{$fg_bold[green]%}%c%{$reset_color%} $(git_prompt_info) %# '` + "\n" + `RPROMPT='%{$fg[blue]%}right%{$reset_color%}'`,
		"hook":  "precmd_two() { TWO=$'line one\\n' }\nprecmd_functions+=(precmd_two)\nPROMPT='${TWO}%~ $COLUMNS> '",
		"empty": "PROMPT=''",
		"hang":  "sleep 30\nPROMPT='late'",
		"fails": "exit 3",
	})
	ctx := context.Background()
	p, err := OMZPrompt(ctx, dir, "plain")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Lines) != 1 || lineText(p.Lines[0]) != "my-project git:(main) * % " || p.Lines[0][0].FG != ansi16[2] || !p.Lines[0][0].Bold {
		t.Fatalf("plain: %+v", p)
	}
	if p.Theme != "plain" || lineText(p.Right) != "right" || p.Right[0].FG != ansi16[4] {
		t.Fatalf("plain's right: %+v", p.Right)
	}
	p, err = OMZPrompt(ctx, dir, "hook")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Lines) != 2 || lineText(p.Lines[0]) != "line one" || lineText(p.Lines[1]) != "~/my-project 64> " || p.Right != nil {
		t.Fatalf("hook: %+v", p)
	}
	for _, name := range []string{"empty", "fails", "missing"} {
		if p, err := OMZPrompt(ctx, dir, name); err == nil {
			t.Errorf("%s: %+v", name, p)
		}
	}
	old := promptTimeout
	promptTimeout = 300 * time.Millisecond
	t.Cleanup(func() { promptTimeout = old })
	start := time.Now()
	_, err = OMZPrompt(ctx, dir, "hang")
	if err == nil || !strings.Contains(err.Error(), "took over") || time.Since(start) > 3*time.Second {
		t.Fatalf("hang: %v after %v", err, time.Since(start))
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := OMZPrompt(cctx, dir, "plain"); err == nil {
		t.Fatal("a cancelled ask drew a prompt")
	}
}
