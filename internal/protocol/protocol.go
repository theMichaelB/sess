package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	MsgConnect    = "CONNECT"
	MsgReady      = "READY"
	MsgData       = "DATA"
	MsgResize     = "RESIZE"
	MsgDisconnect = "DISCONNECT"
	MsgPing       = "PING"
	MsgPong       = "PONG"
	MsgError      = "ERROR"
)

type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type ResizePayload struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

type ErrorPayload struct {
	Message string `json:"message"`
}

type Connection struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
}

func NewConnection(conn net.Conn) *Connection {
	return &Connection{
		conn:   conn,
		reader: bufio.NewReader(conn),
		writer: bufio.NewWriter(conn),
	}
}

func (c *Connection) SendMessage(msgType string, payload interface{}) error {
	msg := Message{Type: msgType}

	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal payload: %w", err)
		}
		msg.Payload = data
	}

	encoder := json.NewEncoder(c.writer)
	if err := encoder.Encode(&msg); err != nil {
		return fmt.Errorf("failed to encode message: %w", err)
	}

	return c.writer.Flush()
}

func (c *Connection) ReadMessage() (*Message, error) {
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	decoder := json.NewDecoder(c.reader)
	var msg Message
	if err := decoder.Decode(&msg); err != nil {
		return nil, err
	}

	return &msg, nil
}

func (c *Connection) SendRaw(data []byte) error {
	c.conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
	_, err := c.conn.Write(data)
	return err
}

func (c *Connection) ReadRaw(buffer []byte) (int, error) {
	c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	return c.conn.Read(buffer)
}

func (c *Connection) Close() error {
	return c.conn.Close()
}

func (c *Connection) SetRaw() error {
	if f, ok := c.conn.(*net.UnixConn); ok {
		file, err := f.File()
		if err != nil {
			return err
		}
		defer file.Close()
	}
	return nil
}

type RawMode struct {
	conn   net.Conn
	buffer []byte
}

func NewRawMode(conn net.Conn) *RawMode {
	return &RawMode{
		conn:   conn,
		buffer: make([]byte, 4096),
	}
}

func (r *RawMode) Write(data []byte) error {
	r.conn.SetWriteDeadline(time.Now().Add(1 * time.Second))

	for len(data) > 0 {
		n, err := r.conn.Write(data)
		if err != nil {
			if err == io.ErrShortWrite {
				data = data[n:]
				continue
			}
			return err
		}
		data = data[n:]
	}

	return nil
}

func (r *RawMode) Read() ([]byte, error) {
	r.conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	n, err := r.conn.Read(r.buffer)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil, nil
		}
		return nil, err
	}

	return r.buffer[:n], nil
}

func (r *RawMode) Close() error {
	return r.conn.Close()
}
