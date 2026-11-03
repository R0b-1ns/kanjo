package claudecode

import (
	"bufio"
	"io"
)

const readBufSize = 65536

// ForEachLine calls fn with every non-empty line of r (without its newline).
// A line longer than max bytes is discarded without being held in memory and
// counted in oversized; reading continues with the next line. The slice passed
// to fn is reused: fn must not keep it.
func ForEachLine(r io.Reader, max int, fn func([]byte)) (oversized int, err error) {
	br := bufio.NewReaderSize(r, readBufSize)
	a := assembler{max: max, fn: fn}
	for {
		frag, err := br.ReadSlice('\n')
		ended := err != bufio.ErrBufferFull
		if err == nil {
			frag = frag[:len(frag)-1]
		}
		a.add(frag, ended)
		switch err {
		case nil, bufio.ErrBufferFull:
		case io.EOF:
			return a.oversized, nil
		default:
			return a.oversized, err
		}
	}
}

// assembler joins the fragments of one line and enforces the size cap.
type assembler struct {
	max        int
	fn         func([]byte)
	line       []byte
	discarding bool
	oversized  int
}

func (a *assembler) add(frag []byte, ended bool) {
	if len(a.line)+len(frag) > a.max {
		a.discarding = true
	}
	if !a.discarding {
		a.line = append(a.line, frag...)
	}
	if !ended {
		return
	}
	switch {
	case a.discarding:
		a.oversized++
	case len(a.line) > 0:
		a.fn(a.line)
	}
	a.line, a.discarding = a.line[:0], false
}
