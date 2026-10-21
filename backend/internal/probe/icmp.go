package probe

import (
	"context"
	"time"

	"argus/internal/store"
)

func checkICMP(ctx context.Context, pinger *Pinger, m store.Monitor) (CheckOutcome, error) {
	if pinger == nil {
		pinger = NewPinger(time.Duration(timeoutSeconds(m, 5)) * time.Second)
	}
	ip, err := pinger.Resolve(m.Target)
	if err != nil {
		return CheckOutcome{}, err
	}
	// The monitor's own timeout, five seconds unless it says otherwise. The
	// shared pinger allows up to its ceiling and stops at this deadline.
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds(m, 5))*time.Second)
	defer cancel()
	lat, err := pinger.Ping(ctx, ip)
	out := CheckOutcome{LatencyMs: lat}
	out.Evidence.RemoteAddr = ip.String()
	return out, err
}
