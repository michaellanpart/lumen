package main

import "fmt"

const N = 64
const REPS = 50_000

func nextI64(s *int64) int64 {
	// 31-bit LCG: keeps values positive and avoids signed-overflow UB.
	*s = ((*s * 1103515245) + 12345) % 2147483647
	return *s
}

func main() {
	var a [N]int64
	state := int64(123456789)
	chk := int64(0)

	for rep := 0; rep < REPS; rep++ {
		for i := range a {
			a[i] = nextI64(&state)
		}

		for i := 1; i < N; i++ {
			key := a[i]
			j := i - 1
			for j >= 0 && a[j] > key {
				a[j+1] = a[j]
				j--
			}
			a[j+1] = key
		}

		for i, v := range a {
			chk = (chk + v + int64(i)) % 2147483647
		}
	}
	fmt.Println(chk)
}
