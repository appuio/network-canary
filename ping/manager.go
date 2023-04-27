package ping

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/go-logr/logr"

	"github.com/prometheus/client_golang/prometheus"
)

// Manager starts and stops pinger instances based on configuration and DNS responses
type Manager struct {
	source string

	pingCtxs map[string]Context

	updateInterval time.Duration
	hist           observerCurrier

	pingInterval time.Duration
	pingTimout   time.Duration

	newPinger pingBuilder
	lookupIP  dnsResolver

	ipTargets  []string
	dnsTargets []string
}

type observerCurrier interface {
	CurryWith(labels prometheus.Labels) (prometheus.ObserverVec, error)
}

type pingBuilder func(ip *net.IPAddr, obs observerVec, interval, timeout time.Duration) (Pinger, error)

type dnsResolver func(host string) ([]net.IP, error)

// Pinger provides a method to continuously ping a single configured endpoint
type Pinger interface {
	Ping(ctx context.Context) error
}

// Context is used to track the execution of Pingers
type Context struct {
	context.Context
	cancel context.CancelFunc
}

// ManagerConfig contains configuration of the Manager
type ManagerConfig struct {
	Source     string
	DNSTargets []string
	IPTargets  []string

	UpdateInterval time.Duration
	PingInterval   time.Duration
	PingTimeout    time.Duration
}

// NewManager returns a new instance of a ping Manager
func NewManager(hist *prometheus.HistogramVec, conf ManagerConfig) *Manager {
	return &Manager{
		source: conf.Source,

		updateInterval: conf.UpdateInterval,
		hist:           hist,

		newPinger: NewPinger,
		lookupIP:  net.LookupIP,

		pingCtxs: map[string]Context{},

		dnsTargets: conf.DNSTargets,
		ipTargets:  conf.IPTargets,

		pingInterval: conf.PingInterval,
		pingTimout:   conf.PingTimeout,
	}
}

// Run starts the manager.
// This will block until the context is canceled
func (m *Manager) Run(ctx context.Context) error {
	l := logr.FromContextOrDiscard(ctx)
	ticker := time.NewTicker(m.updateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			err := m.schedulePingers(ctx)
			// We don't want to fail if DNS disappears, just log and move on.
			if err != nil {
				l.Error(err, "Failed to schedule pingers")
			}
		}
	}
}
func (m *Manager) schedulePingers(ctx context.Context) error {
	l := logr.FromContextOrDiscard(ctx)
	l.V(1).Info("Updating ping targets")
	ips, err := m.lookUpTargets(ctx)
	if err != nil {
		return err
	}
	err = m.cleanupPingers(ctx, ips)
	if err != nil {
		return err
	}
	err = m.startPingers(ctx, ips)
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) cleanupPingers(ctx context.Context, ips []*net.IPAddr) error {
	l := logr.FromContextOrDiscard(ctx)
	for ip, pCtx := range m.pingCtxs {
		if pCtx.Err() != nil {
			l.Info("Removing failed pinger")
			delete(m.pingCtxs, ip)
		}
		if !ipInList(ip, ips) {
			l.Info("Stopping outdated pinger")
			m.pingCtxs[ip].cancel()
			delete(m.pingCtxs, ip)
		}
	}
	return nil
}

func (m *Manager) startPingers(ctx context.Context, ips []*net.IPAddr) error {
	l := logr.FromContextOrDiscard(ctx)
	for i := range ips {
		if _, ok := m.pingCtxs[ips[i].String()]; !ok {
			l.Info("Starting pinger", "target", ips[i].String())
			err := m.startPing(ctx, ips[i])
			if err != nil {
				l.Error(err, "Failed to start ping")
			}
		}
	}
	return nil
}

func ipInList(key string, ips []*net.IPAddr) bool {
	for _, ip := range ips {
		if ip.String() == key {
			return true
		}
	}
	return false
}

func (m *Manager) lookUpTargets(ctx context.Context) ([]*net.IPAddr, error) {
	ips := []*net.IPAddr{}
	for _, t := range m.ipTargets {
		ip, err := net.ResolveIPAddr("ip", t)
		if err != nil {
			return nil, fmt.Errorf("Failed to resolve IP addr: %w", err)
		}

		ips = append(ips, ip)
	}
	for _, t := range m.dnsTargets {
		res, err := m.lookupIP(t)
		if err != nil {
			return nil, fmt.Errorf("Failed to resolve DNS addr: %w", err)
		}
		for _, ip := range res {
			if ip.To4() == nil {
				// Not an IPv4
				continue
			}
			ips = append(ips, &net.IPAddr{
				IP: ip,
			})
		}
	}
	return ips, nil
}

func (m *Manager) startPing(ctx context.Context, ip *net.IPAddr) error {
	l := logr.FromContextOrDiscard(ctx)
	l = l.WithName(ip.IP.String())

	labels := prometheus.Labels{"source": m.source, "target": ip.IP.String()}
	hist, err := m.hist.CurryWith(labels)
	if err != nil {
		return err
	}
	pinger, err := m.newPinger(ip, hist, m.pingInterval, m.pingTimout)
	if err != nil {
		return err
	}
	ctx = logr.NewContext(ctx, l)
	ctx, cancel := context.WithCancel(ctx)
	m.pingCtxs[ip.String()] = Context{
		Context: ctx,
		cancel:  cancel,
	}

	go func(ctx context.Context, cancel context.CancelFunc) {
		defer cancel()
		err := pinger.Ping(ctx)
		if err != nil {
			l.Error(err, "Pinger failed", "ip", ip.IP.String())
		}
	}(ctx, cancel)

	return nil
}
