package probe

import (
	"bufio"
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"

	"argus/internal/store"
)

// RedisReadCommands are the commands a redis monitor may run. A monitor is a
// health check, so it must not be able to write or delete.
var RedisReadCommands = map[string]bool{
	"PING": true, "GET": true, "EXISTS": true, "TTL": true, "TYPE": true,
	"STRLEN": true, "LLEN": true, "SCARD": true, "HGET": true, "HLEN": true,
	"ZCARD": true, "DBSIZE": true, "ROLE": true, "INFO": true, "LINDEX": true,
	"SISMEMBER": true, "GETRANGE": true,
}

// ValidRedisCommand reports whether a query is a permitted read command.
// maxRedisReply bounds one reply. A check wants a status or a small value.
const maxRedisReply = 1 << 20

func ValidRedisCommand(query string) bool {
	fields := strings.Fields(strings.TrimSpace(query))
	if len(fields) == 0 {
		return true
	}
	return RedisReadCommands[strings.ToUpper(fields[0])]
}

// DatabaseTypes are the database monitor types.
var DatabaseTypes = []string{"postgres", "mysql", "redis"}

// IsDatabaseType reports whether kind is a database monitor.
func IsDatabaseType(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "postgres", "mysql", "redis":
		return true
	}
	return false
}

// DatabaseDefaultPort is the port used when the target names only a host.
func DatabaseDefaultPort(kind string) string {
	switch strings.ToLower(kind) {
	case "postgres":
		return "5432"
	case "mysql":
		return "3306"
	case "redis":
		return "6379"
	}
	return ""
}

// hostPort splits a database target, filling in the protocol's default port.
func hostPort(kind, target string) (host, port string) {
	target = strings.TrimSpace(target)
	if h, p, err := net.SplitHostPort(target); err == nil && h != "" && p != "" {
		return h, p
	}
	return target, DatabaseDefaultPort(kind)
}

// checkDatabase connects, authenticates, and runs one trivial statement. It
// deliberately does not keep a pool: a monitor should prove a fresh connection
// can be made, which is what fails during an outage.
func checkDatabase(ctx context.Context, m store.Monitor) (CheckOutcome, error) {
	timeout := time.Duration(timeoutSeconds(m, 10)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch strings.ToLower(m.Type) {
	case "postgres":
		return checkPostgres(ctx, m, timeout)
	case "mysql":
		return checkMySQL(ctx, m, timeout)
	case "redis":
		return checkRedis(ctx, m)
	default:
		return CheckOutcome{}, fmt.Errorf("unsupported database type %q", m.Type)
	}
}

// queryOrDefault is the statement to run: the operator's, or a trivial one.
func queryOrDefault(m store.Monitor, fallback string) string {
	if q := strings.TrimSpace(m.Config.Query); q != "" {
		return q
	}
	return fallback
}

// matchExpected compares a scalar result against the operator's expectation.
func matchExpected(m store.Monitor, value string) error {
	want := strings.TrimSpace(m.Config.ExpectValue)
	if want == "" {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(value), want) {
		return fmt.Errorf("expected %q, got %q", want, value)
	}
	return nil
}

func checkPostgres(ctx context.Context, m store.Monitor, timeout time.Duration) (CheckOutcome, error) {
	host, port := hostPort("postgres", m.Target)
	sslmode := "require"
	if m.Config.InsecureSkipVerify {
		sslmode = "disable"
	}
	dsn := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(m.Config.AuthUser, m.Config.AuthPassword),
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + strings.TrimPrefix(strings.TrimSpace(m.Config.Database), "/"),
	}
	q := url.Values{}
	q.Set("sslmode", sslmode)
	q.Set("connect_timeout", strconv.Itoa(int(timeout.Seconds())))
	dsn.RawQuery = q.Encode()

	var out CheckOutcome
	start := time.Now()
	conn, err := pgx.Connect(ctx, dsn.String())
	if err != nil {
		out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
		return out, cleanDBError(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var value string
	err = conn.QueryRow(ctx, queryOrDefault(m, "SELECT 1")).Scan(&value)
	out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		return out, cleanDBError(err)
	}
	out.Evidence.BodySnippet = snippetBody([]byte(value), 200)
	return out, matchExpected(m, value)
}

func checkMySQL(ctx context.Context, m store.Monitor, timeout time.Duration) (CheckOutcome, error) {
	host, port := hostPort("mysql", m.Target)
	cfg := mysqldriver.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(host, port)
	cfg.User = m.Config.AuthUser
	cfg.Passwd = m.Config.AuthPassword
	cfg.DBName = strings.TrimSpace(m.Config.Database)
	cfg.Timeout = timeout
	cfg.ReadTimeout = timeout
	cfg.WriteTimeout = timeout
	cfg.AllowNativePasswords = true
	if m.Config.InsecureSkipVerify {
		cfg.TLSConfig = "false"
	} else {
		name := "argus-" + host
		// Registering the same name twice is fine; the driver replaces it.
		_ = mysqldriver.RegisterTLSConfig(name, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		cfg.TLSConfig = name
	}

	var out CheckOutcome
	start := time.Now()
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return out, cleanDBError(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	var value string
	err = db.QueryRowContext(ctx, queryOrDefault(m, "SELECT 1")).Scan(&value)
	out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		return out, cleanDBError(err)
	}
	out.Evidence.BodySnippet = snippetBody([]byte(value), 200)
	return out, matchExpected(m, value)
}

// checkRedis speaks just enough RESP to authenticate and PING, which avoids a
// client dependency for a two-command conversation.
func checkRedis(ctx context.Context, m store.Monitor) (CheckOutcome, error) {
	host, port := hostPort("redis", m.Target)
	addr := net.JoinHostPort(host, port)

	var out CheckOutcome
	start := time.Now()
	dialer := &net.Dialer{}
	var conn net.Conn
	var err error
	if m.Config.TLSMode == MailTLSImplicit {
		conn, err = (&tls.Dialer{
			NetDialer: dialer,
			Config:    &tls.Config{ServerName: host, InsecureSkipVerify: m.Config.InsecureSkipVerify}, //nolint:gosec // operator's choice
		}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
		return out, err
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	if pw := m.Config.AuthPassword; pw != "" {
		args := []string{"AUTH", pw}
		if u := strings.TrimSpace(m.Config.AuthUser); u != "" {
			args = []string{"AUTH", u, pw}
		}
		if _, err := redisCommand(rw, args...); err != nil {
			out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
			return out, fmt.Errorf("auth failed: %w", err)
		}
	}
	args := []string{"PING"}
	if q := strings.TrimSpace(m.Config.Query); q != "" {
		// Saving a monitor already refuses anything but a read. The check
		// refuses it as well, so a row that got here another way cannot
		// write to, reconfigure or empty the server it watches.
		if !ValidRedisCommand(q) {
			return out, fmt.Errorf("redis command %q is not a read command", strings.Fields(q)[0])
		}
		args = strings.Fields(q)
	}
	reply, err := redisCommand(rw, args...)
	out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		return out, err
	}
	out.Evidence.BodySnippet = snippetBody([]byte(reply), 200)
	if len(args) == 1 && strings.EqualFold(args[0], "PING") && !strings.EqualFold(reply, "PONG") {
		return out, fmt.Errorf("unexpected PING reply: %s", reply)
	}
	if err := matchExpected(m, reply); err != nil {
		return out, err
	}
	_, _ = redisCommand(rw, "QUIT")
	return out, nil
}

// redisCommand writes one RESP array and reads a single-line reply.
func redisCommand(rw *bufio.ReadWriter, args ...string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := rw.WriteString(b.String()); err != nil {
		return "", err
	}
	if err := rw.Flush(); err != nil {
		return "", err
	}
	line, err := rw.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", fmt.Errorf("empty reply")
	}
	switch line[0] {
	case '+':
		return line[1:], nil
	case '-':
		return "", fmt.Errorf("%s", line[1:])
	case '$':
		n, convErr := strconv.Atoi(line[1:])
		if convErr != nil || n < 0 {
			return "", nil
		}
		// The length is whatever the far end says it is.
		if n > maxRedisReply {
			return "", fmt.Errorf("reply of %d bytes is larger than a check reads", n)
		}
		buf := make([]byte, n+2)
		if _, err := io_ReadFull(rw, buf); err != nil {
			return "", err
		}
		return string(buf[:n]), nil
	case ':':
		return line[1:], nil
	default:
		return line, nil
	}
}

// io_ReadFull is io.ReadFull over the buffered reader.
func io_ReadFull(rw *bufio.ReadWriter, buf []byte) (int, error) {
	read := 0
	for read < len(buf) {
		n, err := rw.Read(buf[read:])
		read += n
		if err != nil {
			return read, err
		}
	}
	return read, nil
}

// cleanDBError keeps a driver error useful without echoing a password back
// into the event log.
func cleanDBError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if i := strings.Index(msg, "://"); i >= 0 {
		if at := strings.Index(msg[i:], "@"); at >= 0 {
			msg = msg[:i+3] + "***" + msg[i+at:]
		}
	}
	return fmt.Errorf("%s", msg)
}
