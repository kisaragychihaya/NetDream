package main

import (
	"sync"

	"github.com/egoist/mygo"
)

// closeRequested asks the page whether it is fine to close the window: it goes
// out when the user closes a window whose workspace holds unsaved changes, and
// the page answers with WorkspaceService.SetDirty (now clean) or
// WorkspaceService.CloseWindow (discard the changes).
var closeRequested = mygo.NewEvent[bool]("workspace:closeRequested")

// windowGuard is what the main window's OnClose listener decides on: the window
// itself (CloseWindow closes it), whether the page holds unsaved changes, and
// whether a close the page asked for must pass. One mutex guards all three:
// OnClose listeners run on the main thread, the rest is set from the goroutines
// bound methods run on.
type windowGuard struct {
	mu    sync.Mutex
	win   *mygo.Window
	dirty bool
	force bool
}

// guard is the main window's guard; guardWindow fills it in.
var guard windowGuard

// guardWindow records win, which CloseWindow closes, and makes its close wait
// for the page while the workspace holds unsaved changes.
func guardWindow(win *mygo.Window) {
	guard.mu.Lock()
	guard.win = win
	guard.mu.Unlock()

	win.OnClose(func(e *mygo.CloseEvent) {
		if guard.letClose() {
			return // nothing unsaved, or the page already decided: it closes
		}
		e.PreventDefault()
		// The page shows save/discard/cancel and calls back with SetDirty (it
		// saved) or CloseWindow (it discards); the workspace stays dirty until
		// then, so a second close asks again.
		_ = closeRequested.Emit(win, true)
	})
}

// letClose reports whether a close may go ahead. A forced one passes once and
// only once: the force is dropped here rather than in CloseWindow, because
// win.Close asks the main thread to run the close and that may never come (the
// window already gone, the loop stopped), which would leave every later close
// unguarded.
func (g *windowGuard) letClose() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.force {
		g.force = false
		return true
	}
	return !g.dirty
}

// setDirty records whether the page holds unsaved changes.
func (g *windowGuard) setDirty(dirty bool) {
	g.mu.Lock()
	g.dirty = dirty
	g.mu.Unlock()
}

// close closes the window even though it may hold unsaved changes, as the page
// asks once its user chose to discard them.
func (g *windowGuard) close() {
	g.mu.Lock()
	win := g.win
	if win != nil {
		g.force = true
	}
	g.mu.Unlock()
	if win != nil {
		win.Close()
	}
}
