package cdialer

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type testDialer struct {
	d func(ctx context.Context, network, address string) (net.Conn, error)
}

func (d testDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.d(ctx, network, address)
}

func TestNoPanic(t *testing.T) {
	d := &Dialer{}
	d.DialContext(context.Background(), "tcp", "localhost")

	d = &Dialer{}
	d.DialContext(context.Background(), "tcp", "localhost:80")
}

func TestWrap(t *testing.T) {
	used := false
	dial := testDialer{d: func(context.Context, string, string) (net.Conn, error) {
		used = true
		return nil, errors.New("")
	}}

	var d *Dialer = Wrap(dial)
	d.DialContext(context.Background(), "tcp", "localhost:80")
	assert.True(t, used)
}

func TestReuseIP(t *testing.T) {
	usedIPs := make([]string, 0)

	c := &net.TCPConn{}
	d := &Dialer{
		D: testDialer{d: func(ctx context.Context, network string, address string) (net.Conn, error) {
			usedIPs = append(usedIPs, address)
			return c, nil
		}},
		TTL:      defaultTTL,
		resolved: time.Now(),
		addrs: map[string][]string{
			"github.com:80": []string{"10.0.0.1:80"},
		},
	}

	testCases := []string{"10.0.0.1:80", "10.0.0.1:80", "10.0.0.1:80"}
	for i, v := range testCases {
		conn, err := d.DialContext(context.Background(), "tcp", "github.com:80")
		assert.Nil(t, err)
		assert.Equal(t, conn, c)
		assert.Equal(t, usedIPs[i], v)
	}
}

func TestIterateOverCachedIPs(t *testing.T) {
	usedIPs := make([]string, 0)

	c := &net.TCPConn{}
	d := &Dialer{
		D: testDialer{d: func(ctx context.Context, network string, address string) (net.Conn, error) {
			usedIPs = append(usedIPs, address)
			return c, nil
		}},
		TTL:      defaultTTL,
		resolved: time.Now(),
		addrs: map[string][]string{
			"github.com:80": []string{"10.0.0.1:80", "10.0.0.2:80", "10.0.0.3:80"},
		},
	}

	testCases := []string{
		"10.0.0.2:80", "10.0.0.3:80", "10.0.0.1:80",
		"10.0.0.2:80", "10.0.0.3:80", "10.0.0.1:80",
	}

	for i, v := range testCases {
		conn, err := d.DialContext(context.Background(), "tcp", "github.com:80")
		assert.Nil(t, err)
		assert.Equal(t, conn, c)
		assert.Equal(t, usedIPs[i], v)
	}
}

func TestRemoveBrokenIPFromCache(t *testing.T) {
	usedIPs := make([]string, 0)

	e := errors.New("Invalid address")
	d := &Dialer{
		D: testDialer{d: func(ctx context.Context, network string, address string) (net.Conn, error) {
			usedIPs = append(usedIPs, address)
			return nil, e
		}},
		TTL:      defaultTTL,
		resolved: time.Now(),
		addrs: map[string][]string{
			"github.com:80": []string{
				"10.0.0.1:80", "10.0.0.2:80",
				"10.0.0.3:80", "10.0.0.4:80",
			},
		},
	}

	testCases := []struct {
		used string
		left []string
	}{
		{
			used: "10.0.0.2:80",
			left: []string{"10.0.0.1:80", "10.0.0.3:80", "10.0.0.4:80"},
		},
		{
			used: "10.0.0.4:80",
			left: []string{"10.0.0.1:80", "10.0.0.3:80"},
		},
		{
			used: "10.0.0.3:80",
			left: []string{"10.0.0.1:80"},
		},
		{
			used: "10.0.0.1:80",
			left: []string{},
		},
	}

	for i := range testCases {
		_, err := d.DialContext(context.Background(), "tcp", "github.com:80")
		assert.Equal(t, err, e)
		assert.Equal(t, testCases[i].used, usedIPs[i])
		assert.Equal(t, d.addrs["github.com:80"], testCases[i].left)
	}
}

func TestResolveHostWhenCacheIsEmpty(t *testing.T) {
	usedIPs := make([]string, 0)
	resIdx := 0
	resolved := make(chan bool, 1)

	e := errors.New("Invalid address")
	d := &Dialer{
		D: testDialer{d: func(ctx context.Context, network string, address string) (net.Conn, error) {
			usedIPs = append(usedIPs, address)
			return nil, e
		}},
		TTL:      defaultTTL,
		resolved: time.Now(),
		LookupIP: func(string) ([]net.IP, error) {
			resolved <- true

			ips := [][]net.IP{
				[]net.IP{net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.2"), net.ParseIP("10.0.0.3")},
				[]net.IP{net.ParseIP("10.0.0.4"), net.ParseIP("10.0.0.5"), net.ParseIP("10.0.0.6")},
			}
			res := ips[resIdx]
			resIdx += 1
			return res, nil
		},
	}

	testCases := []struct {
		used     string
		left     []string
		resolved bool
	}{
		{
			used:     "[10.0.0.2]:80",
			left:     []string{"[10.0.0.1]:80", "[10.0.0.3]:80"},
			resolved: true,
		},
		{
			used:     "[10.0.0.1]:80",
			left:     []string{"[10.0.0.3]:80"},
			resolved: false,
		},
		{
			used:     "[10.0.0.3]:80",
			left:     []string{},
			resolved: false,
		},
		{
			used:     "[10.0.0.5]:80",
			left:     []string{"[10.0.0.4]:80", "[10.0.0.6]:80"},
			resolved: true,
		},
		{
			used:     "[10.0.0.6]:80",
			left:     []string{"[10.0.0.4]:80"},
			resolved: false,
		},
		{
			used:     "[10.0.0.4]:80",
			left:     []string{},
			resolved: false,
		},
	}

	for i := range testCases {
		_, err := d.DialContext(context.Background(), "tcp", "github.com:80")
		assert.Equal(t, err, e)
		assert.Equal(t, testCases[i].used, usedIPs[i])
		assert.Equal(t, d.addrs["github.com:80"], testCases[i].left)

		var resolving bool
		select {
		case resolving = <-resolved:
		default:
		}
		assert.Equal(t, testCases[i].resolved, resolving)
	}
}

func TestResolveNewIPsWhenTTLExpired(t *testing.T) {
	var usedIP string

	d := &Dialer{
		TTL:      defaultTTL,
		resolved: time.Now().Add(-defaultTTL),
		addrs: map[string][]string{
			"github.com:80": []string{"10.0.0.1:80"},
		},
		D: testDialer{d: func(ctx context.Context, network string, address string) (net.Conn, error) {
			usedIP = address
			return nil, nil
		}},
		LookupIP: func(host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("10.0.0.2")}, nil
		},
	}

	_, err := d.DialContext(context.Background(), "tcp", "github.com:80")
	assert.Nil(t, err)
	assert.Equal(t, usedIP, "[10.0.0.2]:80")
	assert.Equal(t, d.addrs["github.com:80"], []string{"[10.0.0.2]:80"})
}

func TestResolve(t *testing.T) {
	d := Dialer{
		LookupIP: func(host string) ([]net.IP, error) {
			ips := []net.IP{
				net.ParseIP("10.11.12.13"),
				net.ParseIP("10.11.12.14"),
				net.ParseIP("2001:470:1:18::119"),
			}
			return ips, nil
		},
	}

	addrs, err := d.resolve("github.com:80")
	assert.NoError(t, err)
	assert.Len(t, addrs, 3)
	assert.Equal(t, addrs[0], "[10.11.12.13]:80")
	assert.Equal(t, addrs[1], "[10.11.12.14]:80")
	assert.Equal(t, addrs[2], "[2001:470:1:18::119]:80")
}

func TestResolveExcludesIPv6(t *testing.T) {
	d := Dialer{
		ExcludeIPv6: true,
		LookupIP: func(host string) ([]net.IP, error) {
			ips := []net.IP{
				net.ParseIP("10.11.12.13"),
				net.ParseIP("10.11.12.14"),
				net.ParseIP("2001:470:1:18::119"),
			}
			return ips, nil
		},
	}

	addrs, err := d.resolve("github.com:80")
	assert.NoError(t, err)
	assert.Len(t, addrs, 2)
	assert.Equal(t, addrs[0], "[10.11.12.13]:80")
	assert.Equal(t, addrs[1], "[10.11.12.14]:80")
}
