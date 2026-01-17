package goob_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/runZeroInc/go-rod/pkg/goob"
)

func TestPipeOrder(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	write, events := goob.NewPipe(ctx)

	write(1)
	write(2)
	write(3)

	if 1 != <-events {
		t.Fatal()
	}
	if 2 != <-events {
		t.Fatal()
	}
	if 3 != <-events {
		t.Fatal()
	}
}

func TestPipe(t *testing.T) {

	const pipeCount = 10
	const msgCount = 10

	round := func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		write, events := goob.NewPipe(ctx)

		wg := sync.WaitGroup{}
		wg.Add(msgCount)
		for i := range msgCount * 2 {
			if i%2 == 0 {
				go write(i)
			} else {
				go func() {
					<-events
					wg.Done()
				}()
			}
		}
		wg.Wait()
	}

	wg := sync.WaitGroup{}
	wg.Add(pipeCount)
	for range pipeCount {
		go func() {
			round()
			wg.Done()
		}()
	}
	wg.Wait()
}

func TestPipeCancel(t *testing.T) {

	const count = 1000

	for range count {
		ctx, cancel := context.WithCancel(context.Background())
		write, _ := goob.NewPipe(ctx)
		go write(1)
		go cancel()
	}
}

func TestPipeMonkey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	write, events := goob.NewPipe(ctx)

	round := 30
	count := 10000

	for range round {
		go func() {
			for i := range count {
				write(i)
			}
		}()
	}

	for i := 0; i < count*round; i++ {
		time.Sleep(100 * time.Nanosecond)
		<-events
	}
}
