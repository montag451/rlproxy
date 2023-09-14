package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/montag451/go-sflag"
	"github.com/rs/zerolog"
	"golang.org/x/time/rate"
)

var Version = "unknown"

var globalCounter atomic.Uint64

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
		if limit {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var clientCounter atomic.Uint64
			progress := func(n int) {
				clientCounter.Add(uint64(n))
				globalCounter.Add(uint64(n))
			}
			r = newThrottledReader(from, limiter, bs, c.NoSplice, progress)
			go logRate(ctx, &logger, &clientCounter, 5*time.Second)
		} else {
			r = newThrottledReader(from, nil, bs, c.NoSplice, nil)
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

func logRate(ctx context.Context, l *zerolog.Logger, c *atomic.Uint64, interval time.Duration) {
	var prev uint64
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			cur := c.Load()
			rate := float64((cur-prev)*8) / interval.Seconds()
			l.Info().
				Float64("rate", rate).
				Str("rate_human", humanize.SI(rate/8, "B")).
				Msgf("rate: %.1f bps", rate)
			prev = cur
		case <-ctx.Done():
			return
		}
	}
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	var c configuration
	cf := flag.String("config", "", "configuration file")
	showVersion := flag.Bool("version", false, "show version")
	sflag.AddFlags(flag.CommandLine, c)
	flag.Parse()
	if *showVersion {
		fmt.Println(Version)
		os.Exit(0)
	}
	if *cf != "" {
		err := parseConfig(&c, *cf)
		if err != nil {
			log.Panicf("invalid config file %q: %v", *cf, err)
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
			logger.Panic().Err(err).Msgf("failed to listen on %q", addr)
		}
		listeners[i] = l.(*net.TCPListener)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				conn, err := l.Accept()
				if err != nil {
					if !os.IsTimeout(err) {
						logger.Panic().Err(err).Msg("failed to accept new connection")
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
	go logRate(ctx, &c.logger, &globalCounter, 5*time.Second)
	sig := <-sigCh
	logger.Info().Msgf("signal %s received, exiting", sig)
	for _, l := range listeners {
		l.SetDeadline(time.Now())
	}
	wg.Wait()
}
