package ami

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// StreamClient represents a dedicated connection that listens to AMI events
type StreamClient struct {
	address  string
	username string
	secret   string

	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
	closed bool
}

func NewStreamClient(address, username, secret string) *StreamClient {
	return &StreamClient{
		address:  address,
		username: username,
		secret:   secret,
	}
}

func (s *StreamClient) ConnectAndListen(eventHandler func(event string, headers map[string]string)) error {
	s.mu.Lock()
	if s.conn != nil {
		_ = s.conn.Close()
	}
	s.closed = false

	conn, err := net.DialTimeout("tcp", s.address, 4*time.Second)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("connection failed: %w", err)
	}

	s.conn = conn
	s.reader = bufio.NewReader(conn)
	s.mu.Unlock()

	// Read banner
	_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	banner, err := s.reader.ReadString('\n')
	if err != nil {
		s.Close()
		return fmt.Errorf("failed to read banner: %w", err)
	}
	if !strings.Contains(banner, "Asterisk Call Manager") {
		s.Close()
		return fmt.Errorf("invalid banner: %s", banner)
	}

	// Login with Events: on
	loginMsg := fmt.Sprintf("Action: Login\r\nUsername: %s\r\nSecret: %s\r\nEvents: on\r\n\r\n", s.username, s.secret)
	_ = conn.SetWriteDeadline(time.Now().Add(4 * time.Second))
	if _, err := conn.Write([]byte(loginMsg)); err != nil {
		s.Close()
		return fmt.Errorf("login failed: %w", err)
	}

	// Read login response
	_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			s.Close()
			return fmt.Errorf("login response failed: %w", err)
		}
		if line == "\r\n" {
			break
		}
	}

	// Clear deadlines for continuous streaming
	_ = conn.SetDeadline(time.Time{})

	// Continuous event loop
	headers := make(map[string]string)
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			s.Close()
			return err
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if evName, ok := headers["event"]; ok && evName != "" {
				eventHandler(evName, headers)
			}
			headers = make(map[string]string)
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			k := strings.ToLower(strings.TrimSpace(parts[0]))
			v := strings.TrimSpace(parts[1])
			headers[k] = v
		}
	}
}

func (s *StreamClient) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed && s.conn != nil {
		s.closed = true
		_ = s.conn.Close()
		s.conn = nil
	}
}
