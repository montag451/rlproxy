package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"sync"

	"github.com/dustin/go-humanize"
	"golang.org/x/time/rate"
)

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
			log.Println("error while forwarding %v -> %v", fromAddr, toAddr)
		}
	}
	wg.Add(2)
	go forward(conn, uconn, true)
	go forward(uconn, conn, false)
	wg.Wait()
}

func main() {
	addr := flag.String("addr", "127.0.0.1:12000", "bind address")
	upstream := flag.String("upstream", "", "upstream address")
	rs := flag.String("rate", "0", "incoming traffic rate limit")
	perClient := flag.Bool("per-client", false, "apply rate limit per client")
	debug := flag.Bool("debug", false, "turn on debugging")
	flag.Parse()
	if *addr == "" || *upstream == "" {
		flag.Usage()
		os.Exit(1)
	}
	r, err := humanize.ParseBytes(*rs)
	if err != nil {
		log.Panicf("invalid rate %q: %v", *rs, err)
	}
	var limiter *rate.Limiter
	if r > 0 {
		limiter = rate.NewLimiter(rate.Limit(r), int(r))
	}
	l, err := net.Listen("tcp", *addr)
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
		if r > 0 && *perClient {
			limiter = rate.NewLimiter(rate.Limit(r), int(r))
		}
		go handleClient(conn, *upstream, limiter, *debug)
	}
}
