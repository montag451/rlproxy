package main

import (
	"context"
	"io"

	"golang.org/x/time/rate"
)

type readerOnly struct {
	io.Reader
}

type writerOnly struct {
	io.Writer
}

type throttledReader struct {
	r        io.Reader
	l        *rate.Limiter
	bs       int64
	noSplice bool
}

func newThrottledReader(r io.Reader, l *rate.Limiter, bs int64, noSplice bool) *throttledReader {
	return &throttledReader{
		r:        r,
		l:        l,
		bs:       bs,
		noSplice: noSplice,
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

func (r *throttledReader) writeTo(w io.Writer) (int64, error) {
	var dst io.Writer = w
	if r.noSplice || r.bs > 0 {
		dst = writerOnly{w}
	}
	var buf []byte
	if r.bs > 0 {
		buf = make([]byte, r.bs)
	}
	return io.CopyBuffer(dst, readerOnly{r}, buf)
}
