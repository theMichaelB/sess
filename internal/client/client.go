package client

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
	"github.com/theMichaelB/sess/internal/protocol"
)

const (
	connectTimeout = 5 * time.Second
	bufferSize     = 4096
)

type Winsize struct {
	Rows uint16
	Cols uint16
}

type Client struct {
	sessionNum   string
	socketPath   string
	conn         net.Conn
	rawMode      *protocol.RawMode
	oldTermState *term.State
	winSize      *Winsize
	disableCtrlX bool
	done         chan struct{}
	doneOnce     sync.Once
	wg           sync.WaitGroup
}

func New(sessionNum, socketPath string, disableCtrlX bool) *Client {
	return &Client{
		sessionNum:   sessionNum,
		socketPath:   socketPath,
		disableCtrlX: disableCtrlX,
		done:         make(chan struct{}),
	}
}

func debugf(format string, args ...interface{}) {
	if os.Getenv("SESS_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "[sess-client] "+format+"\n", args...)
	}
}

func (c *Client) Attach() error {
	conn, err := net.DialTimeout("unix", c.socketPath, connectTimeout)
	if err != nil {
		return fmt.Errorf("failed to connect to session: %w", err)
	}
	c.conn = conn
	c.rawMode = protocol.NewRawMode(conn)

	buffer := make([]byte, 256)
	conn.SetReadDeadline(time.Now().Add(connectTimeout))
	n, err := conn.Read(buffer)
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to read initial response: %w", err)
	}

	response := string(buffer[:n])
	if response != "READY\n" {
		conn.Close()
		return fmt.Errorf("unexpected response: %s", response)
	}

	if err := c.setupTerminal(); err != nil {
		conn.Close()
		return fmt.Errorf("failed to setup terminal: %w", err)
	}

	// Send initial terminal size to the daemon so the PTY matches
	// our current window width/height immediately on attach.
	c.handleResize()

	c.setupSignalHandlers()
	c.run()

	return nil
}

func (c *Client) setupTerminal() error {
	// Check if stdin is a terminal
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("stdin is not a terminal")
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	c.oldTermState = oldState

	// GetSize returns width, height
	width, height, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		term.Restore(int(os.Stdin.Fd()), oldState)
		return err
	}
	c.winSize = &Winsize{Rows: uint16(height), Cols: uint16(width)}

	// Make stdin non-blocking so signal-triggered detach is immediate
	// (otherwise readFromStdin could block until the next keystroke).
	_ = unix.SetNonblock(int(os.Stdin.Fd()), true)

	return nil
}

func (c *Client) restoreTerminal() {
	if c.oldTermState != nil {
		term.Restore(int(os.Stdin.Fd()), c.oldTermState)
	}
	// Restore blocking mode on stdin
	_ = unix.SetNonblock(int(os.Stdin.Fd()), false)
}

func (c *Client) setupSignalHandlers() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGWINCH, syscall.SIGUSR1)

	go func() {
		for {
			select {
			case sig := <-sigChan:
				switch sig {
				case syscall.SIGINT, syscall.SIGTERM:
					debugf("got signal %v -> closing", sig)
					c.closeDone()
					return
				case syscall.SIGWINCH:
					c.handleResize()
				case syscall.SIGUSR1:
					debugf("got SIGUSR1 -> detach")
					c.detach()
					return
				}
			case <-c.done:
				return
			}
		}
	}()
}

func (c *Client) handleResize() {
	// GetSize returns width, height
	width, height, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return
	}
	c.winSize = &Winsize{Rows: uint16(height), Cols: uint16(width)}
	// Notify daemon of resize
	msg := fmt.Sprintf("RESIZE %d %d\n", height, width)
	debugf("sending resize rows=%d cols=%d", height, width)
	_ = c.rawMode.Write([]byte(msg))
}

func (c *Client) run() {
	fmt.Printf("Attaching to session %s\r\n", c.sessionNum)

	c.wg.Add(2)
	go c.readFromSession()
	go c.readFromStdin()

	c.wg.Wait()
	c.cleanup()
}

func (c *Client) readFromSession() {
	defer c.wg.Done()

	for {
		select {
		case <-c.done:
			return
		default:
			data, err := c.rawMode.Read()
			if err != nil {
				debugf("readFromSession error: %v", err)
				c.closeDone()
				return
			}

			if data != nil && len(data) > 0 {
				os.Stdout.Write(data)
			}
		}
	}
}

func (c *Client) readFromStdin() {
	defer c.wg.Done()

	buffer := make([]byte, 1024)
	for {
		// Non-blocking read so we can notice c.done promptly
		select {
		case <-c.done:
			return
		default:
		}

		n, err := os.Stdin.Read(buffer)
		if err != nil {
			// EAGAIN/EWOULDBLOCK: no input ready; check done and retry
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			// EINTR: interrupted by signal (e.g., SIGWINCH); retry read
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			// EOF: no further stdin; stay attached and keep reading from session
			if errors.Is(err, io.EOF) {
				debugf("stdin EOF; staying attached")
				time.Sleep(20 * time.Millisecond)
				continue
			}
			debugf("readFromStdin error: %v", err)
			c.closeDone()
			return
		}

		if n > 0 {
			// Ctrl-X (0x18) to detach if pressed alone (unless disabled)
			if !c.disableCtrlX && n == 1 && buffer[0] == 0x18 {
				c.detach()
				return
			}
			if err := c.rawMode.Write(buffer[:n]); err != nil {
				c.closeDone()
				return
			}
		}
	}
}

func (c *Client) detach() {
	c.rawMode.Write([]byte("DISCONNECT\n"))
	c.closeDone()
}

func (c *Client) cleanup() {
	c.restoreTerminal()

	if c.rawMode != nil {
		c.rawMode.Close()
	}

	fmt.Printf("\r\nDetached from session %s\r\n", c.sessionNum)
}

func (c *Client) SendPing() error {
	_, err := c.conn.Write([]byte("PING\n"))
	return err
}

func (c *Client) closeDone() {
	c.doneOnce.Do(func() {
		close(c.done)
	})
}
