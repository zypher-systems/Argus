package api

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"argus/internal/store"
)

// mailSink is an SMTP server that accepts anything and keeps what it was
// sent, so a test can read the links an email carried.
type mailSink struct {
	t    *testing.T
	msgs chan string
}

// newMailSink starts a sink and points Argus's mail settings at it. The
// settings are cleared when the test ends: they outlive the truncated tables.
func newMailSink(t *testing.T, st *store.Store) *mailSink {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	m := &mailSink{t: t, msgs: make(chan string, 16)}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go m.serve(conn)
		}
	}()
	ctx := context.Background()
	if err := st.SetSMTPSettings(ctx, store.SMTPSettings{
		Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, From: "argus@example.com", TLSMode: "none",
	}, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.SetSMTPSettings(ctx, store.SMTPSettings{}, false) })
	return m
}

func (m *mailSink) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	say := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	say("220 sink")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
		case strings.HasPrefix(cmd, "DATA"):
			say("354 go on")
			var body strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				body.WriteString(l)
			}
			m.msgs <- body.String()
			say("250 kept")
		case strings.HasPrefix(cmd, "QUIT"):
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

// next is the next message the sink was sent, with the soft line breaks of
// its encoding taken out so a link can be searched for in one piece.
func (m *mailSink) next() string {
	m.t.Helper()
	select {
	case msg := <-m.msgs:
		return strings.NewReplacer("=\r\n", "", "=3D", "=").Replace(msg)
	case <-time.After(10 * time.Second):
		m.t.Fatal("no email arrived")
		return ""
	}
}
