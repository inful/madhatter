//go:build e2e

// Package e2e exercises the running server through a real headless
// Chromium browser. The intent is to catch the regressions a
// pure-handler unit test misses: a template that 500s on the
// first render, an HTMX wiring that stops swapping, a redirect
// chain that breaks when one link changes, a CSS selector the
// dashboard relies on, etc.
//
// Run with:
//
//	go test -tags=e2e ./internal/e2e/...
//
// The build tag keeps this off the regular `go test ./...` path
// because the harness depends on Chromium being available and a
// one-time binary build (~30s on a cold cache).
//
// Required:
//   - chromium or chrome on PATH, or set CHROMEDP_CHROME_PATH
//   - SESSION_SECRET env var (set automatically by the harness)
//
// Set GOPRIVATE/XDG_CONFIG_HOME-style env if you want to point
// at a specific Chromium binary; otherwise `/opt/homebrew/bin/
// chromium` and `chromium` on PATH are auto-detected.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	// chromeBinary is the macOS Chrome install path. Chromium's
	// Homebrew cask shim ships a wrapper that points at
	// /Applications/Chromium.app/Contents/MacOS/Chromium, which
	// often isn't fully installed on the same host as Chrome
	// itself; we use Chrome (binary name "Google Chrome") as the
	// deterministic default on macOS, falling through to PATH for
	// CI / Linux.
	chromeBinary = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

	// readinessTimeout caps how long TestMain waits for the server
	// to bind its port. The server starts in well under a second on
	// any reasonable machine; this is just insurance against a bad
	// DB-file lock or a slow box.
	readinessTimeout = 30 * time.Second
)

var (
	harness *Harness
)

// Harness owns the subprocess server + the discovered base URL.
// All e2e tests share one harness (one binary, one server, one
// database); per-test isolation comes from a fresh chromedp
// browser context per test.
type Harness struct {
	binary  string
	workDir string
	cmd     *exec.Cmd
	BaseURL string
	Port    int
}

// Start launches the server subprocess and waits for it to be ready.
// Failures here are TestMain-level — every subsequent test
// depends on this completing successfully.
func Start() (*Harness, error) {
	port, err := pickFreePort()
	if err != nil {
		return nil, fmt.Errorf("pick free port: %w", err)
	}

	workDir, err := os.MkdirTemp("", "madhatter-e2e-*")
	if err != nil {
		return nil, fmt.Errorf("mkdir work dir: %w", err)
	}

	binary, err := buildBinary(workDir)
	if err != nil {
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("build binary: %w", err)
	}

	// Seed the dev user that the /auth/fake/login callback
	// resolves when the dev mode SELECT runs. Without this row
	// the callback handler bails with "sql: no rows in result
	// set" and the user is bounced back to /login. The CLI
	// "team add" path applies migrations + inserts the row in
	// one subprocess call, so by the time we start serve
	// support_rota.db is fully migrated and seeded.
	if err := preSeedDevUser(binary, workDir); err != nil {
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("seed dev user: %w", err)
	}

	cmd := exec.Command(binary, "serve", strconv.Itoa(port), "--development")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"SESSION_SECRET=test-secret-for-e2e-do-not-use-in-prod",
	)
	// The server's support_rota.db is opened relative to CWD; we
	// point CWD at workDir so the DB lives in the temp directory and
	// each TestMain run gets a fresh database.
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("start server: %w", err)
	}

	h := &Harness{
		binary:  binary,
		workDir: workDir,
		cmd:     cmd,
		BaseURL: fmt.Sprintf("http://localhost:%d", port),
		Port:    port,
	}

	if err := h.waitReady(readinessTimeout); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("server not ready: %w", err)
	}

	return h, nil
}

// preSeedDevUser runs `madhatter-e2e team add "Development User"
// dev@example.com` so the dev-mode fake login has a row to
// resolve the callback against.
func preSeedDevUser(binary, workDir string) error {
	cmd := exec.Command(binary, "team", "add", "Development User", "dev@example.com")
	cmd.Dir = workDir
	out := &bytes.Buffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("team add: %w\n%s", err, out.String())
	}
	return nil
}

// Stop kills the subprocess and removes the temp workdir.
func (h *Harness) Stop() {
	if h.cmd != nil && h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
		_ = h.cmd.Wait()
	}
	if h.workDir != "" {
		_ = os.RemoveAll(h.workDir)
	}
}

// waitReady polls the server's /login endpoint (always reachable,
// even with no auth, before fake-login completes) until it
// responds 2xx or 3xx, or the timeout elapses.
func (h *Harness) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	backoff := 50 * time.Millisecond
	for {
		resp, err := client.Get(h.BaseURL + "/login")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode < 400 &&
				bytes.Contains(body, []byte("login")) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %s; server did not bind", timeout)
		}
		time.Sleep(backoff)
		if backoff < 500*time.Millisecond {
			backoff *= 2
		}
	}
}

// pickFreePort asks the kernel for an unused TCP port and
// releases it; this is the standard "ask for a free port" pattern
// and works across linux/darwin.
func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// buildBinary compiles the madhatter binary into workDir and
// returns its absolute path.
//
// The main package lives at the repo root (./main.go) and
// delegates to ./cmd for the CLI surface. Compiling ./cmd alone
// yields an archive (no main()) rather than an executable;
// the parent path "." must be passed so Go links the entry
// point.
func buildBinary(workDir string) (string, error) {
	binary := filepath.Join(workDir, "madhatter-e2e")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = findRepoRoot()
	out := &bytes.Buffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go build: %w\n%s", err, out.String())
	}
	return binary, nil
}

// findRepoRoot walks up from this test file to find the module
// root (where go.mod lives). The e2e suite spawns go build from
// the module root so the build context matches the production
// binary.
func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}

// chromiumPath resolves a usable headless Chromium binary. We try
// the platform-specific Chrome install first, then PATH
// fallbacks. If nothing is found the test should fail with a
// clear message rather than a chromedp panic.
func chromiumPath() string {
	candidates := []string{
		os.Getenv("CHROMEDP_CHROME_PATH"), // user override
		chromeBinary,
		"/opt/homebrew/bin/chromium",
		"/usr/bin/chromium",
		"/usr/bin/google-chrome",
		"/usr/bin/chrome",
		"chromium",
		"google-chrome",
		"chrome",
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if strings.HasPrefix(c, "/") {
			if _, err := os.Stat(c); err == nil {
				return c
			}
			continue
		}
		if path, err := exec.LookPath(c); err == nil {
			return path
		}
	}
	return ""
}

// browserContext creates a fresh chromedp browser context with
// isolated cookies/storage. The allocContext is captured separately
// so the test can cancel the entire chain on cleanup.
//
// If no Chromium binary is found, the test is skipped with a
// helpful message rather than failing with a chromedp panic.
func (h *Harness) browserContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()

	bin := chromiumPath()
	if bin == "" {
		t.Skip("no chromium binary found; install with `brew install --cask chromium` " +
			"or set CHROMEDP_CHROME_PATH")
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(bin),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(allocCtx,
		chromedp.WithLogf(t.Logf),
	)
	cleanup := func() {
		cancel()
		cancelAlloc()
	}
	return ctx, cleanup
}

// loginAsFakeAdmin uses the --development auth bypass by
// navigating directly to /auth/fake/login?user=dev@example.com,
// which goes:
//
//	/auth/fake/login?user=... → 302 →
//	/auth/callback?code=...&state=...&provider=fake → set session cookie + 302 →
//	/
//
// We then navigate to / explicitly to land on the dashboard
// with the session cookie attached. Driving the dev-login form
// via chromedp.Submit hit a CDProtocol stale-context race on
// the mid-flight navigation, so we go directly to the
// producer-equivalent URL here. This still exercises the full
// redirect chain (it's just a manual GET instead of a form POST).
func (h *Harness) loginAsFakeAdmin(t *testing.T, ctx context.Context) {
	t.Helper()

	if err := chromedp.Run(ctx,
		// First navigate anywhere to establish the navigable
		// target on the chromedp context.
		chromedp.Navigate(h.BaseURL+"/login"),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("initial /login navigation: %v", err)
	}

	// Now submit by direct GET — equivalent to clicking the form's
	// submit button without fighting chromedp's form-submit context.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(
			h.BaseURL+"/auth/fake/login?user=dev%40example.com&state=dev-state",
			// dev-state matches the state pattern that the callback
			// handler accepts (it requires state, otherwise the
			// callback bails). We use a fixed-state value here because
			// the fake-provider flow doesn't cross-validate state
			// against an oauth_state cookie in --development mode.
		),
		// After this Navigate returns, the redirect chain has run
		// inside the browser. The browser carries the session
		// cookie forward on the next navigation.
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(1*time.Second),
	); err != nil {
		t.Fatalf("fake-login redirect chain: %v", err)
	}

	// Navigate to / with the session cookie set.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(h.BaseURL+"/"),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("post-login navigation to /: %v", err)
	}
}

// Strings import alias used by chromiumPath.

// TestMain builds the binary, starts the server, runs the suite,
// then cleans up. Each individual test gets its own chromium
// browser context (so cookies don't leak between tests) but
// shares the single server instance.
func TestMain(m *testing.M) {
	var err error
	harness, err = Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: harness start failed: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	harness.Stop()
	os.Exit(code)
}
