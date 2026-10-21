package probe

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"argus/internal/store"
)

// selfSigned makes a certificate a fake mail server can present.
func selfSigned(t *testing.T, notAfter time.Time) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		DNSNames:     []string{"localhost", "127.0.0.1"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// mailServer runs a scripted plaintext server; handle gets the connection
// after the greeting has been written.
func mailServer(t *testing.T, greeting string, handle func(rw *bufio.ReadWriter, conn net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
				_, _ = rw.WriteString(greeting)
				_ = rw.Flush()
				if handle != nil {
					handle(rw, conn)
				} else {
					_, _ = rw.ReadString('\n')
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func mailMonitor(kind, target, mode string) store.Monitor {
	return store.Monitor{
		Type: kind, Target: target,
		Config: store.MonitorConfig{TimeoutSeconds: 5, TLSMode: mode, InsecureSkipVerify: true},
	}
}

func TestCheckMailGreetings(t *testing.T) {
	cases := []struct {
		kind, greeting string
		ok             bool
	}{
		{"smtp", "220 mail.example.com ESMTP Postfix\r\n", true},
		{"smtp", "554 no service here\r\n", false},
		{"imap", "* OK [CAPABILITY IMAP4rev1] Dovecot ready\r\n", true},
		{"imap", "* BAD go away\r\n", false},
		{"pop3", "+OK POP3 ready\r\n", true},
		{"pop3", "-ERR locked\r\n", false},
	}
	for _, c := range cases {
		addr := mailServer(t, c.greeting, nil)
		out, err := checkMail(context.Background(), mailMonitor(c.kind, addr, MailTLSNone))
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.kind, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: expected the bad greeting to fail", c.kind)
		}
		if out.Evidence.BodySnippet == "" {
			t.Errorf("%s: the greeting should be kept as evidence", c.kind)
		}
	}
}

func TestCheckMailKeyword(t *testing.T) {
	addr := mailServer(t, "220 mail.example.com ESMTP Postfix\r\n", nil)
	m := mailMonitor("smtp", addr, MailTLSNone)
	m.Config.Keyword = "postfix"
	if _, err := checkMail(context.Background(), m); err != nil {
		t.Fatalf("keyword should match case-insensitively: %v", err)
	}
	m.Config.Keyword = "exchange"
	if _, err := checkMail(context.Background(), m); err == nil {
		t.Fatal("a missing keyword must fail")
	}
	m.Config.Keyword, m.Config.KeywordInvert = "postfix", true
	if _, err := checkMail(context.Background(), m); err == nil {
		t.Fatal("inverted keyword must fail when present")
	}
}

func TestCheckMailImplicitTLSReadsCertExpiry(t *testing.T) {
	notAfter := time.Now().Add(42 * 24 * time.Hour).UTC().Truncate(time.Second)
	cert := selfSigned(t, notAfter)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = conn.Write([]byte("* OK IMAP ready\r\n"))
				_, _ = bufio.NewReader(conn).ReadString('\n')
			}()
		}
	}()

	out, err := checkMail(context.Background(), mailMonitor("imap", ln.Addr().String(), MailTLSImplicit))
	if err != nil {
		t.Fatal(err)
	}
	if out.TLSExpiresAt == nil || !out.TLSExpiresAt.Equal(notAfter) {
		t.Fatalf("cert expiry %v, want %v", out.TLSExpiresAt, notAfter)
	}
}

func TestCheckMailStartTLSUpgrade(t *testing.T) {
	notAfter := time.Now().Add(20 * 24 * time.Hour).UTC().Truncate(time.Second)
	cert := selfSigned(t, notAfter)
	addr := mailServer(t, "220 mail.example.com ESMTP\r\n", func(rw *bufio.ReadWriter, conn net.Conn) {
		for {
			line, err := rw.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				_, _ = rw.WriteString("250-mail.example.com\r\n250 STARTTLS\r\n")
				_ = rw.Flush()
			case strings.HasPrefix(line, "STARTTLS"):
				_, _ = rw.WriteString("220 go ahead\r\n")
				_ = rw.Flush()
				srv := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}})
				if err := srv.Handshake(); err != nil {
					return
				}
				_, _ = bufio.NewReader(srv).ReadString('\n')
				return
			default:
				return
			}
		}
	})

	out, err := checkMail(context.Background(), mailMonitor("smtp", addr, MailTLSStartTLS))
	if err != nil {
		t.Fatal(err)
	}
	if out.TLSExpiresAt == nil || !out.TLSExpiresAt.Equal(notAfter) {
		t.Fatalf("STARTTLS should capture the certificate: %v", out.TLSExpiresAt)
	}
}

func TestCheckMailStartTLSRefused(t *testing.T) {
	addr := mailServer(t, "220 mail.example.com ESMTP\r\n", func(rw *bufio.ReadWriter, _ net.Conn) {
		for {
			line, err := rw.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "EHLO") {
				_, _ = rw.WriteString("250 mail.example.com\r\n")
				_ = rw.Flush()
				continue
			}
			if strings.HasPrefix(line, "STARTTLS") {
				_, _ = rw.WriteString("454 TLS not available\r\n")
				_ = rw.Flush()
				return
			}
		}
	})
	_, err := checkMail(context.Background(), mailMonitor("smtp", addr, MailTLSStartTLS))
	if err == nil || !strings.Contains(err.Error(), "refused STARTTLS") {
		t.Fatalf("a server without TLS must fail clearly, got %v", err)
	}
}

func TestMailDefaultsAndTypes(t *testing.T) {
	if !IsMailType("SMTP") || !IsMailType("imap") || IsMailType("http") {
		t.Fatal("mail type detection")
	}
	cases := map[[2]string]string{
		{"smtp", MailTLSNone}:     "25",
		{"smtp", MailTLSImplicit}: "465",
		{"imap", MailTLSNone}:     "143",
		{"imap", MailTLSImplicit}: "993",
		{"pop3", MailTLSNone}:     "110",
		{"pop3", MailTLSImplicit}: "995",
	}
	for in, want := range cases {
		if got := MailDefaultPort(in[0], in[1]); got != want {
			t.Errorf("MailDefaultPort(%s,%s) = %s, want %s", in[0], in[1], got, want)
		}
	}
}

func TestCheckMailUnreachable(t *testing.T) {
	if _, err := checkMail(context.Background(), mailMonitor("smtp", "127.0.0.1:9", MailTLSNone)); err == nil {
		t.Fatal("expected a connection error")
	}
}
