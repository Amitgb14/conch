package phone

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"rsc.io/qr"
)

func TestPairLink(t *testing.T) {
	for base, want := range map[string]string{
		"https://laptop.tail1234.ts.net":  "https://laptop.tail1234.ts.net/#code=438-219",
		"https://laptop.tail1234.ts.net/": "https://laptop.tail1234.ts.net/#code=438-219",
		"http://100.101.102.103:8722":     "http://100.101.102.103:8722/#code=438-219",
	} {
		if got := PairLink(base, "438-219"); got != want {
			t.Errorf("%s: %s", base, got)
		}
	}
}

// The drawing is the code: reading the half blocks back gives every
// module the encoder made, inside a light margin.
func TestQRLinesAreTheCode(t *testing.T) {
	for _, text := range []string{"https://laptop.tail1234.ts.net/#code=438-219", "x", strings.Repeat("https://a.b/", 20)} {
		lines, err := QRLines(text)
		if err != nil {
			t.Fatal(err)
		}
		code, _ := qr.Encode(text, qr.L)
		side := code.Size + 2*qrQuiet
		if len(lines) != (side+1)/2 {
			t.Fatalf("%d lines for a code %d modules high", len(lines), side)
		}
		for i, l := range lines {
			if !strings.HasPrefix(l, qrStyle) || !strings.HasSuffix(l, qrReset) {
				t.Fatalf("line %d isn't drawn dark on light: %q", i, l)
			}
			if w := ansi.StringWidth(l); w != side {
				t.Fatalf("line %d is %d wide, want %d", i, w, side)
			}
			for x, r := range []rune(ansi.Strip(l)) {
				top := r == '█' || r == '▀'
				bottom := r == '█' || r == '▄'
				mx, my := x-qrQuiet, 2*i-qrQuiet
				want := func(y int) bool { return mx >= 0 && y >= 0 && mx < code.Size && y < code.Size && code.Black(mx, y) }
				if top != want(my) || bottom != want(my+1) {
					t.Fatalf("%q: cell %d,%d is %q", text, x, i, r)
				}
			}
		}
	}
	if _, err := QRLines(strings.Repeat("x", 5000)); err == nil {
		t.Fatal("a code for more than a QR code holds")
	}
}
