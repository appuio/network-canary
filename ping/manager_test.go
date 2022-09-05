package ping

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_schedule(t *testing.T) {
	mockBuilder := &mockPingBuilder{
		running: map[string]struct{}{},
	}
	m := Manager{
		pingCtxs:  map[string]Context{},
		hist:      &mockCurrry{},
		newPinger: mockBuilder.build,

		lookupIP: mockDNSResolve(
			map[string][]net.IP{
				"foo.example.com": {
					net.ParseIP("10.244.15.2"),
				},
				"bar.example.com": {
					net.ParseIP("10.244.15.3"),
				},
			}),
		dnsTargets: []string{"foo.example.com"},
		ipTargets:  []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"},
	}

	err := m.schedulePingers(context.TODO())
	require.NoError(t, err)
	assertRunning(t, mockBuilder, []net.IP{
		net.ParseIP("10.244.15.2"),
		net.ParseIP("10.0.0.1"),
		net.ParseIP("10.0.0.2"),
		net.ParseIP("10.0.0.3"),
	})
	assertNotRunning(t, mockBuilder, []net.IP{
		net.ParseIP("10.244.15.3"),
		net.ParseIP("10.0.0.5"),
	})

	m.lookupIP = mockDNSResolve(
		map[string][]net.IP{
			"foo.example.com": {
				net.ParseIP("10.244.15.10"),
				net.ParseIP("10.244.15.11"),
				net.ParseIP("10.244.15.12"),
			},
		})
	err = m.schedulePingers(context.TODO())
	require.NoError(t, err)
	assertRunning(t, mockBuilder, []net.IP{
		net.ParseIP("10.244.15.10"),
		net.ParseIP("10.244.15.11"),
		net.ParseIP("10.244.15.12"),
		net.ParseIP("10.0.0.1"),
		net.ParseIP("10.0.0.2"),
		net.ParseIP("10.0.0.3"),
	})
	assertNotRunning(t, mockBuilder, []net.IP{
		net.ParseIP("10.244.15.2"),
	})

	m.lookupIP = mockDNSResolve(
		map[string][]net.IP{
			"foo.example.com": {
				net.ParseIP("10.244.15.11"),
				net.ParseIP("10.244.15.12"),
				net.ParseIP("10.244.15.13"),
			},
		})
	err = m.schedulePingers(context.TODO())
	require.NoError(t, err)
	assertRunning(t, mockBuilder, []net.IP{
		net.ParseIP("10.244.15.11"),
		net.ParseIP("10.244.15.12"),
		net.ParseIP("10.244.15.13"),
		net.ParseIP("10.0.0.1"),
		net.ParseIP("10.0.0.2"),
		net.ParseIP("10.0.0.3"),
	})
	assertNotRunning(t, mockBuilder, []net.IP{
		net.ParseIP("10.244.15.2"),
		net.ParseIP("10.244.15.10"),
	})

}

func assertRunning(t *testing.T, mb *mockPingBuilder, ips []net.IP) bool {
	return assert.Eventually(t, func() bool {
		for _, ip := range ips {
			if !mb.isRunning(ip) {
				return false
			}
		}
		return true
	}, time.Second, 10*time.Millisecond)
}
func assertNotRunning(t *testing.T, mb *mockPingBuilder, ips []net.IP) bool {
	return assert.Eventually(t, func() bool {
		for _, ip := range ips {
			if mb.isRunning(ip) {
				return false
			}
		}
		return true
	}, time.Second, 10*time.Millisecond)
}

func mockDNSResolve(m map[string][]net.IP) func(host string) ([]net.IP, error) {
	return func(host string) ([]net.IP, error) {
		ips, ok := m[host]
		if !ok {
			return nil, errors.New("host not found")
		}
		return ips, nil
	}
}

type mockPingBuilder struct {
	mu      sync.Mutex
	running map[string]struct{}
}

func (mb *mockPingBuilder) build(ip *net.IPAddr, obs observerVec, _, _ time.Duration) (Pinger, error) {
	return &mockPinger{
		ip: ip.IP,
		b:  mb,
	}, nil
}
func (mb *mockPingBuilder) start(ip net.IP) {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.running[ip.String()] = struct{}{}
}
func (mb *mockPingBuilder) stop(ip net.IP) {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	delete(mb.running, ip.String())
}
func (mb *mockPingBuilder) isRunning(ip net.IP) bool {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	_, ok := mb.running[ip.String()]
	return ok
}

type mockPinger struct {
	ip net.IP
	b  *mockPingBuilder
}

func (m *mockPinger) Ping(ctx context.Context) error {
	m.b.start(m.ip)
	<-ctx.Done()
	m.b.stop(m.ip)
	return nil
}

type mockCurrry struct {
}

func (mc *mockCurrry) CurryWith(labels prometheus.Labels) (prometheus.ObserverVec, error) {
	return nil, nil
}
