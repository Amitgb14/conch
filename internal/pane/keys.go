package pane

import (
	"fmt"
	"strconv"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
)

var namedKeys = map[string]rune{
	"enter":     uv.KeyEnter,
	"tab":       uv.KeyTab,
	"backspace": uv.KeyBackspace,
	"esc":       uv.KeyEscape,
	"escape":    uv.KeyEscape,
	"space":     uv.KeySpace,
	"up":        uv.KeyUp,
	"down":      uv.KeyDown,
	"left":      uv.KeyLeft,
	"right":     uv.KeyRight,
	"insert":    uv.KeyInsert,
	"delete":    uv.KeyDelete,
	"home":      uv.KeyHome,
	"end":       uv.KeyEnd,
	"pgup":      uv.KeyPgUp,
	"pgdown":    uv.KeyPgDown,
}

// ParseKey parses key notation such as "enter", "ctrl+c", "alt+x",
// "shift+tab" or "ctrl+shift+left".
func ParseKey(s string) (uv.KeyPressEvent, error) {
	var k uv.KeyPressEvent
	base, plus := s, s == "+" || strings.HasSuffix(s, "++") // "+" or "ctrl++": the key is '+'
	if plus {
		base = strings.TrimSuffix(s, "+")
	}
	parts := strings.Split(base, "+")
	name := parts[len(parts)-1]
	switch {
	case plus:
		name = "+"
	case name == "":
		return k, fmt.Errorf("missing key after %q", s)
	}
	for _, mod := range parts[:len(parts)-1] {
		switch mod {
		case "ctrl":
			k.Mod |= uv.ModCtrl
		case "alt":
			k.Mod |= uv.ModAlt
		case "shift":
			k.Mod |= uv.ModShift
		default:
			return k, fmt.Errorf("unknown modifier %q in key %q", mod, s)
		}
	}
	if code, ok := namedKeys[name]; ok {
		k.Code = code
		return k, nil
	}
	if len(name) > 1 && name[0] == 'f' {
		if n, err := strconv.Atoi(name[1:]); err == nil && n >= 1 && n <= 12 {
			k.Code = uv.KeyF1 + rune(n-1)
			return k, nil
		}
	}
	if name == "@" && k.Mod == uv.ModCtrl { // ctrl+@ is NUL, same as ctrl+space
		k.Code = uv.KeySpace
		return k, nil
	}
	if r := []rune(name); len(r) == 1 {
		k.Code = r[0]
		// The emulator matches control keys by Code and Mod only, and types
		// Text for otherwise-unmodified keys (alt is stripped first and sent
		// as an ESC prefix).
		if k.Mod&^uv.ModAlt == 0 {
			k.Text = name
		}
		return k, nil
	}
	return k, fmt.Errorf("unknown key %q", s)
}

// modifiedSequence encodes modified cursor/navigation keys (xterm style,
// e.g. ctrl+left = CSI 1;5D), which the emulator's key encoder does not
// cover. It returns "" when the key is not one of those.
func modifiedSequence(k uv.KeyPressEvent) string {
	mods := k.Mod & (uv.ModShift | uv.ModAlt | uv.ModCtrl)
	if mods == 0 {
		return ""
	}
	if k.Code == uv.KeyTab && mods == uv.ModShift {
		return "" // shift+tab is handled by the emulator
	}
	param := 1
	if mods&uv.ModShift != 0 {
		param += 1
	}
	if mods&uv.ModAlt != 0 {
		param += 2
	}
	if mods&uv.ModCtrl != 0 {
		param += 4
	}
	final := map[rune]string{
		uv.KeyUp: "A", uv.KeyDown: "B", uv.KeyRight: "C", uv.KeyLeft: "D",
		uv.KeyHome: "H", uv.KeyEnd: "F",
	}
	if f, ok := final[k.Code]; ok {
		return fmt.Sprintf("\x1b[1;%d%s", param, f)
	}
	tilde := map[rune]int{
		uv.KeyInsert: 2, uv.KeyDelete: 3, uv.KeyPgUp: 5, uv.KeyPgDown: 6,
	}
	if n, ok := tilde[k.Code]; ok {
		return fmt.Sprintf("\x1b[%d;%d~", n, param)
	}
	return ""
}
