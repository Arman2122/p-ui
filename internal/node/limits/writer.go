package limits

import (
	"context"
	"io"
)

type Writer struct {
	Context   context.Context
	Writer    io.Writer
	Account   *Account
	Direction Direction
}

func (w Writer) Write(data []byte) (int, error) {
	written := 0
	for len(data) != 0 {
		n, err := w.Account.TakeUpTo(w.Context, w.Direction, len(data))
		if err != nil {
			return written, err
		}
		actual, err := w.Writer.Write(data[:n])
		w.Account.Record(w.Direction, actual)
		written += actual
		if err != nil {
			return written, err
		}
		if actual != n {
			return written, io.ErrShortWrite
		}
		data = data[actual:]
	}
	return written, nil
}
