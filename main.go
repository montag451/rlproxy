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
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/montag451/go-sflag"
	"golang.org/x/time/rate"
)

type StringSlice []string

func (ss *StringSlice) String() string {
	if ss == nil {
		return fmt.Sprint(StringSlice{})
	}
	return fmt.Sprint(*ss)
}

func (ss *StringSlice) Set(s string) error {
	*ss = strings.Split(s, ",")
	return nil
}

var counter atomic.Uint64

type configuration struct {
	Name      string      `json:"name" flag:"name,,instance name"`
	Addrs     StringSlice `json:"addrs" flag:"addrs,127.0.0.1:12000,bind addresses"`
	Upstream  string      `json:"upstream" flag:"upstream,,upstream address"`
	Rate      string      `json:"rate" flag:"rate,0,incoming traffic rate limit"`
	PerClient bool        `json:"per_client" flag:"per-client,false,apply rate limit per client"`
	Debug     bool        `json:"debug" flag:"debug,false,turn on debugging"`
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
	if r.l != nil && n > 0 {
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
	}
	counter.Add(uint64(n))
	return n, err
}

const (
	spliceNonblock = 0x2
	maxSpliceSize  = 1 << 20
)

func splice(rfd uintptr, wfd uintptr) (int64, error) {
	return syscall.Splice(int(rfd), nil, int(wfd), nil, maxSpliceSize, spliceNonblock)
}

func (r *throttledReader) WriteTo(w io.Writer) (int64, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return 0, fmt.Errorf("failed create splice pipes: %v", err)
	}
	defer pr.Close()
	defer pw.Close()
	src, err := r.r.(*net.TCPConn).SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("failed to get raw conn for socket reader: %v", err)
	}
	swc, err := w.(*net.TCPConn).SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("failed to get raw conn for socket writer: %v", err)
	}
	prc, err := pr.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("failed to get raw conn for pipe reader: %v", err)
	}
	pwc, err := pw.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("failed to get raw conn for pipe writer: %v", err)
	}
	var written int64
	for {
		var err error
		var inPipe int64
		src.Read(func(rfd uintptr) bool {
			for {
				pwc.Write(func(wfd uintptr) bool {
					inPipe, err = splice(rfd, wfd)
					return true
				})
				if err == syscall.EINTR {
					continue
				}
				if err == syscall.EAGAIN {
					return false
				}
				return true
			}
		})
		if err != nil {
			return written, err
		}
		if inPipe == 0 {
			return written, nil
		}
		if r.l != nil {
			b := r.l.Burst()
			rem := int(inPipe)
			for rem > 0 {
				wait := b
				if rem <= b {
					wait = rem
				}
				_ = r.l.WaitN(context.TODO(), wait)
				rem -= wait
			}
		}
		counter.Add(uint64(inPipe))
		for inPipe > 0 {
			var n int64
			swc.Write(func(wfd uintptr) bool {
				for {
					prc.Read(func(rfd uintptr) bool {
						n, err = splice(rfd, wfd)
						return true
					})
					if err == syscall.EINTR {
						continue
					}
					if err == syscall.EAGAIN {
						return false
					}
					return true
				}
			})
			if err != nil {
				return written, err
			}
			inPipe -= n
			written += n
		}
	}
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
	if len(c.Addrs) == 0 || c.Upstream == "" {
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
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	listeners := make([]*net.TCPListener, len(c.Addrs))
	defer func() {
		for _, l := range listeners {
			l.Close()
		}
	}()
	var wg sync.WaitGroup
	for i, addr := range c.Addrs {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			log.Panic(err)
		}
		listeners[i] = l.(*net.TCPListener)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				conn, err := l.Accept()
				if err != nil {
					if !os.IsTimeout(err) {
						log.Panic(err)
					}
					break
				}
				limiter := limiter
				if r > 0 && c.PerClient {
					limiter = rate.NewLimiter(rate.Limit(r), int(r))
				}
				go handleClient(conn, c.Upstream, limiter, c.Debug)
			}
		}()
	}
	go func() {
		var prev uint64
		for {
			time.Sleep(1 * time.Second)
			cur := counter.Load()
			log.Printf("rate: %v bps", (cur-prev)*8)
			prev = cur
		}
	}()
	sig := <-sigCh
	log.Printf("signal %s received, exiting", sig)
	for _, l := range listeners {
		l.SetDeadline(time.Now())
	}
	wg.Wait()
}
