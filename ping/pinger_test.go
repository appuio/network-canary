package ping

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

func TestPing(t *testing.T) {
	mConn := &mockConn{
		sentChan: make(chan *icmp.Echo, 5),
		recvChan: make(chan *icmp.Echo, 5),
	}
	mockTime := mockClock{
		time: time.Now(),
		c:    make(chan time.Time),
	}
	mockObs := &mockObserveVec{
		observations: make(chan observation, 5),
	}
	p := pinger{
		listen: mConn.Listen(),
		addr: net.UDPAddr{
			IP: net.ParseIP("10.2.0.1"),
		},
		inflight:  sync.Map{},
		obs:       mockObs,
		seq:       1,
		sndTicker: mockTime.ticker(),
		timeout:   5 * time.Second,
	}
	now = mockTime.get
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		err := p.Ping(ctx)
		require.NoError(t, err)
	}()

	t.Run("success", func(t *testing.T) {
		pingTick(t, &mockTime, mConn, mockObs, false, 1, 10)
		pingTick(t, &mockTime, mConn, mockObs, false, 2, 8)
		pingTick(t, &mockTime, mConn, mockObs, false, 3, 92)
		pingTick(t, &mockTime, mConn, mockObs, false, 4, 100)
		pingTick(t, &mockTime, mConn, mockObs, false, 5, 290)
		pingTick(t, &mockTime, mConn, mockObs, false, 6, 254)
	})
	p.seq = 1
	t.Run("loss", func(t *testing.T) {
		pingTick(t, &mockTime, mConn, mockObs, false, 1, 920)
		pingTick(t, &mockTime, mConn, mockObs, false, 2, 0)
		pingTick(t, &mockTime, mConn, mockObs, false, 3, 92)
		pingTick(t, &mockTime, mConn, mockObs, false, 4, 430)
		pingTick(t, &mockTime, mConn, mockObs, false, 5, 0)
		pingTick(t, &mockTime, mConn, mockObs, false, 6, 254)
		pingTick(t, &mockTime, mConn, mockObs, false, 7, 194)
		pingTick(t, &mockTime, mConn, mockObs, true, 8, 54)
		pingTick(t, &mockTime, mConn, mockObs, false, 9, 104)
		pingTick(t, &mockTime, mConn, mockObs, false, 10, 74)
		pingTick(t, &mockTime, mConn, mockObs, true, 11, 14)
	})

}

func pingTick(t *testing.T, mt *mockClock, conn *mockConn, obs *mockObserveVec, getTimout bool, seq int, latency int) {
	mt.tick()
	if getTimout {
		o := <-obs.observations
		assert.InDelta(t, 5, o.val, 0.00001)
		assert.Equal(t, "lost", o.label)
	}
	b := <-conn.sentChan
	assert.Equal(t, seq, b.Seq)
	assert.Equal(t,
		uint64(mt.get().UnixMicro()),
		binary.BigEndian.Uint64(b.Data))

	if latency != 0 {
		mt.add(time.Duration(latency) * time.Millisecond)
		conn.recvChan <- b
		o := <-obs.observations
		assert.InDelta(t, float64(latency)/1000, o.val, 0.00001)
		assert.Equal(t, "completed", o.label)
	}

	mt.add(time.Duration(1000-latency) * time.Millisecond)
}

type mockClock struct {
	time time.Time
	mu   sync.Mutex
	c    chan time.Time
}

func (c *mockClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.time = t
}
func (c *mockClock) get() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.time
}
func (c *mockClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.time = c.time.Add(d)
}
func (c *mockClock) ticker() *time.Ticker {
	return &time.Ticker{
		C: c.c,
	}
}
func (c *mockClock) tick() {
	c.c <- c.get()
}

type mockObserveVec struct {
	observations chan observation
}
type observation struct {
	label string
	val   float64
}

func (v mockObserveVec) WithLabelValues(ls ...string) prometheus.Observer {
	return prometheus.ObserverFunc(func(f float64) {
		v.observations <- observation{
			label: ls[0],
			val:   f,
		}
	})
}

type mockConn struct {
	sentChan chan *icmp.Echo
	recvChan chan *icmp.Echo
}

func (c *mockConn) Listen() listenPacket {
	return func(network string, address string) (net.PacketConn, error) {
		return c, nil
	}
}
func (c *mockConn) ReadFrom(p []byte) (int, net.Addr, error) {
	res := <-c.recvChan
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEchoReply,
		Code: 0,
		Body: res,
	}
	msgB, err := msg.Marshal(nil)
	if err != nil {
		return 0, nil, err
	}
	copy(p, msgB)
	return len(msgB), nil, nil
}
func (c *mockConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	res, err := icmp.ParseMessage(1, p)
	if err != nil {
		return 0, fmt.Errorf("Failed to parse packet: %w", err)
	}
	if res.Type != ipv4.ICMPTypeEcho {
		return 0, errors.New("Not an echo message")
	}
	echo, ok := res.Body.(*icmp.Echo)
	if !ok {
		return 0, errors.New("error in icmp library, this should never happen")
	}

	c.sentChan <- echo
	return len(p), nil
}
func (c *mockConn) Close() error {
	return nil
}
func (c *mockConn) LocalAddr() net.Addr {
	return nil
}
func (c *mockConn) SetDeadline(t time.Time) error {
	return nil
}
func (c *mockConn) SetReadDeadline(t time.Time) error {
	return nil
}
func (c *mockConn) SetWriteDeadline(t time.Time) error {
	return nil
}
