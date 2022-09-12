package ping

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"

	"github.com/go-logr/logr"

	"github.com/prometheus/client_golang/prometheus"
)

var now = time.Now

type pinger struct {
	conn   net.PacketConn
	listen listenPacket

	addr net.UDPAddr
	seq  uint16

	inflight sync.Map

	obs observerVec

	interval time.Duration
	timeout  time.Duration
}

type observerVec interface {
	WithLabelValues(...string) prometheus.Observer
}

type listenPacket func(network, address string) (net.PacketConn, error)

// NewPinger returns a new instance of pinger
func NewPinger(ip *net.IPAddr, obs observerVec, interval, timeout time.Duration) (Pinger, error) {
	p := pinger{
		obs: obs,
		listen: func(network, address string) (net.PacketConn, error) {
			return icmp.ListenPacket(network, address)
		},
	}

	p.interval = interval
	p.timeout = timeout

	p.addr = net.UDPAddr{
		IP:   ip.IP,
		Zone: ip.Zone,
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	p.seq = uint16(r.Intn(1 << 16))

	return &p, nil
}

// Ping will continuously ping the configured target until the context is cancled
func (p *pinger) Ping(ctx context.Context) error {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	return p.pingWithTicker(ctx, t)
}

func (p *pinger) pingWithTicker(ctx context.Context, t *time.Ticker) error {
	l := logr.FromContextOrDiscard(ctx).WithValues("target", p.addr.String())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn, err := p.listen("udp4", "")
	if err != nil {
		return fmt.Errorf("Failed to listen for ICMP packets: %w", err)
	}
	p.conn = conn
	defer conn.Close()

	go func() {
		defer cancel()
		err := p.receive(ctx)
		if err != nil {
			l.Error(err, "Pinger receiver failed")
		}
	}()

	for {
		select {
		case <-t.C:
			err := p.ping()
			if err != nil {
				l.Error(err, "Pinger failed")
				return err
			}
			p.scrubInflight(ctx)
		case <-ctx.Done():
			return nil
		}
	}
}

func (p *pinger) scrubInflight(ctx context.Context) {
	l := logr.FromContextOrDiscard(ctx)
	p.inflight.Range(func(key, value any) bool {
		sent, ok := value.(time.Time)
		if !ok {
			l.Error(errors.New("inconsistent datastructure"), "Found malformed entry in inflight packet map. Dropping entry.")
			p.inflight.Delete(key)
		}
		if sent.Add(p.timeout).Before(now()) {
			p.obs.WithLabelValues("lost").Observe(p.timeout.Seconds())
			p.inflight.Delete(key)
		}
		return true
	})
}

func (p *pinger) receive(ctx context.Context) error {
	l := logr.FromContextOrDiscard(ctx)
	for {
		res, err := p.recv()
		if err != nil && ctx.Err() == nil {
			l.Error(err, "Failed to receive ping response")
			if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
				return err // socket gone
			}
		}
		if ctx.Err() != nil {
			return nil
		}

		if _, ok := p.inflight.LoadAndDelete(uint16(res.Seq)); ok {
			received := now()
			sent := time.UnixMicro(int64(binary.BigEndian.Uint64(res.Data)))
			l.V(1).Info("got reflection", "id", res.ID, "seq", res.Seq, "sent", sent, "received", received, "latency", received.Sub(sent))
			p.obs.WithLabelValues("completed").Observe(received.Sub(sent).Seconds())
		}
	}
}

func (p *pinger) ping() error {
	sent := now()
	ts := make([]byte, 8)
	binary.BigEndian.PutUint64(ts, uint64(sent.UnixMicro()))

	e := icmp.Echo{
		ID:   0, // Ignored for unpriv ICMP sockets
		Seq:  int(p.seq),
		Data: ts,
	}
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &e,
	}
	msgB, err := msg.Marshal(nil)
	if err != nil {
		return fmt.Errorf("Failed to marshal ICMP packet: %w", err)
	}

	p.inflight.Store(p.seq, sent)
	if _, err := p.conn.WriteTo(msgB, &p.addr); err != nil {
		p.inflight.Delete(p.seq)
		return fmt.Errorf("Failed to send ICMP packet: %w", err)
	}
	p.seq = p.seq + 1
	return nil
}

func (p *pinger) recv() (*icmp.Echo, error) {
	rb := make([]byte, 1500)
	n, _, err := p.conn.ReadFrom(rb)
	if err != nil {
		return nil, fmt.Errorf("Failed to receive packet: %w", err)
	}

	res, err := icmp.ParseMessage(1, rb[:n])
	if err != nil {
		return nil, fmt.Errorf("Failed to parse packet: %w", err)
	}
	if res.Type != ipv4.ICMPTypeEchoReply {
		return nil, nil
	}
	echo, ok := res.Body.(*icmp.Echo)
	if !ok {
		return nil, errors.New("error in icmp library, this should never happen")
	}
	return echo, nil
}
