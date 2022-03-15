package main

import (
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/aybabtme/iocontrol"
)

var rpool *iocontrol.ReaderPool

func handleClient(conn net.Conn, backend string, debug bool) {
	defer conn.Close()
	if debug {
		defer log.Printf("stop processing request for client: %v", conn.RemoteAddr())
		log.Printf("new client: %v", conn.RemoteAddr())
	}
	backendConn, err := net.Dial("tcp", backend)
	if err != nil {
		log.Printf("failed to connect to backend: %v", err)
		return
	}
	defer backendConn.Close()
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
		if limit && rpool != nil {
			var release func()
			r, release = rpool.Get(from)
			defer release()
		}
		if _, err := io.Copy(to, r); err != nil && !errors.Is(err, io.EOF) {
			log.Println("error while forwarding %v -> %v", fromAddr, toAddr)
		}
	}
	wg.Add(2)
	go forward(conn, backendConn, true)
	go forward(backendConn, conn, false)
	wg.Wait()
}

func main() {
	addr := flag.String("addr", "127.0.0.1:12000", "bind address")
	backend := flag.String("backend", "", "backend address")
	rate := flag.Int("rate", 0, "incoming traffic rate limit")
	debug := flag.Bool("debug", false, "turn on debugging")
	flag.Parse()
	if *addr == "" || *backend == "" {
		flag.Usage()
		os.Exit(1)
	}
	if *rate >= 8 {
		rpool = iocontrol.NewReaderPool(int(*rate/8), 50*time.Millisecond)
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
