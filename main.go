package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
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

type HumanBytes uint64

func (h *HumanBytes) String() string {
	if h == nil {
		return strconv.FormatUint(uint64(HumanBytes(0)), 10)
	}
	return strconv.FormatUint(uint64(*h), 10)
}

func (h *HumanBytes) Set(s string) error {
	n, err := humanize.ParseBytes(s)
	if err != nil {
		return err
	}
	*h = HumanBytes(n)
	return nil
}

func (h *HumanBytes) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch v := v.(type) {
	case string:
		return h.Set(v)
	case float64:
		*h = HumanBytes(v)
		return nil
	default:
		return fmt.Errorf("cannot unmarshal %q into a bytes number", b)
	}
}

var counter atomic.Uint64

type configuration struct {
	Name      string      `json:"name" flag:"name,,instance name"`
	Addrs     StringSlice `json:"addrs" flag:"addrs,127.0.0.1:12000,bind addresses"`
	Upstream  string      `json:"upstream" flag:"upstream,,upstream address"`
	Rate      HumanBytes  `json:"rate" flag:"rate,,incoming traffic rate limit"`
	PerClient bool        `json:"per_client" flag:"per-client,,apply rate limit per client"`
	NoSplice  bool        `json:"no_splice" flag:"no-splice,,disable the use of the splice syscall (Linux only)"`
	Debug     bool        `json:"debug" flag:"debug,,turn on debugging"`
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

type readerOnly struct {
	io.Reader
}

type writerOnly struct {
	io.Writer
}

func handleClient(c *configuration, conn net.Conn, limiter *rate.Limiter) {
	defer conn.Close()
	if c.Debug {
		defer log.Printf("stop proxying client: %v", conn.RemoteAddr())
		log.Printf("new client: %v", conn.RemoteAddr())
	}
	uconn, err := net.Dial("tcp", c.Upstream)
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
		if c.Debug {
			log.Printf("forward start %v -> %v", fromAddr, toAddr)
			defer log.Printf("forward done %v -> %v", fromAddr, toAddr)
		}
		var w io.Writer = to
		var r io.Reader = from
		if limit {
			r = newThrottledReader(from, limiter)
		}
		if c.NoSplice {
			r = readerOnly{r}
			w = writerOnly{w}
		}
		if _, err := io.Copy(w, r); err != nil {
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
	var limiter *rate.Limiter
	if c.Rate > 0 {
		limiter = rate.NewLimiter(rate.Limit(c.Rate), int(c.Rate))
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
				if c.Rate > 0 && c.PerClient {
					limiter = rate.NewLimiter(rate.Limit(c.Rate), int(c.Rate))
				}
				go handleClient(&c, conn, limiter)
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
