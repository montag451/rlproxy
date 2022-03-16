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

	"golang.org/x/time/rate"
)

var limiter *rate.Limiter

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

func handleClient(conn net.Conn, backend string, debug bool) {
	defer conn.Close()
	if debug {
		defer log.Printf("stop processing request for client: %v", conn.RemoteAddr())
		log.Printf("new client: %v", conn.RemoteAddr())
	}
	bconn, err := net.Dial("tcp", backend)
	if err != nil {
		log.Printf("failed to connect to backend: %v", err)
		return
	}
	defer bconn.Close()
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
	go forward(conn, bconn, true)
	go forward(bconn, conn, false)
	wg.Wait()
}

func main() {
	addr := flag.String("addr", "127.0.0.1:12000", "bind address")
	backend := flag.String("backend", "", "backend address")
	r := flag.Int("rate", 0, "incoming traffic rate limit")
	debug := flag.Bool("debug", false, "turn on debugging")
	flag.Parse()
	if *addr == "" || *backend == "" {
		flag.Usage()
		os.Exit(1)
	}
	if *r >= 8 {
		limit := rate.Limit(*r) / 8
		limiter = rate.NewLimiter(limit, int(limit))
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
		go handleClient(conn, *backend, *debug)
	}
}
