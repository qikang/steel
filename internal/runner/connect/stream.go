package connect

import (
	"io"
	"time"
)

// Stream copies r into w, periodically invoking onProgress (no-op
// when nil) with the cumulative bytes written so far. The total is
// known up front, which lets the caller render a percentage without
// re-reading the source.
//
// Stream is shared by the copy module (for SFTP uploads) and lives
// here in connect so it sits next to the SFTP connection it feeds
// into. Splitting it into its own file keeps this file (and the
// caller file) small and focused.
func Stream(w io.Writer, r io.Reader, total int64, onProgress func(written int64)) error {
	buf := make([]byte, 32*1024)
	var written int64
	last := time.Now()
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			written += int64(n)
			if onProgress != nil && time.Since(last) > 100*time.Millisecond {
				onProgress(written)
				last = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if onProgress != nil {
		onProgress(written)
	}
	return nil
}
