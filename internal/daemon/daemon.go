package daemon

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "net"
    "os"
    "os/exec"
    "os/signal"
    "path/filepath"
    "strconv"
    "strings"
    "sync"
    "syscall"
    "time"

    ptylib "github.com/creack/pty"
    "golang.org/x/sys/unix"
)

const (
	connectionTimeout = 30 * time.Second
	readTimeout       = 100 * time.Millisecond
)

type Daemon struct {
	sessionNum  string
	socketPath  string
	metaPath    string
	cmd         *exec.Cmd
	ptyMaster   *os.File
	ptySlave    *os.File
	listener    net.Listener
	clients     map[net.Conn]*client
	clientMutex sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

type client struct {
	conn         net.Conn
	lastActivity time.Time
}

func debugf(format string, args ...interface{}) {
	if os.Getenv("SESS_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "[sess-daemon] "+format+"\n", args...)
	}
}

type Metadata struct {
	SessionNum string    `json:"session_num"`
	CreatedAt  time.Time `json:"created_at"`
	PID        int       `json:"pid"`
	Command    string    `json:"command"`
}

func New(sessionNum, socketPath, metaPath string) *Daemon {
	ctx, cancel := context.WithCancel(context.Background())
	return &Daemon{
		sessionNum: sessionNum,
		socketPath: socketPath,
		metaPath:   metaPath,
		clients:    make(map[net.Conn]*client),
		ctx:        ctx,
		cancel:     cancel,
	}
}

func (d *Daemon) Start(shell string, initialRows, initialCols int) error {
	ptmx, pts, err := d.openPTY()
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon: failed to open PTY: %v\n", err)
		return fmt.Errorf("failed to open PTY: %w", err)
	}
	d.ptyMaster = ptmx
	d.ptySlave = pts

	// Apply initial size if provided
	if initialRows > 0 && initialCols > 0 {
		_ = ptylib.Setsize(pts, &ptylib.Winsize{Rows: uint16(initialRows), Cols: uint16(initialCols)})
	}

	if err := d.startShell(shell, pts); err != nil {
		ptmx.Close()
		pts.Close()
		fmt.Fprintf(os.Stderr, "daemon: failed to start shell: %v\n", err)
		return fmt.Errorf("failed to start shell: %w", err)
	}

	if err := d.writeMetadata(shell); err != nil {
		d.cleanup()
		fmt.Fprintf(os.Stderr, "daemon: failed to write metadata: %v\n", err)
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	if err := d.startListener(); err != nil {
		d.cleanup()
		fmt.Fprintf(os.Stderr, "daemon: failed to start listener: %v\n", err)
		return fmt.Errorf("failed to start listener: %w", err)
	}

	// Now detach from terminal
	if err := d.detachFromTerminal(); err != nil {
		d.cleanup()
		fmt.Fprintf(os.Stderr, "daemon: failed to detach: %v\n", err)
		return fmt.Errorf("failed to detach: %w", err)
	}

	d.setupSignalHandlers()
	d.run()

	return nil
}

func (d *Daemon) detachFromTerminal() error {
	// Try to create new session, ignore error if already session leader
	syscall.Setsid()

	devNull, err := os.Open("/dev/null")
	if err != nil {
		return err
	}
	defer devNull.Close()

	if err := unix.Dup2(int(devNull.Fd()), 0); err != nil {
		return err
	}
	if err := unix.Dup2(int(devNull.Fd()), 1); err != nil {
		return err
	}
	if err := unix.Dup2(int(devNull.Fd()), 2); err != nil {
		return err
	}

	return nil
}

func (d *Daemon) openPTY() (*os.File, *os.File, error) {
	// Use a robust cross-distro PTY helper
	ptmx, pts, err := ptylib.Open()
	if err != nil {
		return nil, nil, err
	}
	if err := setNonBlocking(ptmx); err != nil {
		ptmx.Close()
		pts.Close()
		return nil, nil, err
	}
	return ptmx, pts, nil
}

func (d *Daemon) startShell(shell string, pts *os.File) error {
	d.cmd = exec.Command(shell)
	d.cmd.Stdin = pts
	d.cmd.Stdout = pts
	d.cmd.Stderr = pts
	d.cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		// Use child's stdin (fd 0) as controlling TTY
		Ctty: 0,
	}
	d.cmd.Env = append(os.Environ(), fmt.Sprintf("SESS_NUM=%s", d.sessionNum))

	if err := d.cmd.Start(); err != nil {
		return err
	}

	return nil
}

func (d *Daemon) writeMetadata(shell string) error {
	meta := Metadata{
		SessionNum: d.sessionNum,
		CreatedAt:  time.Now(),
		PID:        d.cmd.Process.Pid,
		Command:    shell,
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := d.metaPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}

	return os.Rename(tmpPath, d.metaPath)
}

func (d *Daemon) startListener() error {
	os.Remove(d.socketPath)

	listener, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return err
	}

	if err := os.Chmod(d.socketPath, 0600); err != nil {
		listener.Close()
		return err
	}

	d.listener = listener
	return nil
}

func (d *Daemon) setupSignalHandlers() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGCHLD, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		for {
			select {
			case sig := <-sigChan:
				switch sig {
				case syscall.SIGCHLD:
					var status syscall.WaitStatus
					_, err := syscall.Wait4(d.cmd.Process.Pid, &status, syscall.WNOHANG, nil)
					if err == nil && (status.Exited() || status.Signaled()) {
						d.cancel()
					}
				case syscall.SIGTERM, syscall.SIGINT:
					d.cancel()
				}
			case <-d.ctx.Done():
				return
			}
		}
	}()
}

func (d *Daemon) run() {
	d.wg.Add(3)
	go d.acceptConnections()
	go d.handlePTY()
	go d.monitorClients()

	<-d.ctx.Done()
	d.cleanup()
	d.wg.Wait()
}

func (d *Daemon) acceptConnections() {
	defer d.wg.Done()

	for {
		select {
		case <-d.ctx.Done():
			return
		default:
			d.listener.(*net.UnixListener).SetDeadline(time.Now().Add(1 * time.Second))
			conn, err := d.listener.Accept()
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				continue
			}

			d.handleNewConnection(conn)
		}
	}
}

func (d *Daemon) handleNewConnection(conn net.Conn) {
	d.clientMutex.Lock()
	defer d.clientMutex.Unlock()

	if len(d.clients) > 0 {
		conn.Write([]byte("ERROR: Session already has an active connection\n"))
		conn.Close()
		return
	}

	// Do not toggle nonblocking on the net.Conn; deadlines are used instead.

	d.clients[conn] = &client{
		conn:         conn,
		lastActivity: time.Now(),
	}

	conn.Write([]byte("READY\n"))
	debugf("client connected; sent READY")

	// Start per-connection reader to minimize input latency
	go d.clientReadLoop(conn)
}

// clientReadLoop continuously reads from the client socket and forwards
// control/data to the PTY with low latency.
func (d *Daemon) clientReadLoop(conn net.Conn) {
	buffer := make([]byte, 4096)
	for {
		select {
		case <-d.ctx.Done():
			return
		default:
			conn.SetReadDeadline(time.Now().Add(readTimeout))
			n, err := conn.Read(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				d.removeClient(conn)
				return
			}
			if n > 0 {
				d.clientMutex.Lock()
				if c, ok := d.clients[conn]; ok {
					c.lastActivity = time.Now()
				}
				d.clientMutex.Unlock()

				s := string(buffer[:n])
				switch {
				case s == "DISCONNECT\n":
					d.removeClient(conn)
					return
				case s == "PING\n":
					conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
					conn.Write([]byte("PONG\n"))
				case strings.HasPrefix(s, "RESIZE "):
					var r, c int
					fields := strings.Fields(s)
					if len(fields) >= 3 {
						r, _ = strconv.Atoi(fields[1])
						c, _ = strconv.Atoi(fields[2])
						// Apply size using pty helper on slave/master
						if d.ptySlave != nil {
							_ = ptylib.Setsize(d.ptySlave, &ptylib.Winsize{Rows: uint16(r), Cols: uint16(c)})
						}
						if d.ptyMaster != nil {
							_ = ptylib.Setsize(d.ptyMaster, &ptylib.Winsize{Rows: uint16(r), Cols: uint16(c)})
						}
						// Ensure the shell is notified of the change
						if d.cmd != nil && d.cmd.Process != nil {
							_ = syscall.Kill(-d.cmd.Process.Pid, syscall.SIGWINCH)
						}
						// Best-effort verify via slave winsize
						if d.ptySlave != nil {
							if cur, err := unix.IoctlGetWinsize(int(d.ptySlave.Fd()), unix.TIOCGWINSZ); err == nil {
								debugf("applied resize: req=%dx%d, got=%dx%d", r, c, cur.Row, cur.Col)
							}
						}
					}
				default:
					d.ptyMaster.Write(buffer[:n])
				}
			}
		}
	}
}

func (d *Daemon) handlePTY() {
	defer d.wg.Done()

	buffer := make([]byte, 4096)
	for {
		select {
		case <-d.ctx.Done():
			return
		default:
			n, err := d.ptyMaster.Read(buffer)
			if err != nil {
				if errors.Is(err, syscall.EAGAIN) {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				return
			}

			if n > 0 {
				d.broadcastToClients(buffer[:n])
			}
		}
	}
}

func (d *Daemon) broadcastToClients(data []byte) {
	d.clientMutex.RLock()
	defer d.clientMutex.RUnlock()

	for conn := range d.clients {
		conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
		if _, err := conn.Write(data); err != nil {
			go d.removeClient(conn)
		}
	}
}

func (d *Daemon) monitorClients() {
	defer d.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.checkClientTimeouts()
		}
	}
}

func (d *Daemon) checkClientTimeouts() {
	d.clientMutex.Lock()
	defer d.clientMutex.Unlock()

	now := time.Now()
	for conn, client := range d.clients {
		if now.Sub(client.lastActivity) > connectionTimeout {
			go d.removeClient(conn)
		}
	}
}

func (d *Daemon) removeClient(conn net.Conn) {
	d.clientMutex.Lock()
	defer d.clientMutex.Unlock()

	if _, ok := d.clients[conn]; ok {
		conn.Close()
		delete(d.clients, conn)
	}
}

func (d *Daemon) cleanup() {
	d.clientMutex.Lock()
	for conn := range d.clients {
		conn.Close()
	}
	d.clients = make(map[net.Conn]*client)
	d.clientMutex.Unlock()

	if d.listener != nil {
		d.listener.Close()
	}

	if d.cmd != nil && d.cmd.Process != nil {
		d.cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(1 * time.Second)
		d.cmd.Process.Kill()
	}

	if d.ptyMaster != nil {
		d.ptyMaster.Close()
	}
	if d.ptySlave != nil {
		d.ptySlave.Close()
	}

	os.Remove(d.socketPath)
	os.Remove(d.metaPath)
	os.Remove(filepath.Join(filepath.Dir(d.metaPath), ".current_session"))
}

func setNonBlocking(file interface{}) error {
	var fd int
	switch f := file.(type) {
	case *os.File:
		fd = int(f.Fd())
	case net.Conn:
		if unixConn, ok := f.(*net.UnixConn); ok {
			file, _ := unixConn.File()
			fd = int(file.Fd())
			defer file.Close()
		} else {
			return fmt.Errorf("unsupported connection type")
		}
	default:
		return fmt.Errorf("unsupported file type")
	}

	return unix.SetNonblock(fd, true)
}
