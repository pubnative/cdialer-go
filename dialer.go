package cdialer

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
)

type dialer interface {
	Dial(network, address string) (net.Conn, error)
}

type Dialer struct {
	D        dialer
	LookupIP func(host string) (ips []net.IP, err error)

	mx    sync.RWMutex
	addrs map[string][]string
	idx   int64
}

func Wrap(d dialer) *Dialer {
	return &Dialer{D: d}
}

func (d *Dialer) Dial(network, address string) (net.Conn, error) {
	d.mx.RLock()
	addrs, ok := d.addrs[address]
	d.mx.RUnlock()

	if !ok || len(addrs) == 0 {
		d.mx.Lock()
		if addrs, ok = d.addrs[address]; !ok || len(addrs) == 0 {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				d.mx.Unlock()
				return nil, err
			}

			if d.LookupIP == nil {
				d.LookupIP = net.LookupIP
			}

			ips, err := d.LookupIP(host)
			if err != nil {
				d.mx.Unlock()
				return nil, err
			}

			addrs = make([]string, 0, len(ips))
			for _, ip := range ips {
				addrs = append(addrs, ip.String()+":"+port)
			}
			if d.addrs == nil {
				d.addrs = map[string][]string{}
			}
			d.addrs[address] = addrs
		}

		if d.D == nil {
			d.D = &net.Dialer{}
		}

		d.mx.Unlock()
	}

	if len(addrs) == 0 {
		return nil, errors.New(`dialer: can't resolve host "` + address + `"`)
	}

	idx := atomic.AddInt64(&d.idx, 1)
	addr := addrs[int(idx)%len(addrs)]

	conn, err := d.D.Dial(network, addr)
	if err != nil { // remove IP from the cache
		d.mx.Lock()
		addrs, ok = d.addrs[address]
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
			d.addrs[address] = addrs2
		}
		d.mx.Unlock()
	}

	return conn, err
}
