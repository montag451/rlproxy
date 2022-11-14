//go:build !linux

package main

import (
	"io"
)

func (r *throttledReader) WriteTo(w io.Writer) (int64, error) {
	return r.writeTo(w)
}
