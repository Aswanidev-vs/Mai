// Command transparent is the Mai desktop overlay: a small, always-on-top,
// transparent window that shows the VRM avatar floating over other desktop
// applications.
//
// The window is a Win32 layered window. The WebView2 inside it is given a
// fully transparent background (ICoreWebView2Controller2::put_DefaultBackgroundColor)
// and the page paints nothing behind the avatar, so the desktop shows through
// everywhere the avatar does not cover.
//
// The page is served by the companion server rather than loaded from disk, for
// two reasons: the websocket's origin check only accepts localhost, and the
// page's import map needs /node_modules on the same origin.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	neturl "net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2/pkg/edge"
)

func main() {
	var (
		url          = flag.String("url", "http://localhost:8080/virtual/", "page to load in the overlay")
		title        = flag.String("title", "Mai", "overlay window title")
		width        = flag.Int("width", 320, "overlay width in logical pixels")
		height       = flag.Int("height", 480, "overlay height in logical pixels")
		clickThrough = flag.Bool("click-through", false, "let mouse clicks pass through the overlay to the app below")
		perch        = flag.Bool("perch", false, "perch on the focused application window")
		debug        = flag.Bool("debug", false, "enable the webview developer tools")
	)
	flag.Parse()

	if runtime.GOOS != "windows" {
		log.Fatalf("the desktop overlay is Windows-only (this is %s)", runtime.GOOS)
	}

	host := &overlayHost{quit: make(chan struct{}), clickThrough: *clickThrough}
	if err := host.start(*url, *title, *width, *height, *debug, *perch); err != nil {
		log.Fatalf("could not start the overlay: %v", err)
	}
	host.run()
	host.shutdown()
}

// ─────────────────────────────────────────────────────────────────────────────
// Win32 plumbing
//
// golang.org/x/sys/windows does not export the user32 window APIs this needs
// (SetWindowLong, SetWindowPos, GetWindowRect, GetForegroundWindow), so they
// are called through the standard library's lazy DLL binding.
// ─────────────────────────────────────────────────────────────────────────────

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCreateWindowEx   = user32.NewProc("CreateWindowExW")
	procRegisterClassEx  = user32.NewProc("RegisterClassExW")
	procDefWindowProc    = user32.NewProc("DefWindowProcW")
	procShowWindow       = user32.NewProc("ShowWindow")
	procUpdateWindow     = user32.NewProc("UpdateWindow")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procGetMessage       = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessage  = user32.NewProc("DispatchMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procPostMessage      = user32.NewProc("PostMessageW")
	procLoadCursor       = user32.NewProc("LoadCursorW")

	procSetWindowPos    = user32.NewProc("SetWindowPos")
	procGetWindowRect   = user32.NewProc("GetWindowRect")
	procGetForeground   = user32.NewProc("GetForegroundWindow")
	procSetForeground   = user32.NewProc("SetForegroundWindow")
	procFindWindow      = user32.NewProc("FindWindowW")
	procGetSystemMetric = user32.NewProc("GetSystemMetrics")
	procSetWindowLong   = user32.NewProc("SetWindowLongW")
	procGetWindowLong   = user32.NewProc("GetWindowLongW")
	procGetDpiForSystem = user32.NewProc("GetDpiForSystem")
	procGetDpiForWindow = user32.NewProc("GetDpiForWindow")
	procSetDpiAwareCtx  = user32.NewProc("SetProcessDpiAwarenessContext")
	procGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
	procSetLayeredAttrs = user32.NewProc("SetLayeredWindowAttributes")
	procSetCapture      = user32.NewProc("SetCapture")
	procReleaseCapture  = user32.NewProc("ReleaseCapture")
	procGetCursorPos    = user32.NewProc("GetCursorPos")
)

const (
	wsPopup   = 0x80000000
	wsVisible = 0x10000000

	wsExTopmost     = 0x00000008
	wsExToolWindow  = 0x00000080
	wsExTransparent = 0x00000020
	wsExLayered     = 0x00080000
	wsExNoActivate  = 0x08000000

	gwlStyle   = ^uintptr(15)
	gwlExStyle = ^uintptr(19)

	hwndTopmost    = ^uintptr(0) // (HWND)-1
	swpNoMove      = 0x0002
	swpNoSize      = 0x0001
	swpNoActivate  = 0x0010
	swpFrameChange = 0x0020

	smCXScreen = 0
	smCYScreen = 1

	wmDestroy        = 0x0002
	wmMove           = 0x0003
	wmSize           = 0x0005
	wmClose          = 0x0010
	wmEraseBkgnd     = 0x0014
	wmMouseMove      = 0x0200
	wmLButtonUp      = 0x0202
	wmCaptureChanged = 0x0215
	wmMoving         = 0x0216
	wmDpiChanged     = 0x02E0
	wmAppDispatch    = 0x8001

	csOwnDC   = 0x0020
	csDblClks = 0x0008

	idcArrow = 32512

	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
	dpiAwarePerMonitorV2 = ^uintptr(3) // -4

	lwaAlpha = 0x00000002
)

type rect struct{ Left, Top, Right, Bottom int32 }

type point struct{ X, Y int32 }

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// overlayHost owns the overlay window and the page bridge.
type overlayHost struct {
	chromium *edge.Chromium
	hwnd     uintptr

	clickThrough bool
	dragging     bool
	dragLast     point
	quit         chan struct{}
	once         sync.Once

	// Scripted walk state. All fields are touched on the UI thread except the
	// stop channel, which the walk goroutine closes via cancelWalk.
	walkStop   chan struct{}
	walkActive bool

	mu      sync.Mutex
	pending []func()
	scale   float64

	logicalWidth  int
	logicalHeight int
}

// current is the host of the one overlay window. The window procedure is a
// plain callback, so it needs a way back to the host.
var current *overlayHost

// ─────────────────────────────────────────────────────────────────────────────
// Starting up
// ─────────────────────────────────────────────────────────────────────────────

// start creates the window, embeds the WebView2 in it and navigates to the
// page.
func (h *overlayHost) start(url, title string, width, height int, debug, perch bool) error {
	// Physical pixels. Without this the desktop bitmap-stretches the window
	// and the avatar comes out soft on a scaled display.
	procSetDpiAwareCtx.Call(uintptr(dpiAwarePerMonitorV2))

	dpi, _, _ := procGetDpiForSystem.Call()
	if dpi == 0 {
		dpi = 96
	}
	h.scale = float64(dpi) / 96

	// The window is a popup: no caption, no border, no taskbar button, it
	// never takes focus, and it is layered so that the transparent WebView2
	// inside it composites against the desktop.
	exStyle := uintptr(wsExTopmost | wsExToolWindow | wsExNoActivate | wsExLayered)
	if h.clickThrough {
		exStyle |= wsExTransparent
	}

	style := uintptr(wsPopup | wsVisible)
	h.logicalWidth = width
	h.logicalHeight = height
	physicalWidth := int(float64(width) * h.scale)
	physicalHeight := int(float64(height) * h.scale)

	className, err := utf16("MaiOverlayWindow")
	if err != nil {
		return err
	}
	instance, _, _ := procGetModuleHandle.Call(0)
	cursor, _, _ := procLoadCursor.Call(0, idcArrow)

	wc := wndClassEx{
		Size:     uint32(unsafe.Sizeof(wndClassEx{})),
		Style:    csOwnDC | csDblClks,
		WndProc:  syscall.NewCallback(h.wndproc),
		Instance: instance,
		Cursor:   cursor,
		// No background brush: the window is never painted, so the WebView2's
		// own (transparent) pixels are all there is.
		Background: 0,
		ClassName:  className,
	}
	if r, _, _ := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("could not register the overlay window class")
	}

	titlePtr, err := utf16(title)
	if err != nil {
		return err
	}
	hwnd, _, _ := procCreateWindowEx.Call(
		exStyle,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(titlePtr)),
		style,
		0, 0,
		uintptr(physicalWidth), uintptr(physicalHeight),
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("could not create the overlay window")
	}
	h.hwnd = hwnd
	// A layered window stays hidden until its alpha or colour key is set.
	// The WebView2 supplies the transparent pixels; this only makes the host
	// window visible to the compositor.
	procSetLayeredAttrs.Call(hwnd, 0, 255, lwaAlpha)
	current = h

	// The window is shown right away: the WebView2 refuses to create its
	// controller for a hidden window, and an empty layered window with no
	// background is invisible anyway.
	procShowWindow.Call(hwnd, 5) // SW_SHOW
	procUpdateWindow.Call(hwnd)

	// The overlay must not take focus away from the app the user is working in.
	h.restoreFocus()

	chromium := edge.NewChromium()
	chromium.DataPath = filepath.Join(os.Getenv("AppData"), "Mai", "overlay")
	chromium.MessageCallback = h.onMessage
	if !chromium.Embed(hwnd) {
		return fmt.Errorf("could not embed the WebView2")
	}
	h.chromium = chromium

	settings, err := chromium.GetSettings()
	if err == nil {
		_ = settings.PutAreDefaultContextMenusEnabled(debug)
		_ = settings.PutAreDevToolsEnabled(debug)
	}

	// The WebView2 paints an opaque background by default, and a colour key
	// does not help: the WebView2 renders into a DirectComposition surface
	// that the desktop window manager composites separately, so the host
	// window's layered colour key never reaches it. Making the WebView2's own
	// background transparent is the supported way, and the layered window then
	// composites the result against the desktop.
	if err := setTransparentBackground(chromium.GetController()); err != nil {
		return fmt.Errorf("could not make the WebView2 background transparent: %w", err)
	}
	chromium.Resize()

	// The page talks to the host over window.chrome.webview, and the host
	// talks back by evaluating JavaScript.
	chromium.Init(hostBridgeScript)
	chromium.Init(pageLogBridgeScript)

	h.positionInitial(physicalWidth, physicalHeight)

	chromium.Navigate(url)

	// The page is served by the companion server, so that server has to be
	// running. Without this check the WebView2 shows its own "can't reach this
	// page" error, which says nothing about what is actually wrong. The check
	// runs alongside the navigation rather than before it, so that the
	// navigation is not delayed.
	go func() {
		// Only a page served over http needs the companion server.
		if u, err := neturl.Parse(url); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return
		}
		if err := checkServer(url); err != nil {
			log.Printf("[OVERLAY] %v", err)
			log.Println("[OVERLAY] The companion server is not running. Start it with: make companion-virtual")
			h.dispatch(func() { chromium.NavigateToString(serverDownPage) })
		}
	}()

	// The WebView2 rebuilds its presentation surface whenever it likes, and a
	// surface left at the wrong size leaves the avatar outside the overlay.
	go h.keepSized()

	if perch {
		go h.perchLoop()
	}
	return nil
}

// run pumps the window's message queue until the window is closed.
func (h *overlayHost) run() {
	var m winMsg
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// The window procedure
// ─────────────────────────────────────────────────────────────────────────────

func (h *overlayHost) wndproc(hwnd, m, wp, lp uintptr) uintptr {
	switch m {
	case wmEraseBkgnd:
		// Never paint the window background: the WebView2 supplies its own
		// pixels, and anything painted here would fill the window in.
		return 1
	case wmAppDispatch:
		h.runPending()
		return 0
	case wmSize:
		h.resizeWebView()
		return 0
	case wmMouseMove:
		if h.dragging {
			h.dragMove()
			return 0
		}
	case wmLButtonUp:
		if h.dragging {
			h.endDrag()
			return 0
		}
	case wmCaptureChanged:
		if h.dragging {
			h.dragging = false
			h.emitToPage(map[string]any{"event": "dragEnd"})
		}
		return 0
	case wmMove, wmMoving:
		if h.chromium != nil {
			_ = h.chromium.NotifyParentWindowPositionChanged()
		}
		return 0
	case wmDpiChanged:
		// The overlay keeps a fixed size in logical pixels; only the physical
		// size changes when the window moves to a display with another scale.
		h.applyScale()
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProc.Call(hwnd, m, wp, lp)
	return r
}

// resizeWebView sizes the WebView2 to the window's client area.
func (h *overlayHost) resizeWebView() {
	if h.chromium != nil {
		h.chromium.Resize()
	}
}

// applyScale resizes the window to its logical size at the display's current
// scale, and resizes the WebView2 with it.
func (h *overlayHost) applyScale() {
	dpi, _, _ := procGetDpiForWindow.Call(h.hwnd)
	if dpi == 0 {
		dpi = 96
	}
	h.scale = float64(dpi) / 96
	procSetWindowPos.Call(h.hwnd, hwndTopmost, 0, 0,
		uintptr(int(float64(h.logicalWidth)*h.scale)),
		uintptr(int(float64(h.logicalHeight)*h.scale)),
		swpNoMove|swpNoActivate)
	h.resizeWebView()
}

// dispatch runs a function on the thread that owns the window. The WebView2's
// COM objects and the window's styles may only be touched from there.
func (h *overlayHost) dispatch(f func()) {
	h.mu.Lock()
	h.pending = append(h.pending, f)
	h.mu.Unlock()
	procPostMessage.Call(h.hwnd, wmAppDispatch, 0, 0)
}

func (h *overlayHost) runPending() {
	h.mu.Lock()
	queue := h.pending
	h.pending = nil
	h.mu.Unlock()
	for _, f := range queue {
		f()
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Window state
// ─────────────────────────────────────────────────────────────────────────────

// restoreFocus gives the focus back to whatever had it before the overlay was
// created.
func (h *overlayHost) restoreFocus() {
	previous, _, _ := procGetForeground.Call()
	if previous != 0 && previous != h.hwnd {
		procSetForeground.Call(previous)
	}
}

// setClickThrough toggles mouse pass-through. With WS_EX_TRANSPARENT the whole
// window ignores the mouse, which is what "click the app underneath" needs.
func (h *overlayHost) setClickThrough(on bool) {
	h.clickThrough = on
	exStyle, _, _ := procGetWindowLong.Call(h.hwnd, gwlExStyle)
	if on {
		exStyle |= wsExTransparent
	} else {
		exStyle &^= wsExTransparent
	}
	procSetWindowLong.Call(h.hwnd, gwlExStyle, exStyle)
	// The extended style has to be pushed to the window manager with a frame
	// change for the hit-test behaviour to take effect.
	procSetWindowPos.Call(h.hwnd, hwndTopmost, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoActivate|swpFrameChange)
	h.emitToPage(map[string]any{"event": "clickThrough", "on": on})
}

// positionInitial puts the window near the bottom-right of the screen, the way
// a desktop pet usually starts.
func (h *overlayHost) positionInitial(width, height int) {
	w, _, _ := procGetSystemMetric.Call(smCXScreen)
	sw, _, _ := procGetSystemMetric.Call(smCYScreen)
	margin := int(48 * h.scale)
	x := int(w) - width - margin
	y := int(sw) - height - margin
	h.moveTo(x, y)
}

// moveTo moves the window to a screen position.
func (h *overlayHost) moveTo(x, y int) {
	procSetWindowPos.Call(h.hwnd, hwndTopmost, uintptr(x), uintptr(y), 0, 0,
		swpNoSize|swpNoActivate)
}

// moveBy moves the window by a pixel delta.
func (h *overlayHost) moveBy(dx, dy int) {
	h.cancelWalk("drag")
	var r rect
	procGetWindowRect.Call(h.hwnd, uintptr(unsafe.Pointer(&r)))
	procSetWindowPos.Call(h.hwnd, hwndTopmost,
		uintptr(int(r.Left)+dx), uintptr(int(r.Top)+dy), 0, 0,
		swpNoSize|swpNoActivate)
	h.emitToPage(map[string]any{"event": "moved"})
}

// walkTo animates the overlay window from its current position to x,y. The
// host owns the motion so the page only has to select the matching directional
// walk clip when it receives walkStart.
func (h *overlayHost) walkTo(x, y, durationMs int) {
	h.cancelWalk("replaced")

	var r rect
	procGetWindowRect.Call(h.hwnd, uintptr(unsafe.Pointer(&r)))
	sx, sy := int(r.Left), int(r.Top)
	dx, dy := x-sx, y-sy
	dist := math.Hypot(float64(dx), float64(dy))
	if dist < 2 {
		h.emitToPage(map[string]any{"event": "walkEnd", "reason": "done"})
		return
	}
	if durationMs <= 0 {
		speed := 220 * h.scale
		if speed < 1 {
			speed = 220
		}
		durationMs = int(dist / speed * 1000)
	}
	if durationMs < 250 {
		durationMs = 250
	}
	if durationMs > 15000 {
		durationMs = 15000
	}

	stop := make(chan struct{})
	h.walkStop = stop
	h.walkActive = true
	h.emitToPage(map[string]any{
		"event":      "walkStart",
		"dx":         dx,
		"dy":         dy,
		"durationMs": durationMs,
		"targetX":    x,
		"targetY":    y,
	})

	go func() {
		ticker := time.NewTicker(33 * time.Millisecond)
		defer ticker.Stop()
		start := time.Now()
		for {
			select {
			case <-stop:
				return
			case <-h.quit:
				return
			case <-ticker.C:
				t := float64(time.Since(start)) / float64(time.Duration(durationMs)*time.Millisecond)
				if t > 1 {
					t = 1
				}
				eased := t * t * (3 - 2*t)
				nx := sx + int(float64(dx)*eased)
				ny := sy + int(float64(dy)*eased)
				done := t >= 1
				h.dispatch(func() {
					if h.walkStop != stop {
						return
					}
					h.moveTo(nx, ny)
					if done {
						h.walkStop = nil
						h.walkActive = false
						h.emitToPage(map[string]any{"event": "walkEnd", "reason": "done"})
					}
				})
				if done {
					return
				}
			}
		}
	}()
}

func (h *overlayHost) cancelWalk(reason string) {
	if h.walkStop != nil {
		close(h.walkStop)
		h.walkStop = nil
	}
	if h.walkActive {
		h.walkActive = false
		h.emitToPage(map[string]any{"event": "walkEnd", "reason": reason})
	}
}

// walkToForeground parks the overlay at the left edge of the focused app. The
// left edge keeps the avatar clear of WhatsApp's composer, which the screenshot
// verification samples on the right-hand side.
func (h *overlayHost) walkToForeground(durationMs int) {
	fg, _, _ := procGetForeground.Call()
	var target rect
	ok := false
	if fg != 0 && fg != h.hwnd {
		procGetWindowRect.Call(fg, uintptr(unsafe.Pointer(&target)))
		ok = target.Right > target.Left && target.Bottom > target.Top
	}

	var r rect
	procGetWindowRect.Call(h.hwnd, uintptr(unsafe.Pointer(&r)))
	winW, winH := int(r.Right-r.Left), int(r.Bottom-r.Top)
	margin := int(18 * h.scale)
	x, y := 0, 0
	if ok {
		x = int(target.Left) + margin
		y = int(target.Bottom) - winH - margin
	} else {
		sw, _, _ := procGetSystemMetric.Call(smCXScreen)
		sh, _, _ := procGetSystemMetric.Call(smCYScreen)
		x = int(sw)/2 - winW/2
		y = int(sh) - winH - int(64*h.scale)
	}
	h.walkToClamped(x, y, durationMs)
}

// walkToTaskbar walks to the search area of the primary taskbar. The actual
// search is opened with Win+S; the walk is the visual choreography.
func (h *overlayHost) walkToTaskbar(durationMs int) {
	var r rect
	procGetWindowRect.Call(h.hwnd, uintptr(unsafe.Pointer(&r)))
	winW, winH := int(r.Right-r.Left), int(r.Bottom-r.Top)

	taskbar, ok := taskbarRect()
	x, y := 0, 0
	if ok {
		taskbarW := int(taskbar.Right - taskbar.Left)
		x = int(taskbar.Left) + int(float64(taskbarW)*0.38) - winW/2
		y = int(taskbar.Top) - winH + int(10*h.scale)
	} else {
		sw, _, _ := procGetSystemMetric.Call(smCXScreen)
		sh, _, _ := procGetSystemMetric.Call(smCYScreen)
		x = int(sw)*38/100 - winW/2
		y = int(sh) - int(64*h.scale) - winH
	}
	h.walkToClamped(x, y, durationMs)
}

func (h *overlayHost) walkToClamped(x, y, durationMs int) {
	var r rect
	procGetWindowRect.Call(h.hwnd, uintptr(unsafe.Pointer(&r)))
	winW, winH := int(r.Right-r.Left), int(r.Bottom-r.Top)
	sw, _, _ := procGetSystemMetric.Call(smCXScreen)
	sh, _, _ := procGetSystemMetric.Call(smCYScreen)
	maxX := int(sw) - winW
	maxY := int(sh) - winH
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if maxX > 0 && x > maxX {
		x = maxX
	}
	if maxY > 0 && y > maxY {
		y = maxY
	}
	h.walkTo(x, y, durationMs)
}

func taskbarRect() (rect, bool) {
	className, err := utf16("Shell_TrayWnd")
	if err != nil {
		return rect{}, false
	}
	hwnd, _, _ := procFindWindow.Call(0, uintptr(unsafe.Pointer(className)))
	if hwnd == 0 {
		return rect{}, false
	}
	var r rect
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if r.Right <= r.Left || r.Bottom <= r.Top {
		return rect{}, false
	}
	return r, true
}

// beginDrag takes the mouse capture so the host can move the window directly
// from WM_MOUSEMOVE. Round-tripping every move through the page adds a
// frame of latency and makes the drag feel like it is fighting the cursor.
func (h *overlayHost) beginDrag() {
	h.cancelWalk("drag")
	if h.dragging {
		return
	}
	var p point
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p))); r == 0 {
		return
	}
	h.dragLast = p
	h.dragging = true
	procSetCapture.Call(h.hwnd)
}

func (h *overlayHost) dragMove() {
	var p point
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p))); r == 0 {
		return
	}
	dx := int(p.X - h.dragLast.X)
	dy := int(p.Y - h.dragLast.Y)
	if dx == 0 && dy == 0 {
		return
	}
	h.dragLast = p
	var r rect
	procGetWindowRect.Call(h.hwnd, uintptr(unsafe.Pointer(&r)))
	procSetWindowPos.Call(h.hwnd, hwndTopmost,
		uintptr(int(r.Left)+dx), uintptr(int(r.Top)+dy), 0, 0,
		swpNoSize|swpNoActivate)
}

func (h *overlayHost) endDrag() {
	if !h.dragging {
		return
	}
	h.dragging = false
	procReleaseCapture.Call()
	h.emitToPage(map[string]any{"event": "dragEnd"})
}

// foregroundRect returns the rectangle of the window that currently has focus.
func foregroundRect() (rect, bool) {
	hwnd, _, _ := procGetForeground.Call()
	if hwnd == 0 {
		return rect{}, false
	}
	var r rect
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if r.Right-r.Left <= 0 || r.Bottom-r.Top <= 0 {
		return rect{}, false
	}
	return r, true
}

// perchOnForeground places the window on the bottom edge of the focused
// application window, so the avatar appears to stand on it.
func (h *overlayHost) perchOnForeground() bool {
	h.cancelWalk("perch")
	target, ok := foregroundRect()
	if !ok {
		return false
	}
	var r rect
	procGetWindowRect.Call(h.hwnd, uintptr(unsafe.Pointer(&r)))
	width := r.Right - r.Left
	height := r.Bottom - r.Top
	x := int(target.Left) + (int(target.Right)-int(target.Left)-int(width))/2
	y := int(target.Bottom) - int(height)
	h.moveTo(x, y)
	h.emitToPage(map[string]any{"event": "perched"})
	return true
}

// perchLoop keeps the avatar on the focused window while it is perched.
func (h *overlayHost) perchLoop() {
	var last uintptr
	for {
		select {
		case <-h.quit:
			return
		case <-time.After(700 * time.Millisecond):
		}
		fg, _, _ := procGetForeground.Call()
		if fg == last || fg == h.hwnd {
			continue
		}
		last = fg
		h.dispatch(func() { h.perchOnForeground() })
	}
}

// keepSized re-applies the overlay's size to the WebView2 until the window
// closes. The WebView2 rebuilds its presentation surface whenever it likes, and
// a surface left at the wrong size leaves the avatar outside the overlay.
func (h *overlayHost) keepSized() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-h.quit:
			return
		case <-ticker.C:
			h.dispatch(h.resizeWebView)
		}
	}
}

func (h *overlayHost) shutdown() {
	h.once.Do(func() { close(h.quit) })
}

// ─────────────────────────────────────────────────────────────────────────────
// Making the WebView2 transparent
// ─────────────────────────────────────────────────────────────────────────────

// controller2 mirrors the layout of edge's ICoreWebView2Controller2 so the
// vtable slot for put_DefaultBackgroundColor can be called directly.
//
// The vtable is IUnknown (3 slots) plus the 23 ICoreWebView2Controller
// methods, so Controller2's getter and setter are slots 26 and 27. The
// setter takes the colour by value (a packed uint32), not by pointer.
type controller2Vtbl struct {
	_                         [26]uintptr
	GetDefaultBackgroundColor uintptr
	PutDefaultBackgroundColor uintptr
}

type controller2 struct {
	vtbl *controller2Vtbl
}

// setTransparentBackground makes the WebView2's own background fully
// transparent, so the window behind the avatar shows through.
func setTransparentBackground(c *edge.ICoreWebView2Controller) error {
	if c == nil {
		return fmt.Errorf("the WebView2 controller is not ready")
	}
	c2 := c.GetICoreWebView2Controller2()
	if c2 == nil {
		return fmt.Errorf("this WebView2 runtime has no ICoreWebView2Controller2")
	}
	mine := (*controller2)(unsafe.Pointer(c2))
	if mine.vtbl.PutDefaultBackgroundColor == 0 {
		return fmt.Errorf("put_DefaultBackgroundColor is missing from the vtable")
	}
	// COREWEBVIEW2_COLOR is {A, R, G, B} packed into a uint32, and COM
	// expects it by value. Zero means fully transparent.
	r, _, _ := syscall.SyscallN(mine.vtbl.PutDefaultBackgroundColor,
		uintptr(unsafe.Pointer(mine)), 0)
	if r != 0 {
		return fmt.Errorf("put_DefaultBackgroundColor failed: 0x%08x", r)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// The page bridge
// ─────────────────────────────────────────────────────────────────────────────

// hostBridgeScript gives the page the two functions it needs: hostCall sends
// a message to the host, pageLog forwards a console line to it.
const hostBridgeScript = `
(function () {
  window.hostCall = function (method, params) {
    try {
      window.chrome.webview.postMessage(JSON.stringify({ kind: 'call', method: method, params: params }));
    } catch (e) { /* the bridge is not up yet */ }
  };
  window.pageLog = function (level, text) {
    try {
      window.chrome.webview.postMessage(JSON.stringify({ kind: 'log', level: level, text: text }));
    } catch (e) { /* the bridge is not up yet */ }
  };
})();
`

// pageLogBridgeScript forwards the page's console output and uncaught errors to
// the host's log. Without this the overlay is a silent window: if the page
// fails to load, nothing on screen says why.
const pageLogBridgeScript = `
(function () {
  function send(level, args) {
    try {
      var text = Array.prototype.map.call(args, function (a) {
        if (a instanceof Error) return a.message;
        if (typeof a === 'object') { try { return JSON.stringify(a); } catch (e) { return String(a); } }
        return String(a);
      }).join(' ');
      if (typeof window.pageLog === 'function') { window.pageLog(level, text); return; }
    } catch (e) { /* the bridge is not up yet */ }
  }
  ['log', 'info', 'warn', 'error'].forEach(function (level) {
    var original = console[level];
    console[level] = function () {
      send(level, arguments);
      if (original) original.apply(console, arguments);
    };
  });
  window.addEventListener('error', function (e) {
    send('error', [e.message + ' @ ' + e.filename + ':' + e.lineno]);
  });
  window.addEventListener('unhandledrejection', function (e) {
    send('error', ['unhandled rejection: ' + (e.reason && e.reason.message ? e.reason.message : e.reason)]);
  });
})();
`

// onMessage receives a message from the page. The page sends two kinds:
// a call into the host, and a console line.
func (h *overlayHost) onMessage(raw string) {
	var m struct {
		Kind   string `json:"kind"`
		Method string `json:"method"`
		Params string `json:"params"`
		Level  string `json:"level"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		log.Printf("[OVERLAY-PAGE] could not read a message: %v", err)
		return
	}
	switch m.Kind {
	case "call":
		h.onHostCall(m.Method, m.Params)
	case "log":
		h.onPageLog(m.Level, m.Text)
	default:
		// The page's fallback path posts {method, params} without a kind.
		if m.Method != "" {
			h.onHostCall(m.Method, m.Params)
		}
	}
}

// onHostCall receives a call from the page: a method name and a JSON object of
// parameters.
func (h *overlayHost) onHostCall(method, params string) {
	switch method {
	case "moveBy":
		var p struct {
			DX int `json:"dx"`
			DY int `json:"dy"`
		}
		json.Unmarshal([]byte(params), &p)
		h.dispatch(func() { h.moveBy(p.DX, p.DY) })
	case "beginDrag":
		h.dispatch(func() { h.beginDrag() })
	case "endDrag":
		h.dispatch(func() { h.endDrag() })
	case "perchOnForeground":
		h.dispatch(func() { h.perchOnForeground() })
	case "setClickThrough":
		var p struct {
			On bool `json:"on"`
		}
		json.Unmarshal([]byte(params), &p)
		h.dispatch(func() { h.setClickThrough(p.On) })
	case "walkTo":
		var p struct {
			X          int `json:"x"`
			Y          int `json:"y"`
			DurationMS int `json:"duration_ms"`
		}
		json.Unmarshal([]byte(params), &p)
		h.dispatch(func() { h.walkTo(p.X, p.Y, p.DurationMS) })
	case "walkToForeground":
		var p struct {
			DurationMS int `json:"duration_ms"`
		}
		json.Unmarshal([]byte(params), &p)
		h.dispatch(func() { h.walkToForeground(p.DurationMS) })
	case "walkToTaskbar":
		var p struct {
			DurationMS int `json:"duration_ms"`
		}
		json.Unmarshal([]byte(params), &p)
		h.dispatch(func() { h.walkToTaskbar(p.DurationMS) })
	case "cancelWalk":
		h.dispatch(func() { h.cancelWalk("page") })
	case "quit":
		h.dispatch(func() {
			h.cancelWalk("quit")
			h.shutdown()
			procPostMessage.Call(h.hwnd, wmClose, 0, 0)
		})
	}
}

// onPageLog receives a console line from the page. Without this the overlay is
// a silent window: if the page fails to load, nothing on screen says why.
func (h *overlayHost) onPageLog(level, text string) {
	log.Printf("[OVERLAY-PAGE] %s: %s", level, strings.TrimSpace(text))
}

// emitToPage sends an event to the page.
func (h *overlayHost) emitToPage(payload map[string]any) {
	if h.chromium == nil {
		return
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	h.chromium.Eval(fmt.Sprintf("window.virtual && window.virtual.onHostEvent(%s)", string(b)))
}

// ─────────────────────────────────────────────────────────────────────────────
// The companion server
// ─────────────────────────────────────────────────────────────────────────────

// checkServer makes sure the companion server is actually there before the
// overlay navigates to it.
func checkServer(rawURL string) error {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL: %w", rawURL, err)
	}
	host := u.Host
	if u.Port() == "" {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	conn, err := net.DialTimeout("tcp", host, 2*time.Second)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", host, err)
	}
	conn.Close()
	return nil
}

// serverDownPage is shown when the companion server is not running. The window
// is transparent, so the message floats over the desktop.
const serverDownPage = `<!DOCTYPE html><html><body style="margin:0;background:transparent;
font:13px/1.5 system-ui,sans-serif;color:#fff;display:flex;align-items:center;
justify-content:center;height:100vh;text-align:center">
<div style="background:rgba(20,20,24,.85);border-radius:10px;padding:14px 18px">
<div style="font-size:15px;margin-bottom:6px">Mai's server is not running</div>
<div style="opacity:.8">Start it with<br><code>make companion-virtual</code></div>
</div></body></html>`

// utf16 converts a string to a UTF-16 pointer for the Win32 calls.
func utf16(s string) (*uint16, error) {
	b, err := syscall.UTF16FromString(s)
	if err != nil {
		return nil, err
	}
	return &b[0], nil
}
