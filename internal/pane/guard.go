package pane

import (
	"log"
	"runtime/debug"
	"sync/atomic"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Everything a program prints goes through an emulator conch did not
// write, and a panic in a pane's reader ends the whole server, with every
// pane in it. Two did (2026-11-19, and once before): a program set a
// scroll region below the bottom of its screen — the size it had before a
// resize — and the next scroll indexed a row the screen did not have.
// So the margins are kept on the screen (clampMargins), and a panic that
// still gets through is taken by the pane it happened in (fed), not the
// server.

// repanic makes a recovered panic panic again in tests, so a test meets
// the bug rather than a log line that hides it. A test of the recovery
// itself turns it off.
var repanic = func() *atomic.Bool {
	var b atomic.Bool
	b.Store(testing.Testing())
	return &b
}()

// clampMargins keeps a margin a program sets — DECSTBM's bottom, DECSLRM's
// right — on the screen, as xterm does: the emulator takes the number as
// given, and the next scroll or insertion inside it reaches past the
// screen's last row and panics. It changes the parameter and declines, so
// the emulator's own handler still sets the margins, from the parameter as
// clamped: the handlers are given the parser's own parameters, which is
// also where that handler reads the bottom from (TestMarginsPastTheScreen
// says whether that still holds).
func clampMargins(params ansi.Params, limit int) bool {
	if len(params) > 1 {
		if v := params[1].Param(0); v > limit {
			params[1] = ansi.Param(ansi.Parameter(limit, params[1].HasMore()))
		}
	}
	return false
}

func (p *Pane) registerClamps() {
	p.emu.RegisterCsiHandler('r', func(params ansi.Params) bool { return clampMargins(params, p.emu.Height()) })
	p.emu.RegisterCsiHandler('s', func(params ansi.Params) bool { return clampMargins(params, p.emu.Width()) })
}

// fed hands b to the emulator. A panic inside it is the emulator's, about
// this pane's output alone: it is logged with the stack, the screen's
// margins and modes are set back by resizing it to its own size (or, if
// even that fails, the pane gets a fresh screen), and the pane carries on.
// The bytes that set it off are lost; the server and every other pane
// are not. Called with emuMu held, which the caller still releases.
func (p *Pane) fed(b []byte) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		log.Printf("pane %s: the terminal emulator failed on its output and was reset: %v\n%s", p.id, r, debug.Stack())
		if repanic.Load() {
			panic(r)
		}
		p.resetEmulator()
	}()
	_, _ = p.emu.Write(b)
}

func (p *Pane) resetEmulator() {
	cols, rows := p.emu.Width(), p.emu.Height()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("pane %s: resetting the emulator failed too, starting a blank screen: %v", p.id, r)
			p.newEmulator(cols, rows)
		}
	}()
	p.emu.Resize(cols, rows)
	// CAN: the parser stopped in the middle of the sequence that failed,
	// and would take the next output as the rest of it.
	_, _ = p.emu.Write([]byte{0x18})
}

// survived is deferred around one read's worth of output in readLoop: a
// panic in conch's own handling of it (titles, status reports, the raw
// stream) is logged and that output dropped, rather than the server ended.
func (p *Pane) survived() {
	r := recover()
	if r == nil {
		return
	}
	log.Printf("pane %s: handling its output failed: %v\n%s", p.id, r, debug.Stack())
	if repanic.Load() {
		panic(r)
	}
}
