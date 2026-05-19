// Benchmark: encode 5,000,000 JSON records to stdout.
package main

import (
	"bufio"
	"os"
	"strconv"
)

const N = 5_000_000

func main() {
	w := bufio.NewWriterSize(os.Stdout, 1<<20)
	defer w.Flush()
	prefix := []byte(`{"id":`)
	suffix := []byte(`,"name":"alice","active":true,"score":3.14}` + "\n")
	var nbuf [24]byte
	for i := int64(0); i < N; i++ {
		w.Write(prefix)
		w.Write(strconv.AppendInt(nbuf[:0], i, 10))
		w.Write(suffix)
	}
}
