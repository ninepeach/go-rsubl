package rsub

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)


type Error string

func (err Error) Error() string { return string(err) }

// conn is the low-level implementation of Conn
type Conn struct {
	// Shared
	mu      sync.Mutex
	err     error
	conn    net.Conn

	// Read
	readTimeout time.Duration
	br          *bufio.Reader

	// Write
	writeTimeout time.Duration
	bw           *bufio.Writer

}

func NewConn(host string, port int) (*Conn, error){
	netConn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", host, port))

	if err != nil {
		return nil, err
	}

	c := &Conn{
		conn:         netConn,
		bw:           bufio.NewWriter(netConn),
		br:           bufio.NewReader(netConn),
		readTimeout:  0 * time.Second,
		writeTimeout: 0 * time.Second,

	}
	return c, err
}

func (c *Conn) Close() error {
	c.mu.Lock()
	err := c.err
	if c.err == nil {
		c.err = errors.New("conn: closed")
		err = c.conn.Close()
	}
	c.mu.Unlock()
	return err
}

func (c *Conn) fatal(err error) error {
	c.mu.Lock()
	if c.err == nil {
		c.err = err
		// Close connection to force errors on subsequent calls and to unblock
		// other reader or writer.
		c.conn.Close()
	}
	c.mu.Unlock()
	return err
}

func (c *Conn) Err() error {
	c.mu.Lock()
	err := c.err
	c.mu.Unlock()
	return err
}

func (c *Conn) writeString(s string) error {
	_, err := c.bw.WriteString(s)
	return err
}

func (c *Conn) writeBytes(p []byte) error {
	_, err := c.bw.Write(p)
	return err
}

func (c *Conn) SendString(data string) error {
	if c.writeTimeout != 0 {
		c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	}
	if err := c.writeString(data); err != nil {
		return c.fatal(err)
	}
	return nil
}

func (c *Conn) SendBytes(data []byte) error {
	if c.writeTimeout != 0 {
		c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	}
	if err := c.writeBytes(data); err != nil {
		return c.fatal(err)
	}
	return nil
}

func (c *Conn) SendFile(src *bufio.Reader) error {
	if c.writeTimeout != 0 {
		c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	}
	if _, err := io.Copy(c.bw, src); err != nil {
		return c.fatal(err)
	}
	return nil
}

func (c *Conn) Flush() error {

	if c.writeTimeout != 0 {
		c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	}
	if err := c.bw.Flush(); err != nil {
		return c.fatal(err)
	}
	return nil
}

type protocolError string

func (pe protocolError) Error() string {
	return fmt.Sprintf("rsub: %s (possible server error or unsupported concurrent read by application)", string(pe))
}

func (c *Conn) GetReader() (*bufio.Reader) {
	return c.br
}

// readLine reads a line of input
func (c *Conn) readLine() ([]byte, error) {
	p, err := c.br.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		// The line does not fit in the bufio.Reader's buffer. Fall back to
		// allocating a buffer for the line.
		buf := append([]byte{}, p...)
		for err == bufio.ErrBufferFull {
			p, err = c.br.ReadSlice('\n')
			buf = append(buf, p...)
		}
		p = buf
	}
	if err != nil {
		return nil, err
	}
	i := len(p) - 1

	if i < 0 || p[i] != '\n' {
		return nil, protocolError("bad response line terminator")
	}
	return p[:i], nil
}

func (c *Conn) readReply() ([]byte, error) {
	line, err := c.readLine()
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, nil
	}

	return line, nil
}

func (c *Conn) Receive() (interface{}, error) {
	return c.ReceiveWithTimeout(c.readTimeout)
}

func (c *Conn) ReceiveWithTimeout(timeout time.Duration) (reply interface{}, err error) {
	var deadline time.Time
	if timeout != 0 {
		deadline = time.Now().Add(timeout)
	}
	c.conn.SetReadDeadline(deadline)

	if reply, err = c.readReply(); err != nil {
		return nil, c.fatal(err)
	}

	if err, ok := reply.(Error); ok {
		return nil, err
	}
	return
}