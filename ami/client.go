package ami

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Message represents an AMI message packet
type Message struct {
	Headers map[string]string
	Lines   []string
}

// Client represents an AMI connection
type Client struct {
	address  string
	username string
	secret   string

	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex

	actionSeq uint64
}

func NewClient(address, username, secret string) *Client {
	return &Client{
		address:  address,
		username: username,
		secret:   secret,
	}
}

func (c *Client) SetCredentials(user, secret string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.username = user
	c.secret = secret
}

// Connect establishes the connection and logs in with Events: off
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}

	conn, err := net.DialTimeout("tcp", c.address, 4*time.Second)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}

	c.conn = conn
	c.reader = bufio.NewReader(conn)

	_ = c.conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	banner, err := c.reader.ReadString('\n')
	if err != nil {
		c.conn.Close()
		c.conn = nil
		return fmt.Errorf("failed to read banner: %w", err)
	}

	if !strings.Contains(banner, "Asterisk Call Manager") {
		c.conn.Close()
		c.conn = nil
		return fmt.Errorf("invalid AMI banner: %s", banner)
	}

	actionID := c.nextActionID()
	loginMsg := fmt.Sprintf("Action: Login\r\nUsername: %s\r\nSecret: %s\r\nEvents: off\r\nActionID: %s\r\n\r\n",
		c.username, c.secret, actionID)

	_ = c.conn.SetWriteDeadline(time.Now().Add(4 * time.Second))
	if _, err := c.conn.Write([]byte(loginMsg)); err != nil {
		c.conn.Close()
		c.conn = nil
		return fmt.Errorf("failed to send login: %w", err)
	}

	resp, err := c.readMessage()
	if err != nil {
		c.conn.Close()
		c.conn = nil
		return fmt.Errorf("failed to read login response: %w", err)
	}

	if !strings.EqualFold(resp.Headers["response"], "success") {
		c.conn.Close()
		c.conn = nil
		return fmt.Errorf("authentication failed: %s", resp.Headers["message"])
	}

	return nil
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		_, _ = c.conn.Write([]byte("Action: Logoff\r\n\r\n"))
		_ = c.conn.Close()
		c.conn = nil
	}
}

func (c *Client) nextActionID() string {
	val := atomic.AddUint64(&c.actionSeq, 1)
	return fmt.Sprintf("act-%d-%d", time.Now().Unix(), val)
}

// SendAction sends a standard AMI action and returns the Response packet
func (c *Client) SendAction(action string, headers map[string]string) (*Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil, fmt.Errorf("no active AMI connection")
	}

	actionID := c.nextActionID()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Action: %s\r\nActionID: %s\r\n", action, actionID))
	for k, v := range headers {
		sb.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	sb.WriteString("\r\n")

	_ = c.conn.SetWriteDeadline(time.Now().Add(4 * time.Second))
	if _, err := c.conn.Write([]byte(sb.String())); err != nil {
		c.conn.Close()
		c.conn = nil
		return nil, fmt.Errorf("write error: %w", err)
	}

	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(4 * time.Second))
		msg, err := c.readMessage()
		if err != nil {
			c.conn.Close()
			c.conn = nil
			return nil, err
		}
		if msg.Headers["actionid"] == actionID && msg.Headers["response"] != "" {
			return msg, nil
		}
	}
}

// SendActionWithEvents sends an action and collects related event packets until completeEvent is encountered
func (c *Client) SendActionWithEvents(action string, headers map[string]string, completeEvents ...string) ([]Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil, fmt.Errorf("no active AMI connection")
	}

	actionID := c.nextActionID()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Action: %s\r\nActionID: %s\r\n", action, actionID))
	for k, v := range headers {
		sb.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	sb.WriteString("\r\n")

	_ = c.conn.SetWriteDeadline(time.Now().Add(4 * time.Second))
	if _, err := c.conn.Write([]byte(sb.String())); err != nil {
		c.conn.Close()
		c.conn = nil
		return nil, fmt.Errorf("write error: %w", err)
	}

	var events []Message
	timeout := time.Now().Add(8 * time.Second)

	for {
		if time.Now().After(timeout) {
			return events, nil
		}

		_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		msg, err := c.readMessage()
		if err != nil {
			if len(events) > 0 {
				return events, nil
			}
			c.conn.Close()
			c.conn = nil
			return nil, err
		}

		if msg.Headers["actionid"] == actionID {
			if strings.EqualFold(msg.Headers["response"], "error") {
				return nil, fmt.Errorf("AMI error: %s", msg.Headers["message"])
			}

			currEvent := msg.Headers["event"]
			for _, ce := range completeEvents {
				if strings.EqualFold(currEvent, ce) {
					return events, nil
				}
			}

			if currEvent != "" {
				events = append(events, *msg)
			}
		}
	}
}

// Command runs an Asterisk CLI command via AMI Command
func (c *Client) Command(cmd string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil, fmt.Errorf("no active AMI connection")
	}

	actionID := c.nextActionID()
	req := fmt.Sprintf("Action: Command\r\nCommand: %s\r\nActionID: %s\r\n\r\n", cmd, actionID)

	_ = c.conn.SetWriteDeadline(time.Now().Add(4 * time.Second))
	if _, err := c.conn.Write([]byte(req)); err != nil {
		c.conn.Close()
		c.conn = nil
		return nil, fmt.Errorf("write error: %w", err)
	}

	var output []string
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(4 * time.Second))
		msg, err := c.readMessage()
		if err != nil {
			if len(output) > 0 {
				return output, nil
			}
			c.conn.Close()
			c.conn = nil
			return nil, err
		}
		if msg.Headers["actionid"] == actionID {
			for _, l := range msg.Lines {
				if strings.HasPrefix(strings.ToLower(l), "output:") {
					trimmed := strings.TrimSpace(l[7:])
					if trimmed != "" && !strings.Contains(trimmed, "--END COMMAND--") {
						output = append(output, trimmed)
					}
				}
			}
			return output, nil
		}
	}
}

func (c *Client) readMessage() (*Message, error) {
	msg := &Message{
		Headers: make(map[string]string),
		Lines:   make([]string, 0),
	}

	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(msg.Headers) > 0 || len(msg.Lines) > 0 {
				return msg, nil
			}
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			k := strings.ToLower(strings.TrimSpace(parts[0]))
			v := strings.TrimSpace(parts[1])
			if _, exists := msg.Headers[k]; !exists {
				msg.Headers[k] = v
			}
		}
		msg.Lines = append(msg.Lines, line)
	}
}
