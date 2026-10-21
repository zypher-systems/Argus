package probe

import (
	"context"
	"net"
	"os"
	"testing"
	"time"
)

func TestPingPublicDNS(t *testing.T) {
	if os.Getenv("ARGUS_NET_TEST") == "" {
		t.Skip("set ARGUS_NET_TEST=1 to ping 8.8.8.8")
	}
	p := NewPinger(5 * time.Second)
	defer p.Close()
	for _, target := range []string{"8.8.8.8", "1.1.1.1", "127.0.0.1"} {
		ip := net.ParseIP(target)
		ms, err := p.Ping(context.Background(), ip)
		t.Logf("%s -> %.2fms err=%v", target, ms, err)
		if err != nil {
			t.Errorf("%s: %v", target, err)
		}
	}
}
