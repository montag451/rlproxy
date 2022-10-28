package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"

	"github.com/dustin/go-humanize"
	"github.com/montag451/go-sflag"
	"golang.org/x/time/rate"
)

type configuration struct {
	Name      string `json:"name" flag:"name,,instance name"`
	Addr      string `json:"addr" flag:"addr,127.0.0.1:12000,bind addr"`
	Upstream  string `json:"upstream" flag:"upstream,,upstream address"`
	Rate      string `json:"rate" flag:"rate,0,incoming traffic rate limit"`
	PerClient bool   `json:"per_client" flag:"per-client,false,apply rate limit per client"`
	Debug     bool   `json:"debug" flag:"debug,false,turn on debugging"`
}

type throttledReader struct {
	r io.Reader
	l *rate.Limiter
}

func newThrottledReader(r io.Reader, l *rate.Limiter) *throttledReader {
	return &throttledReader{
		r: r,
		l: l,
	}
}

func (r *throttledReader) Read(buf []byte) (int, error) {
	n, err := r.r.Read(buf)
	if r.l == nil || n == 0 {
		return n, err
	}
	b := r.l.Burst()
	rem := n
	for rem > 0 {
		wait := b
		if rem <= b {
			wait = rem
		}
		_ = r.l.WaitN(context.TODO(), wait)
		rem -= wait
	}
	return n, err
}

func handleClient(conn net.Conn, upstream string, limiter *rate.Limiter, debug bool) {
	defer conn.Close()
	if debug {
		defer log.Printf("stop proxying client: %v", conn.RemoteAddr())
		log.Printf("new client: %v", conn.RemoteAddr())
	}
	uconn, err := net.Dial("tcp", upstream)
	if err != nil {
		log.Printf("failed to connect to upstream: %v", err)
		return
	}
	defer uconn.Close()
	var wg sync.WaitGroup
	forward := func(from, to net.Conn, limit bool) {
		defer wg.Done()
		defer to.(*net.TCPConn).CloseWrite()
		fromAddr, toAddr := from.RemoteAddr(), to.RemoteAddr()
		if debug {
			log.Printf("forward start %v -> %v", fromAddr, toAddr)
			defer log.Printf("forward done %v -> %v", fromAddr, toAddr)
		}
		var r io.Reader = from
		if limit {
			r = newThrottledReader(from, limiter)
		}
		if _, err := io.Copy(to, r); err != nil && !errors.Is(err, io.EOF) {
			log.Printf("error while forwarding %v -> %v: %v", fromAddr, toAddr, err)
		}
	}
	wg.Add(2)
	go forward(conn, uconn, true)
	go forward(uconn, conn, false)
	wg.Wait()
}

func parseConf(c *configuration, cf string) error {
	f, err := os.Open(cf)
	if err != nil {
		return fmt.Errorf("failed to open conf file %q: %v", cf, err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(c); err != nil {
		return fmt.Errorf("failed to parse conf file %q: %v", cf, err)
	}
	return nil
}

func main() {
	var c configuration
	cf := flag.String("conf", "", "configuration file")
	sflag.AddFlags(flag.CommandLine, c)
	flag.Parse()
	if *cf != "" {
		err := parseConf(&c, *cf)
		if err != nil {
			log.Panicf("invalid conf %q: %v", *cf, err)
		}
	}
	sflag.SetFromFlags(&c, flag.CommandLine)
	if c.Addr == "" || c.Upstream == "" {
		flag.Usage()
		os.Exit(1)
	}
	if c.Debug {
		log.Printf("%+v", c)
	}
	r, err := humanize.ParseBytes(c.Rate)
	if err != nil {
		log.Panicf("invalid rate %q: %v", c.Rate, err)
	}
	var limiter *rate.Limiter
	if r > 0 {
		limiter = rate.NewLimiter(rate.Limit(r), int(r))
	}
	l, err := net.Listen("tcp", c.Addr)
	if err != nil {
		log.Panic(err)
	}
	defer l.Close()
	for {
		conn, err := l.Accept()
		if err != nil {
			log.Panic(err)
		}
		limiter := limiter
		if r > 0 && c.PerClient {
			limiter = rate.NewLimiter(rate.Limit(r), int(r))
		}
		go handleClient(conn, c.Upstream, limiter, c.Debug)
	}
}
