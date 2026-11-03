package treeman

import (
	"bufio"
	"net"
)

// readLines pumps NDJSON lines until the stream closes or the reader
// is done.
func readLines(conn net.Conn, out chan<- string, done <-chan struct{}) {
	defer close(out)
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		select {
		case out <- scanner.Text():
		case <-done:
			return
		}
	}
}
