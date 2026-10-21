package probe

import (
	"context"
	"net"
	"time"

	"argus/internal/store"
)

func checkTCP(ctx context.Context, m store.Monitor) (CheckOutcome, error) {
	timeout := time.Duration(timeoutSeconds(m, 5)) * time.Second
	d := net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", m.Target)
	out := CheckOutcome{LatencyMs: float64(time.Since(start).Microseconds()) / 1000}
	if err != nil {
		return out, err
	}
	out.Evidence.RemoteAddr = conn.RemoteAddr().String()
	_ = conn.Close()
	return out, nil
}
