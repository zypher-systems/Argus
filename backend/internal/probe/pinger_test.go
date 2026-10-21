package probe

import (
	"net"
	"testing"
	"time"
)

func TestAddrIP(t *testing.T) {
	ip4 := net.ParseIP("8.8.8.8")
	if got := addrIP(&net.IPAddr{IP: ip4}); !got.Equal(ip4) {
		t.Fatalf("IPAddr: %v", got)
	}
	if got := addrIP(&net.UDPAddr{IP: ip4}); !got.Equal(ip4) {
		t.Fatalf("UDPAddr: %v", got)
	}
	if addrIP(&net.TCPAddr{IP: ip4}) != nil {
		t.Fatal("TCPAddr should be ignored")
	}
}

func TestResolveLiteral(t *testing.T) {
	p := NewPinger(time.Second)
	ip, err := p.Resolve("1.1.1.1")
	if err != nil || !ip.Equal(net.ParseIP("1.1.1.1")) {
		t.Fatalf("literal: %v %v", ip, err)
	}
}
