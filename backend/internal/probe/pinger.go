package probe

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"runtime"
	"sync/atomic"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

const readBufferSize = 1500

// icmpConn is the part of an ICMP socket a ping uses.
type icmpConn interface {
	WriteTo(b []byte, dst net.Addr) (int, error)
	ReadFrom(b []byte) (int, net.Addr, error)
	SetReadDeadline(t time.Time) error
	Close() error
}

// Pinger sends ICMP echo requests. Each ping has a socket of its own, so
// pings run side by side: a target that does not answer costs its own
// timeout and nobody else's.
//
// It used to share one socket per address family behind a lock held for the
// whole wait. Sixty unreachable hosts, a site or a VPN going away, then took
// five minutes to get through, with every probe worker queued behind the
// lock and every other monitor's checks, of every type, late by as much.
type Pinger struct {
	Timeout time.Duration

	// open makes the socket for one ping. It is openICMP except in tests.
	open func(v6 bool) (conn icmpConn, dgram bool, err error)
	// raw4 and raw6 remember that this process may not use ping sockets, so
	// later pings go straight to a raw one.
	raw4, raw6 atomic.Bool
}

func NewPinger(timeout time.Duration) *Pinger {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	p := &Pinger{Timeout: timeout}
	p.open = p.openICMP
	return p
}

// Close is kept for callers that held a shared socket open; a ping now
// closes its own.
func (p *Pinger) Close() {}

func (p *Pinger) Resolve(target string) (net.IP, error) {
	if ip := net.ParseIP(target); ip != nil {
		return ip, nil
	}
	addrs, err := net.LookupIP(target)
	if err != nil || len(addrs) == 0 {
		return nil, fmt.Errorf("resolve %s: %w", target, err)
	}
	var v4, v6 net.IP
	for _, a := range addrs {
		if a.To4() != nil {
			if v4 == nil {
				v4 = a
			}
			continue
		}
		if v6 == nil {
			v6 = a
		}
	}
	if v4 != nil {
		return v4, nil
	}
	if v6 != nil {
		return v6, nil
	}
	return nil, fmt.Errorf("resolve %s: no addresses", target)
}

func randomID() int {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return int(binary.BigEndian.Uint16(b[:]))
}

func addrIP(addr net.Addr) net.IP {
	switch a := addr.(type) {
	case *net.IPAddr:
		return a.IP
	case *net.UDPAddr:
		return a.IP
	default:
		return nil
	}
}

func (p *Pinger) Ping(ctx context.Context, ip net.IP) (float64, error) {
	return p.ping(ctx, ip, ip.To4() == nil)
}

// openICMP opens a socket for one ping. Linux ping sockets (udp4, udp6) work
// as a non-root container user. Raw ICMP needs CAP_NET_RAW in CapEff, which
// Docker only gives root, so user 65532 with cap_add NET_RAW still gets
// EPERM; it is the fallback for hosts that do not allow ping sockets.
func (p *Pinger) openICMP(v6 bool) (icmpConn, bool, error) {
	dgramNet, rawNet, addr, raw := "udp4", "ip4:icmp", "0.0.0.0", &p.raw4
	if v6 {
		dgramNet, rawNet, addr, raw = "udp6", "ip6:ipv6-icmp", "::", &p.raw6
	}
	if !raw.Load() {
		if conn, err := icmp.ListenPacket(dgramNet, addr); err == nil {
			return conn, true, nil
		}
		raw.Store(true)
	}
	conn, err := icmp.ListenPacket(rawNet, addr)
	if err != nil {
		raw.Store(false) // nothing worked; try both again next time
		return nil, false, fmt.Errorf("listen icmp: %w", err)
	}
	return conn, false, nil
}

func (p *Pinger) ping(ctx context.Context, ip net.IP, v6 bool) (float64, error) {
	conn, dgram, err := p.open(v6)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()

	request, reply, proto := icmp.Type(ipv4.ICMPTypeEcho), icmp.Type(ipv4.ICMPTypeEchoReply), 1
	if v6 {
		request, reply, proto = ipv6.ICMPTypeEchoRequest, ipv6.ICMPTypeEchoReply, 58
	}
	id := randomID()
	msg := icmp.Message{Type: request, Code: 0, Body: &icmp.Echo{ID: id, Seq: 1, Data: []byte("argus-probe")}}
	wb, err := msg.Marshal(nil)
	if err != nil {
		return 0, err
	}

	start := time.Now()
	deadline := start.Add(p.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetReadDeadline(deadline)

	var dst net.Addr = &net.IPAddr{IP: ip}
	if dgram {
		dst = &net.UDPAddr{IP: ip}
	}
	if _, err := conn.WriteTo(wb, dst); err != nil {
		return 0, fmt.Errorf("write icmp: %w", err)
	}

	rb := make([]byte, readBufferSize)
	for {
		n, peer, err := conn.ReadFrom(rb)
		if err != nil {
			return 0, fmt.Errorf("timeout waiting for echo reply")
		}
		elapsed := time.Since(start).Seconds() * 1000

		body := rb[:n]
		// A raw IPv4 socket on Linux hands over the IP header too.
		if !v6 && !dgram && runtime.GOOS == "linux" && n >= 20 {
			hdrLen := int(body[0]&0x0f) * 4
			if hdrLen > 0 && hdrLen < n {
				body = body[hdrLen:n]
			}
		}
		rm, err := icmp.ParseMessage(proto, body)
		if err != nil || rm.Type != reply {
			continue
		}
		echo, ok := rm.Body.(*icmp.Echo)
		if !ok {
			continue
		}
		// A raw socket sees every echo reply the host receives, so the id
		// picks out ours. A ping socket rewrites the id and the kernel
		// delivers only this socket's replies.
		if !dgram && echo.ID != id {
			continue
		}
		if peerIP := addrIP(peer); peerIP == nil || !peerIP.Equal(ip) {
			continue
		}
		return elapsed, nil
	}
}
