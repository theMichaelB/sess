package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/term"
	"github.com/theMichaelB/sess/internal/client"
	"github.com/theMichaelB/sess/internal/daemon"
	"github.com/theMichaelB/sess/internal/session"
	"strconv"
)

// version follows Semantic Versioning (https://semver.org/)
// Overridden at build time via: -ldflags "-X main.version=vX.Y.Z"
var version = "v1.0.0"

func runDaemon(number, socketPath, metaPath, shell string, rows, cols int) {
	d := daemon.New(number, socketPath, metaPath)
	if err := d.Start(shell, rows, cols); err != nil {
		// Surface daemon startup errors to help diagnose issues during attach
		fmt.Fprintf(os.Stderr, "daemon failed to start: %v\n", err)
		os.Exit(1)
	}
}

func main() {
	// Check for daemon mode first
	if len(os.Args) >= 6 && os.Args[1] == "--daemon" {
		rows, cols := 0, 0
		if len(os.Args) >= 8 {
			if v, err := strconv.Atoi(os.Args[6]); err == nil {
				rows = v
			}
			if v, err := strconv.Atoi(os.Args[7]); err == nil {
				cols = v
			}
		}
		runDaemon(os.Args[2], os.Args[3], os.Args[4], os.Args[5], rows, cols)
		return
	}

	var (
		attachFlag       = flag.String("a", "", "Attach to session by number")
		attachCreateFlag = flag.String("A", "", "Attach to session or create if not exists")
		detachFlag       = flag.Bool("x", false, "Detach from current session")
		killFlag         = flag.String("k", "", "Kill session (current if no number given)")
		killAllFlag      = flag.Bool("K", false, "Kill all sessions")
		disableCtrlXFlag = flag.Bool("C", false, "Disable Ctrl-X to detach")
		disableCtrlXLong = flag.Bool("no-ctrlx", false, "Disable Ctrl-X to detach")
		versionFlag      = flag.Bool("v", false, "Show version")
		versionLongFlag  = flag.Bool("version", false, "Show version")
		helpFlag         = flag.Bool("h", false, "Show help")
		longHelpFlag     = flag.Bool("help", false, "Show help")
	)

	flag.Usage = showUsage
	flag.Parse()

	if *versionFlag || *versionLongFlag {
		fmt.Printf("sess %s\n", version)
		return
	}

	if *helpFlag || *longHelpFlag {
		showUsage()
		return
	}

	manager, err := session.NewManager()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	args := flag.Args()

	disableCtrlX := (*disableCtrlXFlag || *disableCtrlXLong)

	switch {
	case *attachFlag != "":
		handleAttach(manager, *attachFlag, disableCtrlX)
	case *attachCreateFlag != "":
		handleAttachCreate(manager, *attachCreateFlag, disableCtrlX)
	case *detachFlag:
		handleDetach(manager)
	case *killAllFlag:
		handleKillAll(manager)
	case flag.NFlag() > 0 && (flag.Arg(0) == "-k" || *killFlag != ""):
		handleKill(manager, *killFlag)
	case len(args) > 0 && args[0] == "ls":
		handleList(manager)
	default:
		handleCreate(manager, disableCtrlX)
	}
}

func showUsage() {
	fmt.Printf(`sess %s - minimal session persistence tool

Usage:
  sess              Create new session
  sess ls           List all sessions
  sess -a <num>     Attach to session
  sess -A <num>     Attach or create session
  sess -x           Detach from current session
  sess -C           Disable Ctrl-X detach (for this attach)
  sess --no-ctrlx   Same as -C
  sess -K           Kill all sessions
  sess -k [num]     Kill session (current if no number)
  sess -v, --version Show version
  sess -h, --help   Show this help

Sessions are numbered sequentially (001, 002, etc).
You can use either 1 or 001 format for session numbers.

Flags:
  -a <num>           Attach to session
  -A <num>           Attach or create session
  -x                 Detach from current session
  -C, --no-ctrlx     Disable Ctrl-X detach for this attach
  -k [num]           Kill session by number (or current)
  -K                 Kill all sessions
  -v, --version      Show version
  -h, --help         Show help
`, version)
}

func handleCreate(manager *session.Manager, disableCtrlX bool) {
	if manager.IsInSession() {
		fmt.Fprintf(os.Stderr, "Error: Cannot create session from within existing session %s\n", manager.CurrentSessionNumber())
		os.Exit(1)
	}

	number, err := manager.NextSessionNumber()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	socketPath := manager.GetSocketPath(number)
	metaPath := manager.GetMetaPath(number)

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	// Determine initial terminal size to pass to daemon
	initRows, initCols := 0, 0
	if term.IsTerminal(int(os.Stdin.Fd())) {
		if w, h, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
			initRows, initCols = h, w
		}
	}
	// Fork daemon process (pass initial rows/cols)
	cmd := exec.Command(os.Args[0], "--daemon", number, socketPath, metaPath, shell, fmt.Sprint(initRows), fmt.Sprint(initCols))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to fork daemon: %v\n", err)
		os.Exit(1)
	}

	// Wait for daemon to be ready
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if err := manager.SetCurrentSession(number); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to set current session: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created session %s at %s\n", number, time.Now().Format("2006-01-02 15:04"))

	c := client.New(number, socketPath, disableCtrlX)
	if err := c.Attach(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to attach to new session: %v\n", err)
		manager.ClearCurrentSession()
		os.Exit(1)
	}

	manager.ClearCurrentSession()
}

func handleList(manager *session.Manager) {
	sessions, err := manager.ListSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(sessions) == 0 {
		fmt.Println("No active sessions")
		return
	}

	// Determine current attachment:
	// - If running inside a session, use SESS_NUM
	// - Otherwise, read from the current-session file if present
	current := ""
	if manager.IsInSession() {
		current = manager.CurrentSessionNumber()
	} else if num, _ := manager.GetCurrentSession(); num != "" {
		current = num
	}

	fmt.Printf("SESSION  STATUS    CREATED              PID     CMD\n")
	for _, sess := range sessions {
		status := "detached"
		indicator := "  "
		if sess.Number == current {
			status = "attached"
			indicator = "* "
		}
		fmt.Printf("%s%3s   %-9s %-20s %-7d %s\n",
			indicator,
			sess.Number,
			status,
			sess.CreatedAt.Format("2006-01-02 15:04"),
			sess.PID,
			sess.Command,
		)
	}

	if current != "" {
		fmt.Printf("\n* indicates current session (%s)\n", current)
	}
}

func handleAttach(manager *session.Manager, number string, disableCtrlX bool) {
	number = manager.NormalizeSessionNumber(number)

	if manager.IsInSession() && manager.CurrentSessionNumber() == number {
		fmt.Fprintf(os.Stderr, "Error: Already attached to session %s\n", number)
		os.Exit(1)
	}

	sess, err := manager.GetSession(number)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	socketPath := manager.GetSocketPath(number)

	if err := manager.SetCurrentSession(number); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to set current session: %v\n", err)
		os.Exit(1)
	}

	c := client.New(sess.Number, socketPath, disableCtrlX)
	if err := c.Attach(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		manager.ClearCurrentSession()
		os.Exit(1)
	}

	manager.ClearCurrentSession()
}

func handleAttachCreate(manager *session.Manager, number string, disableCtrlX bool) {
	number = manager.NormalizeSessionNumber(number)

	if manager.IsInSession() {
		fmt.Fprintf(os.Stderr, "Error: Cannot create session from within existing session %s\n", manager.CurrentSessionNumber())
		os.Exit(1)
	}

	if _, err := manager.GetSession(number); err == nil {
		handleAttach(manager, number, disableCtrlX)
		return
	}

	socketPath := manager.GetSocketPath(number)
	metaPath := manager.GetMetaPath(number)

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	// Determine initial terminal size to pass to daemon
	initRows, initCols := 0, 0
	if term.IsTerminal(int(os.Stdin.Fd())) {
		if w, h, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
			initRows, initCols = h, w
		}
	}
	// Fork daemon process (pass initial rows/cols)
	cmd := exec.Command(os.Args[0], "--daemon", number, socketPath, metaPath, shell, fmt.Sprint(initRows), fmt.Sprint(initCols))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to fork daemon: %v\n", err)
		os.Exit(1)
	}

	// Wait for daemon to be ready
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Do not write metadata here; the daemon writes authoritative metadata
	// once the PTY and child shell are started.

	if err := manager.SetCurrentSession(number); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to set current session: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created session %s at %s\n", number, time.Now().Format("2006-01-02 15:04"))

	c := client.New(number, socketPath, disableCtrlX)
	if err := c.Attach(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to attach to new session: %v\n", err)
		manager.ClearCurrentSession()
		os.Exit(1)
	}

	manager.ClearCurrentSession()
}

func handleDetach(manager *session.Manager) {
	// Detach the active client by signaling the client PID recorded
	// in the current-session file, regardless of where this command runs.
	info, err := manager.GetCurrentSessionInfo()
	if err != nil || info == nil || info.Number == "" || info.PID == 0 {
		fmt.Fprintf(os.Stderr, "Error: Not attached to any session\n")
		os.Exit(1)
	}
	if err := syscall.Kill(info.PID, syscall.SIGUSR1); err != nil {
		if err == syscall.ESRCH {
			// Stale marker; clear and report
			_ = manager.ClearCurrentSession()
			fmt.Fprintf(os.Stderr, "Error: Not attached to any session\n")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Error: Failed to detach: %v\n", err)
		os.Exit(1)
	}
}

func handleKill(manager *session.Manager, number string) {
	if number == "" {
		if !manager.IsInSession() {
			fmt.Fprintf(os.Stderr, "Error: Not attached to any session\n")
			os.Exit(1)
		}
		number = manager.CurrentSessionNumber()
	} else {
		number = manager.NormalizeSessionNumber(number)
	}

	if err := manager.KillSession(number); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Killed session %s\n", number)
}

func handleKillAll(manager *session.Manager) {
	sessions, err := manager.ListSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(sessions) == 0 {
		fmt.Println("No active sessions")
		return
	}
	for _, s := range sessions {
		if err := manager.KillSession(s.Number); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			// continue with others
			continue
		}
		fmt.Printf("Killed session %s\n", s.Number)
	}
}
