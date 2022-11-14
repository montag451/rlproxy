package main

import (
	"io"

	"github.com/montag451/go-splice"
)

func (r *throttledReader) WriteTo(w io.Writer) (int64, error) {
	src, _ := r.r.(splice.FD)
	dst, _ := w.(splice.FD)
	if src == nil || dst == nil || r.noSplice {
		return r.writeTo(w)
	}
	progress := func(n int64) {
		r.throttle(int(n))
	}
	opts := []splice.Option{
		splice.WithBufSize(int(r.bs)),
		splice.WithProgressHandler(progress),
	}
	return splice.Copy(dst, src, opts...)
}
