package main

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"log"
	"math"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/go-vgo/robotgo"
	"github.com/tailscale/win"
)

// The message workflow is deliberately split into prepare and confirm phases.
// WhatsApp Desktop does not expose the open chat's contact name through the
// window title (it stays "WhatsApp"), so blindly pressing Enter after a search
// can send to the wrong chat. Mai therefore opens the chat, verifies that a
// composer exists, types the draft, and asks the user to confirm before Enter.
var (
	ErrAppNotFound             = errors.New("WhatsApp could not be opened")
	ErrContactNotFound         = errors.New("contact or chat could not be found")
	ErrMessageNotPending       = errors.New("no message is waiting for confirmation")
	ErrMessageRequiresUserTurn = errors.New("message drafting requires an explicit user turn")
)

type pendingMessage struct {
	App     string
	Contact string
	Text    string
}

// SetEmitter connects the automation workflow to the event bus. Passing nil
// disables overlay phases; keyboard automation continues to work.
func (a *Automation) SetEmitter(emit func(eventType string, payload map[string]interface{})) {
	a.emit = emit
}

func (a *Automation) emitPhase(phase, target, reason string, durationMS int) {
	if a.emit == nil {
		return
	}
	payload := map[string]interface{}{
		"phase":  phase,
		"target": target,
		"app":    "whatsapp",
	}
	if reason != "" {
		payload["reason"] = reason
	}
	if durationMS > 0 {
		payload["duration_ms"] = durationMS
	}
	a.emit("companion.automation", payload)
}

// HasPendingSend reports whether a prepared draft is waiting for confirmation.
func (a *Automation) HasPendingSend() bool {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	return a.pending != nil
}

// PrepareMessage opens WhatsApp, searches for contact, opens the chat, and
// types text into the composer. It does not press Enter.
func (a *Automation) PrepareMessage(app, contact, text string) error {
	if !a.InUserTurn() {
		return ErrMessageRequiresUserTurn
	}

	app = strings.ToLower(strings.TrimSpace(app))
	contact = strings.Trim(strings.TrimSpace(contact), "\"'")
	text = strings.TrimSpace(text)

	if err := validateMessageRequest(app, contact, text); err != nil {
		return err
	}

	// Serialize the whole UI workflow. A second prepare must not type into the
	// composer while this one is still searching for a contact.
	a.opMu.Lock()
	defer a.opMu.Unlock()

	if a.HasPendingSend() {
		return fmt.Errorf("a message is already waiting for confirmation")
	}

	hwnd, err := a.ensureWhatsApp()
	if err != nil {
		return err
	}
	if err := a.focusWhatsApp(hwnd); err != nil {
		a.emitPhase("not_found", "app", err.Error(), 0)
		return err
	}

	// Walk the overlay to the app only after WhatsApp is foreground so the
	// DPI-aware host can use GetForegroundWindow to find its rectangle.
	a.emitPhase("walk", "app", "", 1800)
	time.Sleep(1800 * time.Millisecond)

	// The walk never takes focus, but another app may have stolen it while the
	// overlay was moving. Re-focus before sending any keys.
	if err := a.focusWhatsApp(hwnd); err != nil {
		a.emitPhase("not_found", "app", err.Error(), 0)
		return err
	}

	if err := a.openWhatsAppChat(hwnd, contact, true); err != nil {
		return err
	}

	a.emitPhase("typing", "message", "", 0)
	robotgo.TypeStr(text)
	time.Sleep(250 * time.Millisecond)

	a.pendingMu.Lock()
	a.pending = &pendingMessage{App: app, Contact: contact, Text: text}
	a.pendingMu.Unlock()
	a.emitPhase("await_confirm", "message", "", 0)
	log.Printf("[AUTO] Prepared WhatsApp draft for %q (length=%d); waiting for confirmation", contact, len([]rune(text)))
	return nil
}

// ConfirmSend re-opens the requested chat, re-checks the app and composer, then
// presses Enter. Re-opening matters because a visible composer only proves that
// some chat is open, not which one; the user may have switched chats while the
// confirmation was pending.
func (a *Automation) ConfirmSend() error {
	a.opMu.Lock()
	defer a.opMu.Unlock()

	a.pendingMu.Lock()
	pending := a.pending
	a.pendingMu.Unlock()
	if pending == nil {
		return ErrMessageNotPending
	}
	if !a.InUserTurn() {
		return ErrMessageRequiresUserTurn
	}

	hwnd := findWhatsAppWindow()
	if hwnd == 0 {
		a.emitPhase("not_found", "app", "WhatsApp is not open", 0)
		return ErrAppNotFound
	}
	if err := a.focusWhatsApp(hwnd); err != nil {
		a.emitPhase("not_found", "app", err.Error(), 0)
		return err
	}

	// Re-open the exact chat immediately before pressing Enter. Passing
	// clearComposer=false keeps the draft already typed there. If the contact
	// is gone, openWhatsAppChat returns before any Enter is sent.
	if err := a.openWhatsAppChat(hwnd, pending.Contact, false); err != nil {
		return err
	}

	// Put the caret back in the composer. If focus moved while the search was
	// running, the click restores it before the send key.
	a.clickWhatsAppComposer(hwnd)
	time.Sleep(150 * time.Millisecond)
	if win.GetForegroundWindow() != hwnd {
		a.emitPhase("not_found", "app", "WhatsApp lost focus before sending", 0)
		return fmt.Errorf("WhatsApp lost focus before sending")
	}

	a.emitPhase("send", "message", "", 0)
	robotgo.KeyTap("enter")
	time.Sleep(300 * time.Millisecond)
	a.emitPhase("done", "message", "", 0)

	a.pendingMu.Lock()
	a.pending = nil
	a.pendingMu.Unlock()
	log.Printf("[AUTO] WhatsApp message sent to %q", pending.Contact)
	return nil
}

// CancelSend discards the pending state. The typed draft is intentionally left
// in the composer so the user can edit or send it manually.
func (a *Automation) CancelSend() {
	a.opMu.Lock()
	defer a.opMu.Unlock()

	a.pendingMu.Lock()
	a.pending = nil
	a.pendingMu.Unlock()
	a.emitPhase("cancelled", "message", "", 0)
	log.Printf("[AUTO] Cancelled prepared WhatsApp message")
}

func validateMessageRequest(app, contact, text string) error {
	if app == "" {
		return fmt.Errorf("which app should I use?")
	}
	if app != "whatsapp" {
		return fmt.Errorf("I can choreograph WhatsApp messages right now")
	}
	if contact == "" {
		return fmt.Errorf("who should I message on WhatsApp?")
	}
	if isPlaceholderMessage(text) {
		return fmt.Errorf("what should I say to %s?", contact)
	}
	if strings.ContainsAny(text, "\r\n") {
		return fmt.Errorf("the message must be a single line")
	}
	return nil
}

// ensureWhatsApp opens WhatsApp Desktop if needed. It first performs the visible
// taskbar-search choreography, then falls back to the Start Menu AppID launch.
func (a *Automation) ensureWhatsApp() (win.HWND, error) {
	if hwnd := findWhatsAppWindow(); hwnd != 0 {
		return hwnd, nil
	}

	a.emitPhase("walk", "taskbar", "", 1600)
	time.Sleep(1600 * time.Millisecond)
	a.emitPhase("click", "taskbar", "", 0)

	// Windows Search: Win+S, type the app name, Enter. This is keyboard-driven
	// so it works regardless of taskbar alignment or DPI.
	robotgo.KeyTap("s", "cmd")
	time.Sleep(900 * time.Millisecond)
	robotgo.TypeStr("WhatsApp")
	time.Sleep(1200 * time.Millisecond)
	robotgo.KeyTap("enter")
	time.Sleep(1000 * time.Millisecond)

	if hwnd := a.waitWhatsAppWindow(8 * time.Second); hwnd != 0 {
		return hwnd, nil
	}

	// Search may be disabled or the result may not be ready. Launch the Store
	// app directly through its AppID; never fall back to WhatsApp Web.
	appID := a.findAppInStartMenu("WhatsApp")
	if appID != "" {
		cmd := exec.Command(`C:\Windows\explorer.exe`, `shell:AppsFolder\`+appID)
		if err := cmd.Start(); err == nil {
			if hwnd := a.waitWhatsAppWindow(8 * time.Second); hwnd != 0 {
				return hwnd, nil
			}
		}
	}

	a.emitPhase("not_found", "app", "WhatsApp could not be opened", 0)
	return 0, ErrAppNotFound
}

func (a *Automation) waitWhatsAppWindow(timeout time.Duration) win.HWND {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if hwnd := findWhatsAppWindow(); hwnd != 0 {
			return hwnd
		}
		time.Sleep(250 * time.Millisecond)
	}
	return 0
}

func windowTitle(hwnd win.HWND) string {
	n := win.GetWindowTextLength(hwnd)
	if n <= 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	win.GetWindowText(hwnd, &buf[0], int32(len(buf)))
	return syscall.UTF16ToString(buf)
}

func windowClass(hwnd win.HWND) string {
	buf := make([]uint16, 256)
	win.GetClassName(hwnd, &buf[0], len(buf))
	return syscall.UTF16ToString(buf)
}

// findWhatsAppWindow returns only a visible top-level WhatsApp Desktop window.
// WhatsApp keeps a hidden WinUI window alive when it is "closed" to the tray;
// FindWindow returns that handle even though there is nothing on screen, so it
// cannot be used as proof that the app is open.
func findWhatsAppWindow() win.HWND {
	desktop := win.GetDesktopWindow()
	for hwnd := win.GetWindow(desktop, win.GW_CHILD); hwnd != 0; hwnd = win.GetWindow(hwnd, win.GW_HWNDNEXT) {
		if !win.IsWindowVisible(hwnd) {
			continue
		}
		title := windowTitle(hwnd)
		lower := strings.ToLower(title)
		if !strings.Contains(lower, "whatsapp") || strings.Contains(lower, "whatsapp web") {
			continue
		}
		class := windowClass(hwnd)
		if strings.EqualFold(class, "WinUIDesktopWin32WindowClass") || strings.EqualFold(strings.TrimSpace(title), "WhatsApp") {
			return hwnd
		}
	}
	return 0
}

// activateWindow brings hwnd to the foreground. A background process is not
// normally allowed to call SetForegroundWindow, so attach to the current
// foreground thread's input queue for the duration of the switch. Without
// this, Windows silently leaves focus on the terminal/editor and Mai's
// keystrokes land in the wrong app.
func activateWindow(hwnd win.HWND) bool {
	if hwnd == 0 || !win.IsWindowVisible(hwnd) {
		return false
	}
	if win.GetForegroundWindow() == hwnd {
		return true
	}
	if win.IsIconic(hwnd) {
		win.ShowWindow(hwnd, win.SW_RESTORE)
		time.Sleep(150 * time.Millisecond)
	}

	foreground := win.GetForegroundWindow()
	foregroundThread := win.GetWindowThreadProcessId(foreground, nil)
	currentThread := win.GetCurrentThreadId()
	attached := false
	if foregroundThread != 0 && foregroundThread != currentThread {
		attached = win.AttachThreadInput(int32(currentThread), int32(foregroundThread), true)
	}
	win.BringWindowToTop(hwnd)
	win.SetForegroundWindow(hwnd)
	win.SetActiveWindow(hwnd)
	win.SetFocus(hwnd)
	if attached {
		win.AttachThreadInput(int32(currentThread), int32(foregroundThread), false)
	}
	time.Sleep(250 * time.Millisecond)
	return win.GetForegroundWindow() == hwnd
}

func (a *Automation) focusWhatsApp(hwnd win.HWND) error {
	if hwnd == 0 || !win.IsWindowVisible(hwnd) {
		return fmt.Errorf("WhatsApp Desktop is not showing")
	}
	for attempt := 0; attempt < 4; attempt++ {
		if activateWindow(hwnd) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	title := strings.ToLower(windowTitle(win.GetForegroundWindow()))
	if strings.Contains(title, "whatsapp web") {
		return fmt.Errorf("WhatsApp Web is not supported; open WhatsApp Desktop")
	}
	return fmt.Errorf("could not focus WhatsApp Desktop (foreground is %q)", title)
}

func (a *Automation) openWhatsAppChat(hwnd win.HWND, contact string, clearComposer bool) error {
	a.emitPhase("click", "search", "", 0)
	robotgo.KeyTap("f", "ctrl")
	time.Sleep(350 * time.Millisecond)

	// Clear a previous search. Ctrl+F focuses WhatsApp's search box, so this
	// cannot clear the message composer.
	robotgo.KeyTap("a", "ctrl")
	robotgo.KeyTap("backspace")
	time.Sleep(200 * time.Millisecond)

	a.emitPhase("typing", "contact", "", 0)
	robotgo.TypeStr(contact)
	time.Sleep(1500 * time.Millisecond)

	robotgo.KeyTap("enter")

	// Wait for the right panel's composer to appear. The no-results state has
	// no composer, which is how we distinguish it without OCR.
	var open bool
	var lastErr error
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		open, lastErr = a.whatsAppChatOpen(hwnd)
		if lastErr == nil && open {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if lastErr != nil {
		a.emitPhase("not_found", "contact", "I could not verify the chat", 0)
		robotgo.KeyTap("escape")
		return lastErr
	}
	if !open {
		a.emitPhase("not_found", "contact", fmt.Sprintf("I could not find %s", contact), 0)
		robotgo.KeyTap("escape")
		return ErrContactNotFound
	}

	a.emitPhase("click", "composer", "", 0)
	a.clickWhatsAppComposer(hwnd)
	time.Sleep(250 * time.Millisecond)

	// Replace any existing draft so the confirmation contains exactly the
	// requested message rather than an accidental concatenation. The confirm
	// path passes false: it must preserve the draft it is about to send.
	if clearComposer {
		robotgo.KeyTap("a", "ctrl")
		robotgo.KeyTap("backspace")
		time.Sleep(150 * time.Millisecond)
	}
	return nil
}

func (a *Automation) clickWhatsAppComposer(hwnd win.HWND) {
	cx, cy, cw, ch := robotgo.GetClient(int(hwnd), 1)
	if cw <= 0 || ch <= 0 {
		return
	}
	x := cx + int(float64(cw)*0.72)
	y := cy + int(float64(ch)*0.94)
	robotgo.MoveClick(x, y)
}

// whatsAppChatOpen reports whether the right-hand chat panel has a composer.
// The overlay is parked at the app's left edge, so it cannot cover the sampled
// right-hand region.
func (a *Automation) whatsAppChatOpen(hwnd win.HWND) (bool, error) {
	img, err := captureWhatsAppClient(hwnd)
	if err != nil {
		return false, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 40 || h < 40 {
		return false, fmt.Errorf("WhatsApp window is too small to inspect")
	}

	composer := image.Rect(
		b.Min.X+int(float64(w)*0.55),
		b.Min.Y+int(float64(h)*0.88),
		b.Min.X+int(float64(w)*0.95),
		b.Min.Y+int(float64(h)*0.96),
	)
	reference := image.Rect(
		b.Min.X+int(float64(w)*0.55),
		b.Min.Y+int(float64(h)*0.65),
		b.Min.X+int(float64(w)*0.95),
		b.Min.Y+int(float64(h)*0.80),
	)
	composerLuma := averageLuma(img, composer)
	referenceLuma := averageLuma(img, reference)
	log.Printf("[AUTO] WhatsApp composer luma %.1f vs reference %.1f", composerLuma, referenceLuma)
	return composerLuma > referenceLuma+4.0, nil
}

func captureWhatsAppClient(hwnd win.HWND) (image.Image, error) {
	img, err := robotgo.CaptureImg()
	if err != nil {
		return nil, fmt.Errorf("screen capture failed: %w", err)
	}
	screenW, screenH := robotgo.GetScreenSize()
	if screenW <= 0 || screenH <= 0 {
		return nil, fmt.Errorf("could not read the screen size")
	}
	cx, cy, cw, ch := robotgo.GetClient(int(hwnd), 1)
	if cw <= 0 || ch <= 0 {
		return nil, fmt.Errorf("could not read the WhatsApp client rectangle")
	}

	bounds := img.Bounds()
	scaleX := float64(bounds.Dx()) / float64(screenW)
	scaleY := float64(bounds.Dy()) / float64(screenH)
	crop := image.Rect(
		int(math.Round(float64(cx)*scaleX)),
		int(math.Round(float64(cy)*scaleY)),
		int(math.Round(float64(cx+cw)*scaleX)),
		int(math.Round(float64(cy+ch)*scaleY)),
	).Intersect(bounds)
	if crop.Empty() {
		return nil, fmt.Errorf("WhatsApp is outside the captured screen")
	}

	sub := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
	draw.Draw(sub, sub.Bounds(), img, crop.Min, draw.Src)
	return sub, nil
}

func averageLuma(img image.Image, r image.Rectangle) float64 {
	r = r.Intersect(img.Bounds())
	if r.Empty() {
		return 0
	}
	var sum float64
	var count int
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			sum += 0.299*float64(cr>>8) + 0.587*float64(cg>>8) + 0.114*float64(cb>>8)
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}
