package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

const maxSpliceSize = 1 << 20

func splice(rfd uintptr, wfd uintptr) (int64, error) {
	return unix.Splice(int(rfd), nil, int(wfd), nil, maxSpliceSize, unix.SPLICE_F_NONBLOCK)
}

func (r *throttledReader) WriteTo(w io.Writer) (int64, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return 0, fmt.Errorf("failed to create splice pipes: %v", err)
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
		if serr := src.Read(func(rfd uintptr) bool {
			for {
				if werr := pwc.Write(func(wfd uintptr) bool {
					inPipe, err = splice(rfd, wfd)
					return true
				}); werr != nil {
					err = werr
					return true
				}
				if err == unix.EINTR {
					continue
				}
				if err == unix.EAGAIN {
					return false
				}
				return true
			}
		}); serr != nil {
			err = serr
		}
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
			if werr := swc.Write(func(wfd uintptr) bool {
				for {
					if serr := prc.Read(func(rfd uintptr) bool {
						n, err = splice(rfd, wfd)
						return true
					}); serr != nil {
						err = serr
						return true
					}
					if err == unix.EINTR {
						continue
					}
					if err == unix.EAGAIN {
						return false
					}
					return true
				}
			}); werr != nil {
				err = werr
			}
			if err != nil {
				return written, err
			}
			inPipe -= n
			written += n
		}
	}
}
