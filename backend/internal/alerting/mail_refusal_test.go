package alerting

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

// pickySMTP accepts mail except to addresses containing "gone".
func pickySMTP(t *testing.T, st *store.Store) (delivered func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var mu sync.Mutex
	var got []string
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				r := bufio.NewReader(conn)
				say := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
				say("220 picky")
				var rcpts []string
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.TrimSpace(line)
					switch upper := strings.ToUpper(cmd); {
					case strings.HasPrefix(upper, "RCPT TO:"):
						if strings.Contains(cmd, "gone") {
							say("550 no such user")
							continue
						}
						rcpts = append(rcpts, cmd[len("RCPT TO:"):])
						say("250 ok")
					case strings.HasPrefix(upper, "DATA"):
						say("354 go on")
						for {
							l, err := r.ReadString('\n')
							if err != nil {
								return
							}
							if l == ".\r\n" {
								break
							}
						}
						mu.Lock()
						got = append(got, rcpts...)
						mu.Unlock()
						say("250 kept")
					case strings.HasPrefix(upper, "QUIT"):
						say("221 bye")
						return
					default:
						say("250 ok")
					}
				}
			}()
		}
	}()
	ctx := context.Background()
	if err := st.SetSMTPSettings(ctx, store.SMTPSettings{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, From: "argus@example.com", TLSMode: "none"}, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.SetSMTPSettings(context.Background(), store.SMTPSettings{}, false) })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

// One address the mail server refuses, a typo or someone who has left, must
// not cost everyone else on the channel their alert.
func TestOneRefusedRecipientDoesNotLoseTheEmail(t *testing.T) {
	f := newQueueFixture(t)
	delivered := pickySMTP(t, f.st)
	mail := Email{Subject: "s", Text: "t", HTML: "<p>t</p>"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := f.d.mailer.SendEmail(ctx, mail, []string{"ops@example.com", "gone@example.com", "lead@example.com"}); err != nil {
		t.Fatalf("two of three recipients were accepted: %v", err)
	}
	if got := strings.Join(delivered(), " "); !strings.Contains(got, "ops@example.com") || !strings.Contains(got, "lead@example.com") || strings.Contains(got, "gone") {
		t.Fatalf("delivered to %q", got)
	}
	// Nobody accepted is still a failure, so the alert is retried.
	if err := f.d.mailer.SendEmail(ctx, mail, []string{"gone@example.com"}); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("every recipient refused: %v", err)
	}
}
