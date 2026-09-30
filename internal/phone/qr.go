package phone

import (
	"strings"

	"rsc.io/qr"
)

// PairLink is where a pairing code is opened: the gateway's page with the
// code in the fragment, which the browser keeps to itself — it is in no
// request, so in no log along the way.
func PairLink(base, code string) string {
	return strings.TrimRight(base, "/") + "/#code=" + code
}

// qrQuiet is the light margin round the code, in modules. The standard
// asks for four; phones read two, and a terminal has few rows to spare.
const qrQuiet = 2

// qrStyle draws dark modules on a light ground whatever the terminal's
// own colours: a code drawn light on dark doesn't scan.
const (
	qrStyle = "\x1b[30;107m"
	qrReset = "\x1b[0m"
)

// QRLines draws text as a QR code for a terminal, two modules to a
// character cell (the upper and lower halves), margin included. Every
// line is the same width: the code's size plus the margin on each side.
func QRLines(text string) ([]string, error) {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return nil, err
	}
	dark := func(x, y int) bool {
		return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
	}
	var lines []string
	for y := -qrQuiet; y < code.Size+qrQuiet; y += 2 {
		var b strings.Builder
		b.WriteString(qrStyle)
		for x := -qrQuiet; x < code.Size+qrQuiet; x++ {
			top, bottom := dark(x, y), dark(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString(qrReset)
		lines = append(lines, b.String())
	}
	return lines, nil
}
