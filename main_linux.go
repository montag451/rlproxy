package main

import (
	"context"
	"io"

	"github.com/montag451/go-splice"
)

func (r *throttledReader) WriteTo(w io.Writer) (int64, error) {
	src, _ := r.r.(splice.FD)
	dst, _ := w.(splice.FD)
	if src == nil || dst == nil {
		return io.Copy(w, readerOnly{r})
	}
	progress := func(n int64) {
		if r.l != nil && n > 0 {
			b := r.l.Burst()
			rem := int(n)
			for rem > 0 {
				wait := b
				if rem <= b {
					wait = rem
				}
				_ = r.l.WaitN(context.TODO(), wait)
				rem -= wait
			}
		}
	}
	opts := []splice.Option{
		splice.WithProgressHandler(progress),
	}
	return splice.Copy(dst, src, opts...)
}
