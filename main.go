package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/montag451/go-sflag"
	"golang.org/x/time/rate"
)

var counter atomic.Uint64

func handleClient(c *configuration, conn net.Conn, limiter *rate.Limiter) {
	defer conn.Close()
	logger := c.logger.With().Stringer("client", conn.RemoteAddr()).Logger()
	defer logger.Debug().Msg("stop proxying client")
	logger.Debug().Msg("new client")
	uconn, err := net.Dial("tcp", c.Upstream)
	if err != nil {
		logger.Err(err).Msg("failed to connect to upstream")
		return
	}
	defer uconn.Close()
	var wg sync.WaitGroup
	forward := func(from, to net.Conn, limit bool) {
		defer wg.Done()
		defer to.(*net.TCPConn).CloseWrite()
		logger := logger.With().
			Stringer("from", from.RemoteAddr()).
			Stringer("to", to.RemoteAddr()).
			Logger()
		logger.Debug().Msg("forward start")
		defer logger.Debug().Msg("forward done")
		var r *throttledReader
		bs := int64(c.BufSize)
		progress := func(n int) {
			counter.Add(uint64(n))
		}
		if limit {
			r = newThrottledReader(from, limiter, bs, c.NoSplice, progress)
		} else {
			r = newThrottledReader(from, nil, bs, c.NoSplice, progress)
		}
		n, err := r.WriteTo(to)
		logger.Debug().Msgf("%d bytes sent", n)
		if err != nil {
			logger.Err(err).Msg("forward error")
		}
	}
	wg.Add(2)
	go forward(conn, uconn, true)
	go forward(uconn, conn, false)
	wg.Wait()
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	var c configuration
	cf := flag.String("conf", "", "configuration file")
	sflag.AddFlags(flag.CommandLine, c)
	flag.Parse()
	if *cf != "" {
		err := parseConfig(&c, *cf)
		if err != nil {
			log.Panicf("invalid conf %q: %v", *cf, err)
		}
	}
	sflag.SetFromFlags(&c, flag.CommandLine)
	logger, err := loggerFromConfig(&c.Logging)
	if err != nil {
		log.Panicf("failed to create logger: %v", err)
	}
	c.logger = logger.With().
		Str("instance", c.Name).
		Str("upstream", c.Upstream).
		Logger()
	if len(c.Addrs) == 0 || c.Upstream == "" {
		flag.Usage()
		os.Exit(1)
	}
	if c.Burst == 0 {
		c.Burst = c.Rate
	}
	var limiter *rate.Limiter
	if c.Rate > 0 {
		limiter = rate.NewLimiter(rate.Limit(c.Rate), int(c.Burst))
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
					limiter = rate.NewLimiter(rate.Limit(c.Rate), int(c.Burst))
				}
				go handleClient(&c, conn, limiter)
			}
		}()
	}
	go func() {
		var prev uint64
		interval := 5 * time.Second
		for {
			time.Sleep(interval)
			cur := counter.Load()
			rate := float64((cur-prev)*8) / interval.Seconds()
			c.logger.Info().Float64("rate", rate).Msgf("rate: %.1f bps", rate)
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
