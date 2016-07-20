package cdialer

import (
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var defaultTTL = 1 * time.Hour

type dialer interface {
	Dial(network, address string) (net.Conn, error)
}

type Dialer struct {
	D           dialer
	LookupIP    func(host string) (ips []net.IP, err error)
	TTL         time.Duration
	ExcludeIPv6 bool

	mx       sync.RWMutex
	addrs    map[string][]string
	idx      int64
	resolved time.Time
}

func Wrap(d dialer) *Dialer {
	return &Dialer{D: d, TTL: defaultTTL}
}

func (d *Dialer) Dial(network, host string) (net.Conn, error) {
	addrs, err := d.getAddrs(host)
	if err != nil {
		return nil, err
	}

	if len(addrs) == 0 {
		return nil, errors.New(`dialer: can't resolve host "` + host + `"`)
	}

	idx := atomic.AddInt64(&d.idx, 1)
	addr := addrs[int(idx)%len(addrs)]

	conn, err := d.D.Dial(network, addr)
	if err != nil { // remove IP from the cache
		d.mx.Lock()
		var ok bool
		addrs, ok = d.addrs[host]
		if !ok || len(addrs) == 0 {
			d.mx.Unlock()
			return conn, err
		}

		index := 0
		found := false
		for i, a := range addrs {
			if a == addr {
				index = i
				found = true
				break
			}
		}
		if found {
			addrs2 := make([]string, len(addrs)-1)
			copy(addrs2[:index], addrs[:index])
			copy(addrs2[index:], addrs[index+1:])
			d.addrs[host] = addrs2
		}
		d.mx.Unlock()
	}

	return conn, err
}

func (d *Dialer) getAddrs(address string) ([]string, error) {
	now := time.Now()
	if now.Sub(d.resolved) > d.TTL {
		d.mx.Lock()

		var addrs []string
		var err error

		if now.Sub(d.resolved) <= d.TTL {
			list, ok := d.addrs[address]
			if ok && len(addrs) > 0 {
				addrs = list
			}
		}

		if len(addrs) == 0 {
			addrs, err = d.updateAddrs(address)
		}

		d.mx.Unlock()
		return addrs, err
	}

	d.mx.RLock()
	addrs, ok := d.addrs[address]
	d.mx.RUnlock()

	if !ok || len(addrs) == 0 {
		d.mx.Lock()
		if addrs, ok = d.addrs[address]; !ok || len(addrs) == 0 {
			list, err := d.updateAddrs(address)
			if err != nil {
				d.mx.Unlock()
				return nil, err
			}
			addrs = list
		}

		d.mx.Unlock()
	}

	return addrs, nil
}

func (d *Dialer) updateAddrs(address string) ([]string, error) {
	addrs, err := d.resolve(address)
	if err != nil {
		return nil, err
	}

	if d.addrs == nil {
		d.addrs = map[string][]string{}
	}
	d.addrs[address] = addrs
	d.resolved = time.Now()

	if d.D == nil {
		d.D = &net.Dialer{}
	}

	return addrs, nil
}

func (d *Dialer) resolve(address string) ([]string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}

	if d.LookupIP == nil {
		d.LookupIP = net.LookupIP
	}

	ips, err := d.LookupIP(host)
	if err != nil {
		return nil, err
	}

	addrs := make([]string, 0, len(ips))
	for _, ip := range ips {
		addr := ip.String()

		if d.ExcludeIPv6 {
			if strings.IndexRune(addr, ':') > -1 {
				continue
			}
		}

		addrs = append(addrs, "["+addr+"]:"+port)
	}
	return addrs, nil
}
