package main

import (
	"context"
	"sync"

	"github.com/chaos-io/chaos/core"
	"github.com/chaos-io/chaos/logs"

	"chaos-io/chaos/docker"
)

func main() {
	var wg sync.WaitGroup
	n := 20
	for i := 0; i < n; i++ {
		wg.Add(1)
		i := i
		go func(i int) {
			start(i)
			wg.Done()
		}(i)
	}
	wg.Wait()
}

func start(i int) {
	logs.Infow("start", "num", i)
	opt := core.Options{
		docker.OptionPorts: []string{"9000"},
	}
	_, err := docker.Start(context.Background(), "print", "", true, nil, opt, "")
	if err != nil {
		logs.Errorw("failed to start", "error", err)
		return
	}
	logs.Infow("start successfully")
}
