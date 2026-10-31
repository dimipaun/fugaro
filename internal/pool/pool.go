// Package pool runs a command's index-addressed work on a bounded number of
// goroutines at once.
package pool

import "sync"

// Run calls fn(0) … fn(n-1) on at most workers goroutines, and nothing is
// written between them: indices go out on the channel in order, and whatever
// each call computes is the caller's to place (a slice indexed by i, so the
// result is deterministic whatever order the goroutines finish in).
//
// The width is the caller's because what it stands for is the service the work
// reaches: a bucket takes tens of object reads at once, while a host's
// secondary rate limits discourage concurrent requests at all. A pass that
// fans out over repositories is one width multiplied by the width of the read
// inside each of them, so a caller that nests two of these passes is choosing
// the product.
func Run(n, workers int, fn func(i int)) {
	var wg sync.WaitGroup
	next := make(chan int)
	for range min(workers, n) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				fn(i)
			}
		}()
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
}
