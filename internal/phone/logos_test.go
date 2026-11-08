package phone

import (
	"os"
	"regexp"
	"testing"
)

// The phone draws the agents' logos Conch.app draws, path for path, and
// every agent the New screen offers has one: copied once, they would
// drift apart without a word, and an agent without one would fall back to
// its mark on that screen alone.
func TestLogosMatchTheApp(t *testing.T) {
	js, err := uiFiles.ReadFile("ui/logos.mjs")
	if err != nil {
		t.Fatal(err)
	}
	swift, err := os.ReadFile("../../macos/Sources/ConchKit/AgentLogos.swift")
	if err != nil {
		t.Fatal(err)
	}
	app := map[string][2]string{}
	for _, m := range regexp.MustCompile(`"(\w+)": Logo\(path: "([^"]*)", evenOdd: (true|false)\)`).FindAllStringSubmatch(string(swift), -1) {
		app[m[1]] = [2]string{m[2], m[3]}
	}
	phone := map[string][2]string{}
	for _, m := range regexp.MustCompile(`(?m)^  (\w+): \{ d: "([^"]*)", evenOdd: (true|false) \},$`).FindAllStringSubmatch(string(js), -1) {
		phone[m[1]] = [2]string{m[2], m[3]}
	}
	if len(app) < 5 || len(phone) == 0 {
		t.Fatalf("read %d of the app's logos and %d of the phone's", len(app), len(phone))
	}
	for name, l := range phone {
		if app[name] != l {
			t.Errorf("%s: the phone's logo isn't the app's", name)
		}
	}
	src, _ := uiFiles.ReadFile("ui/app.mjs")
	agents := regexp.MustCompile(`const AGENTS = \[([^\]]*)\]`).FindStringSubmatch(string(src))
	if agents == nil {
		t.Fatal("no AGENTS in app.mjs")
	}
	for _, m := range regexp.MustCompile(`"(\w+)"`).FindAllStringSubmatch(agents[1], -1) {
		if _, ok := phone[m[1]]; !ok {
			t.Errorf("the New screen offers %s, which has no logo", m[1])
		}
	}
}
