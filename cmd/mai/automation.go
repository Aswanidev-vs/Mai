package main

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-vgo/robotgo"
)

type Automation struct {
	// defaultDelay is the pause between automation steps to let UI catch up.
	defaultDelay  time.Duration
	vision        *Vision
	visionEnabled bool
}

// NewAutomation creates a new Automation instance with sensible defaults.
func NewAutomation(visionModel, visionURL string, visionEnabled bool) *Automation {
	return &Automation{
		defaultDelay:  500 * time.Millisecond,
		vision:        NewVision(visionModel, visionURL),
		visionEnabled: visionEnabled,
	}
}

// SetDelay changes the default delay between automation steps.
func (a *Automation) SetDelay(d time.Duration) {
	a.defaultDelay = d
}

// appInfo holds launch metadata for a known application.
type appInfo struct {
	exeName     string   // Executable name for direct launch
	windowTitle string   // Window title keyword for verification
	protocol    string   // Protocol URI (e.g., "ms-settings:") for UWP apps
	altTitles   []string // Alternative window title fragments
}

// knownApps maps common names to their launch metadata.
var knownApps = map[string]appInfo{
	"chrome":        {exeName: "chrome", windowTitle: "Google Chrome"},
	"google chrome": {exeName: "chrome", windowTitle: "Google Chrome"},
	"brave":         {exeName: "brave", windowTitle: "Brave"},
	"firefox":       {exeName: "firefox", windowTitle: "Firefox"},
	"edge":          {exeName: "msedge", windowTitle: "Microsoft Edge"},
	"notepad":       {exeName: "notepad", windowTitle: "Notepad"},
	"calculator":    {exeName: "calc", windowTitle: "Calculator"},
	"whatsapp":      {exeName: "WhatsApp", windowTitle: "WhatsApp", altTitles: []string{"WhatsApp"}},
	"telegram":      {exeName: "Telegram", windowTitle: "Telegram", altTitles: []string{"Telegram Desktop"}},
	"discord":       {exeName: "Discord", windowTitle: "Discord"},
	"spotify":       {exeName: "Spotify", windowTitle: "Spotify"},
	"vscode":        {exeName: "Code", windowTitle: "Visual Studio Code"},
	"code":          {exeName: "Code", windowTitle: "Visual Studio Code"},
	"terminal":      {exeName: "wt", windowTitle: "Terminal", altTitles: []string{"Windows Terminal", "Command Prompt"}},
	"cmd":           {exeName: "cmd", windowTitle: "Command Prompt"},
	"explorer":      {exeName: "explorer", windowTitle: "File Explorer"},
	"settings":      {exeName: "ms-settings:", windowTitle: "Settings", protocol: "ms-settings:"},
	"word":          {exeName: "winword", windowTitle: "Word"},
	"excel":         {exeName: "excel", windowTitle: "Excel"},
	"powerpoint":    {exeName: "powerpnt", windowTitle: "PowerPoint"},
	"paint":         {exeName: "mspaint", windowTitle: "Paint"},
	"photos":        {exeName: "ms-photos:", windowTitle: "Photos", protocol: "ms-photos:"},
}

// OpenApp opens an application by name.
// Strategy order: Protocol URI → AppID (Get-StartApps) → Direct exe → PATH search → Windows Search (last resort).
func (a *Automation) OpenApp(name string) error {
	log.Printf("[AUTO] Opening application: %s", name)
	nameLower := strings.ToLower(strings.TrimSpace(name))

	// Resolve known app info
	info, isKnown := knownApps[nameLower]
	exeName := name
	windowKeyword := name
	if isKnown {
		exeName = info.exeName
		windowKeyword = info.windowTitle
	}

	// Collect all window keywords to check (primary + alternatives)
	allKeywords := []string{windowKeyword}
	if isKnown {
		for _, alt := range info.altTitles {
			if alt != windowKeyword {
				allKeywords = append(allKeywords, alt)
			}
		}
	}

	// Helper: check if any of our window keywords appear
	waitAny := func(timeout time.Duration) bool {
		for _, kw := range allKeywords {
			if a.waitForAppWindow(kw, timeout) {
				return true
			}
		}
		return false
	}

	// Strategy 1: Protocol URI (ms-settings:, ms-photos:, etc.)
	if isKnown && info.protocol != "" && runtime.GOOS == "windows" {
		log.Printf("[AUTO] Trying protocol URI: %s", info.protocol)
		cmd := exec.Command(`C:\Windows\explorer.exe`, info.protocol)
		if err := cmd.Start(); err == nil {
			if waitAny(5 * time.Second) {
				log.Printf("[AUTO] Successfully opened via protocol: %s", name)
				return nil
			}
			return fmt.Errorf("protocol launch succeeded for %s but window not detected", name)
		}
	}

	// Strategy 2: AppID via Get-StartApps → shell:AppsFolder (most reliable for Store/UWP apps)
	if runtime.GOOS == "windows" {
		if appPath := a.findAppInStartMenu(name); appPath != "" {
			log.Printf("[AUTO] Found AppID via Start Menu: %s", appPath)
			var cmd *exec.Cmd
			// Check if result is an AppID (e.g., 5319275A.WhatsAppDesktop_cv1g1gnamwwy8!App) vs file path
			if !strings.Contains(appPath, `\`) && !strings.Contains(appPath, `/`) {
				// AppID - launch via explorer shell:AppsFolder
				cmd = exec.Command(`C:\Windows\explorer.exe`, `shell:AppsFolder\`+appPath)
			} else {
				// Regular file path
				cmdPath := `C:\Windows\System32\cmd.exe`
				cmd = exec.Command(cmdPath, "/c", "start", "", appPath)
			}
			if err := cmd.Start(); err == nil {
				// Give UWP apps more time to start (they can be slow on first launch)
				if waitAny(8 * time.Second) {
					log.Printf("[AUTO] Successfully opened via AppID: %s", name)
					return nil
				}
				// User requested: If found, don't fallback to the rest
				return fmt.Errorf("app identified in Start Menu but window not detected: %s", appPath)
			}
		}
	}

	// Strategy 3: Try direct executable open
	log.Printf("[AUTO] Trying direct executable: %s", exeName)
	if err := a.tryDirectOpen(exeName); err == nil {
		if waitAny(5 * time.Second) {
			log.Printf("[AUTO] Successfully opened and verified: %s", name)
			return nil
		}
		// If we tried a specific name that exists in PATH or knownApps, don't keep guessing
		return fmt.Errorf("direct launch executed for %s but window not detected", exeName)
	}

	// Strategy 4: Try to find executable in PATH or common locations
	if fullPath := a.findExecutablePath(exeName); fullPath != "" {
		log.Printf("[AUTO] Found executable at: %s", fullPath)
		if err := a.tryDirectOpen(fullPath); err == nil {
			if waitAny(5 * time.Second) {
				log.Printf("[AUTO] Successfully opened via PATH: %s", name)
				return nil
			}
			return fmt.Errorf("executable found at %s but failed to detect window", fullPath)
		}
	}

	// Strategy 5: Windows Search (last resort - keyboard automation is unreliable)
	if runtime.GOOS == "windows" {
		log.Printf("[AUTO] Trying Windows Search for: %s", name)
		if err := a.openViaWindowsSearch(name); err == nil {
			if waitAny(6 * time.Second) {
				log.Printf("[AUTO] Successfully opened via Windows Search: %s", name)
				return nil
			}
			// Dismiss search if it didn't work
			robotgo.KeyTap("escape")
			time.Sleep(200 * time.Millisecond)
		}
	}

	return fmt.Errorf("could not open application: %s", name)
}

// tryDirectOpen attempts to run an executable using the OS-specific method.
func (a *Automation) tryDirectOpen(exePath string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmdPath := a.getCmdPath()
		// Use start command for non-.exe paths (like UWP apps)
		if !strings.HasSuffix(strings.ToLower(exePath), ".exe") && !strings.Contains(exePath, `\`) {
			cmd = exec.Command(cmdPath, "/c", "start", "", exePath)
		} else {
			cmd = exec.Command(cmdPath, "/c", "start", "", exePath)
		}
	} else if runtime.GOOS == "darwin" {
		cmd = exec.Command("open", exePath)
	} else {
		cmd = exec.Command(exePath)
	}

	// Start and wait briefly to catch immediate errors
	if err := cmd.Start(); err != nil {
		return err
	}

	// Give it a moment to fail if executable doesn't exist
	time.Sleep(200 * time.Millisecond)
	if cmd.Process != nil {
		// Check if process exited immediately (failure)
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()
		select {
		case err := <-done:
			if err != nil {
				return fmt.Errorf("process exited with error: %v", err)
			}
		case <-time.After(300 * time.Millisecond):
			// Still running, probably succeeded
		}
	}

	return nil
}

// findExecutablePath searches for the executable in PATH and common locations.
func (a *Automation) findExecutablePath(name string) string {
	nameLower := strings.ToLower(name)

	// Try where.exe on Windows
	if runtime.GOOS == "windows" {
		wherePath := `C:\Windows\System32\where.exe`
		if out, err := exec.Command(wherePath, nameLower).Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" && strings.HasSuffix(strings.ToLower(line), ".exe") {
					return line
				}
			}
		}
	}

	// Search common installation directories
	if runtime.GOOS == "windows" {

		localAppData := os.Getenv("LOCALAPPDATA")
		programFiles := os.Getenv("PROGRAMFILES")
		programFilesX86 := os.Getenv("PROGRAMFILES(X86)")

		// Common paths for Electron apps, UWP apps, etc.
		candidates := []string{
			filepath.Join(localAppData, name, name+".exe"),
			filepath.Join(localAppData, name, "app", name+".exe"),
			filepath.Join(localAppData, "Programs", name, name+".exe"),
			filepath.Join(programFiles, name, name+".exe"),
			filepath.Join(programFilesX86, name, name+".exe"),
			filepath.Join(programFiles, name, "bin", name+".exe"),
			// Special cases for common apps
			filepath.Join(localAppData, "Microsoft", "WindowsApps", name+".exe"),
			filepath.Join(localAppData, "Microsoft", "WindowsApps", nameLower+".exe"),
		}

		// Only add app-specific hardcoded paths when searching for that app
		if nameLower == "brave" {
			candidates = append(candidates,
				filepath.Join(programFiles, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
				filepath.Join(programFilesX86, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
				filepath.Join(localAppData, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
			)
		}
		if nameLower == "telegram" {
			candidates = append(candidates,
				filepath.Join(localAppData, "Telegram Desktop", "Telegram.exe"),
				filepath.Join(programFiles, "Telegram Desktop", "Telegram.exe"),
			)
		}
		if nameLower == "vscode" || nameLower == "code" {
			candidates = append(candidates,
				filepath.Join(localAppData, "Programs", "Microsoft VS Code", "Code.exe"),
				filepath.Join(programFiles, "Microsoft VS Code", "Code.exe"),
			)
		}

		for _, path := range candidates {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}

	return ""
}

// findAppInStartMenu uses PowerShell to search for apps in the Start Menu.
func (a *Automation) findAppInStartMenu(name string) string {
	if runtime.GOOS != "windows" {
		return ""
	}

	// Use PowerShell to find app in Start Menu
	psCmd := fmt.Sprintf(
		`Get-StartApps | Where-Object { $_.Name -like '*%s*' } | Select-Object -First 1 -ExpandProperty AppID`,
		name,
	)

	cmdPath := `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
	if _, err := os.Stat(cmdPath); os.IsNotExist(err) {
		cmdPath = `C:\WINDOWS\System32\WindowsPowerShell\v1.0\powershell.exe`
	}

	out, err := exec.Command(cmdPath, "-NoProfile", "-Command", psCmd).Output()
	if err != nil {
		return ""
	}

	result := strings.TrimSpace(string(out))
	if result != "" {
		return result
	}

	return ""
}

// waitForAppWindow checks if an actual application window (not a search panel)
// with the given keyword appears within the timeout.
// It excludes Windows Search, Start Menu, Cortana and similar transient panels.
func (a *Automation) waitForAppWindow(keyword string, timeout time.Duration) bool {
	keywordLower := strings.ToLower(keyword)
	deadline := time.Now().Add(timeout)
	checkInterval := 300 * time.Millisecond

	// Titles that indicate a search/transient window, not the actual app
	excludeTitles := []string{"search", "start", "cortana", "type here to search"}

	isExcluded := func(title string) bool {
		titleLower := strings.ToLower(title)
		for _, excl := range excludeTitles {
			if strings.Contains(titleLower, excl) {
				return true
			}
		}
		return false
	}

	for time.Now().Before(deadline) {
		// Check active window title
		activeTitle := robotgo.GetTitle()
		activeTitleLower := strings.ToLower(activeTitle)

		// Must contain our keyword AND not be a search/transient panel
		if strings.Contains(activeTitleLower, keywordLower) && !isExcluded(activeTitle) {
			log.Printf("[AUTO] Window verified (active): %q", activeTitle)
			return true
		}

		// Also try FindWindow for background windows
		if hwnd := robotgo.FindWindow(keyword); hwnd != 0 {
			log.Printf("[AUTO] Window verified (FindWindow): %q", keyword)
			return true
		}

		// For multi-word keywords, try matching all parts
		parts := strings.Fields(keywordLower)
		if len(parts) > 1 {
			allMatch := true
			for _, part := range parts {
				if !strings.Contains(activeTitleLower, part) {
					allMatch = false
					break
				}
			}
			if allMatch && !isExcluded(activeTitle) {
				log.Printf("[AUTO] Window verified (multi-word): %q", activeTitle)
				return true
			}
		}

		time.Sleep(checkInterval)
	}

	log.Printf("[AUTO] Window NOT found for %q after %v", keyword, timeout)
	return false
}

// openViaWindowsSearch opens an app using Windows Search (Win+S).
// This is a last-resort strategy since it relies on keyboard automation.
func (a *Automation) openViaWindowsSearch(appName string) error {
	// Open Windows Search with Win+S
	robotgo.KeyTap("s", "win")
	time.Sleep(1200 * time.Millisecond) // Give search more time to open

	// Type the app name character by character for reliability
	for _, ch := range appName {
		robotgo.TypeStr(string(ch))
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond) // Wait for search results to populate

	// Press Enter to open the first result
	robotgo.KeyTap("enter")
	time.Sleep(a.defaultDelay)

	return nil
}

// TypeText types the given text at the current cursor position.
func (a *Automation) TypeText(text string) error {
	log.Printf("[AUTO] Typing text: %q", text)
	robotgo.TypeStr(text)
	time.Sleep(100 * time.Millisecond)
	return nil
}

// PressKey presses a single key. Supports modifiers like "ctrl", "alt", "shift".
// Examples: "enter", "tab", "esc", "ctrl+c", "alt+tab"
func (a *Automation) PressKey(keyCombo string) error {
	log.Printf("[AUTO] Pressing key: %s", keyCombo)

	parts := strings.Split(keyCombo, "+")
	if len(parts) == 1 {
		robotgo.KeyTap(parts[0])
	} else {
		// Last part is the main key, rest are modifiers
		mainKey := parts[len(parts)-1]
		modifiers := parts[:len(parts)-1]
		// Normalize modifiers
		for i, mod := range modifiers {
			mod = strings.ToLower(strings.TrimSpace(mod))
			if mod == "control" {
				modifiers[i] = "ctrl"
			} else {
				modifiers[i] = mod
			}
		}
		robotgo.KeyTap(mainKey, modifiers)
	}

	time.Sleep(100 * time.Millisecond)
	return nil
}

// FocusWindow brings a window to the foreground by matching its title.
func (a *Automation) FocusWindow(title string) error {
	log.Printf("[AUTO] Focusing window: %s", title)

	pid := robotgo.FindWindow(title)
	if pid == 0 {
		return fmt.Errorf("window not found: %s", title)
	}

	robotgo.ActivePid(int(pid))
	time.Sleep(a.defaultDelay)
	return nil
}

// SendMessage automates sending a message in a specific messaging app.
// If a contact is provided, it attempts to search for that contact first.
func (a *Automation) SendMessage(app, contact, text string) error {
	// Clean up quotes that might have been captured or added by LLM/ASR
	text = strings.Trim(strings.TrimSpace(text), "\"'")
	contact = strings.Trim(strings.TrimSpace(contact), "\"'")

	log.Printf("[AUTO] Sending message on %s to %q: %q", app, contact, text)
	appLower := strings.ToLower(app)

	// Step 1: Ensure app is open and focused
	if err := a.OpenApp(app); err != nil {
		return fmt.Errorf("could not open %s: %v", app, err)
	}
	time.Sleep(1500 * time.Millisecond) // Give it time to fully come to foreground

	// Resolve the correct window title for focusing
	windowTitle := app
	if info, ok := knownApps[strings.ToLower(app)]; ok {
		windowTitle = info.windowTitle
	}

	// Step 2: If a contact is specified, search for them
	if contact != "" {
		log.Printf("[AUTO] Searching for contact: %s", contact)
		// Most messaging apps (WhatsApp, Telegram, Discord) 
		if appLower == "whatsapp" || appLower == "telegram" || appLower == "discord" {
			// Ensure we are focused on the correct window
			a.FocusWindow(windowTitle)
			time.Sleep(500 * time.Millisecond)

			// User specifically requested Ctrl+F for search
			searchKey := "f"
			
			log.Printf("[AUTO] Triggering search with Ctrl+%s", searchKey)
			robotgo.KeyTap(searchKey, "ctrl")
			time.Sleep(1000 * time.Millisecond)

			// Clear any previous search and type contact name
			log.Printf("[AUTO] Clearing search bar...")
			robotgo.KeyTap("a", "ctrl")
			robotgo.KeyTap("backspace")
			time.Sleep(400 * time.Millisecond)
			
			log.Printf("[AUTO] Typing contact name: %s", contact)
			robotgo.TypeStr(contact)
			time.Sleep(2000 * time.Millisecond) // Wait for search results to populate

			// Press Enter to select the first result
			log.Printf("[AUTO] Selecting contact and opening chat...")
			robotgo.KeyTap("enter")
			time.Sleep(1500 * time.Millisecond) // Wait for chat to open
		}
	}

	// Step 3: Type the message
	log.Printf("[AUTO] Typing message...")
	robotgo.TypeStr(text)
	time.Sleep(200 * time.Millisecond)

	// Step 4: Press Enter to send
	robotgo.KeyTap("enter")
	log.Printf("[AUTO] Message sent successfully")

	return nil
}

// MouseClick moves the mouse to (x, y) and clicks.
func (a *Automation) MouseClick(x, y int) error {
	log.Printf("[AUTO] Clicking at (%d, %d)", x, y)
	robotgo.Move(x, y)
	time.Sleep(50 * time.Millisecond)
	robotgo.Click()
	time.Sleep(50 * time.Millisecond)
	return nil
}

// MouseMove moves the mouse to (x, y) without clicking.
func (a *Automation) MouseMove(x, y int) error {
	log.Printf("[AUTO] Moving mouse to (%d, %d)", x, y)
	robotgo.Move(x, y)
	return nil
}

// GetScreenSize returns the current screen dimensions.
func (a *Automation) GetScreenSize() (width, height int) {
	return robotgo.GetScreenSize()
}

// Scroll performs a mouse scroll. positive = up, negative = down.
func (a *Automation) Scroll(amount int) {
	log.Printf("[AUTO] Scrolling: %d", amount)
	robotgo.Scroll(0, amount)
}

// TakeScreenshot captures the entire screen and saves it to a file.
func (a *Automation) TakeScreenshot(filename string) error {
	log.Printf("[AUTO] Taking screenshot: %s", filename)
	return robotgo.SaveCapture(filename)
}

// CloseApp attempts to close an application by window title.
func (a *Automation) CloseApp(title string) error {
	log.Printf("[AUTO] Closing application: %s", title)
	pid := robotgo.FindWindow(title)
	if pid == 0 {
		return fmt.Errorf("window not found: %s", title)
	}
	robotgo.Kill(int(pid))
	return nil
}

// Wait pauses execution for the default delay duration.
func (a *Automation) Wait() {
	time.Sleep(a.defaultDelay)
}

// WebSearch opens a platform-specific search or site.
func (a *Automation) WebSearch(platform, query, browser string) error {
	platform = strings.ToLower(platform)
	var urlStr string

	switch platform {
	case "google":
		if query != "" {
			urlStr = "https://www.google.com/search?q=" + url.QueryEscape(query)
		} else {
			urlStr = "https://www.google.com"
		}
	case "youtube":
		if query != "" {
			urlStr = "https://www.youtube.com/results?search_query=" + url.QueryEscape(query)
		} else {
			urlStr = "https://www.youtube.com"
		}
	case "bing":
		if query != "" {
			urlStr = "https://www.bing.com/search?q=" + url.QueryEscape(query)
		} else {
			urlStr = "https://www.bing.com"
		}
	case "github":
		if query != "" {
			urlStr = "https://github.com/search?q=" + url.QueryEscape(query)
		} else {
			urlStr = "https://github.com"
		}
	case "wikipedia":
		if query != "" {
			urlStr = "https://en.wikipedia.org/wiki/Special:Search?search=" + url.QueryEscape(query)
		} else {
			urlStr = "https://en.wikipedia.org"
		}
	case "spotify":
		if query != "" {
			urlStr = "https://open.spotify.com/search/" + url.QueryEscape(query)
		} else {
			urlStr = "https://open.spotify.com"
		}
	case "soundcloud":
		if query != "" {
			urlStr = "https://soundcloud.com/search?q=" + url.QueryEscape(query)
		} else {
			urlStr = "https://soundcloud.com"
		}
	default:
		// If it looks like a domain, open it directly
		if strings.Contains(platform, ".") {
			urlStr = "https://" + platform
			if query != "" {
				urlStr += "/search?q=" + url.QueryEscape(query)
			}
		} else {
			// Fallback to Google search
			urlStr = "https://www.google.com/search?q=" + url.QueryEscape(platform+" "+query)
		}
	}

	return a.OpenURL(urlStr, browser)
}

// PlayMedia attempts to play content on a specific platform.
func (a *Automation) PlayMedia(platform, query, browser string) error {
	log.Printf("[AUTO] Playing %q on %s (browser: %s)", query, platform, browser)
	platform = strings.ToLower(platform)

	if platform == "youtube" {
		urlStr := "https://www.youtube.com/results?search_query=" + url.QueryEscape(query)
		err := a.OpenURL(urlStr, browser)
		if err != nil {
			return err
		}

		// Wait for browser to open and load search results
		time.Sleep(4 * time.Second)

		// Focus the browser window using the known window title
		if browser != "" {
			browserLower := strings.ToLower(browser)
			if info, ok := knownApps[browserLower]; ok {
				robotgo.ActiveName(info.windowTitle)
			} else {
				robotgo.ActiveName(browser)
			}
		} else {
			robotgo.ActiveName("YouTube")
		}
		time.Sleep(500 * time.Millisecond)

		return a.clickFirstYouTubeVideo()
	}

	// Fallback to regular search for other platforms
	return a.WebSearch(platform, query, browser)
}

// clickFirstYouTubeVideo attempts to click the first video on a YouTube
// search results page using multiple strategies.
func (a *Automation) clickFirstYouTubeVideo() error {
	screenW, screenH := a.GetScreenSize()

	// Strategy 1: Click on the first video thumbnail.
	// YouTube search results layout (with sidebar collapsed):
	//   - Top bar:          ~5% height
	//   - Filter chips row: ~5-15% height
	//   - First video card: starts ~18% height, thumbnail at ~30% width
	// We target the center of the first video thumbnail at 30% X, 35% Y
	// which sits well below the filter chips ("All", "Videos", "Shorts", "Unwatched").
	clickX := screenW * 30 / 100
	clickY := screenH * 35 / 100
	log.Printf("[AUTO] Strategy 1: Clicking first video thumbnail at (%d, %d) on %dx%d screen", clickX, clickY, screenW, screenH)
	a.MouseClick(clickX, clickY)
	time.Sleep(2 * time.Second)

	// Strategy 2: Tab-navigate from the search bar to the first video link.
	// On YouTube search results, pressing Tab from the page body cycles through
	// interactive elements. The first video link is typically 3-5 tabs away from
	// the search bar (past filter chips). We press Tab several times then Enter.
	log.Println("[AUTO] Strategy 2: Tab-navigating to first video and pressing Enter...")
	robotgo.KeyTap("tab")
	time.Sleep(200 * time.Millisecond)
	robotgo.KeyTap("tab")
	time.Sleep(200 * time.Millisecond)
	robotgo.KeyTap("tab")
	time.Sleep(200 * time.Millisecond)
	robotgo.KeyTap("tab")
	time.Sleep(200 * time.Millisecond)
	robotgo.KeyTap("tab")
	time.Sleep(200 * time.Millisecond)
	robotgo.KeyTap("enter")
	time.Sleep(2 * time.Second)

	// Press 'k' to toggle play/pause (YouTube keyboard shortcut).
	// This works on the video watch page to ensure playback started.
	robotgo.KeyTap("k")
	return nil
}

// OpenURL opens a URL in a specific browser or the default one.
func (a *Automation) OpenURL(urlStr, browser string) error {
	browser = strings.ToLower(strings.TrimSpace(browser))
	exeName := ""

	if browser != "" {
		if info, ok := knownApps[browser]; ok {
			exeName = info.exeName
		} else {
			exeName = browser
		}
	}

	if exeName != "" {
		log.Printf("[AUTO] Opening URL in %s: %s", exeName, urlStr)
		// Check if we can find the full path for the browser
		if fullPath := a.findExecutablePath(exeName); fullPath != "" {
			exeName = fullPath
		}

		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			// Use start to launch the browser with the URL
			cmd = exec.Command(a.getCmdPath(), "/c", "start", "", exeName, urlStr)
		} else {
			cmd = exec.Command(exeName, urlStr)
		}
		return cmd.Start()
	}

	// Default browser
	log.Printf("[AUTO] Opening URL in default browser: %s", urlStr)
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command(a.getCmdPath(), "/c", "start", "", urlStr)
	} else if runtime.GOOS == "darwin" {
		cmd = exec.Command("open", urlStr)
	} else {
		cmd = exec.Command("xdg-open", urlStr)
	}
	return cmd.Start()
}

// getCmdPath returns the full path to cmd.exe on Windows.
func (a *Automation) getCmdPath() string {
	if runtime.GOOS != "windows" {
		return "sh"
	}
	// Try standard locations
	paths := []string{
		`C:\Windows\System32\cmd.exe`,
		`C:\WINDOWS\system32\cmd.exe`,
		filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "cmd" // Fallback to PATH
}
