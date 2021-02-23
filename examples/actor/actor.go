// +build example

package main

import (
	"fmt"
	"time"

	"github.com/dominicletz/genserver"
)

// Actor example actor
type Actor struct {
	gen *genserver.GenServer
}

// StartActor runs the new actor
func StartActor() *Actor {
	return &Actor{gen: genserver.New("Actor")}
}

// DoSomething runs any code you want with the actor
func (actor *Actor) DoSomething() {
	actor.gen.Call(func() {
		fmt.Println("DoSomething")
	})
}

// TryTo is a non-blocking send
func (actor *Actor) TryTo() {
	if actor.gen.TryToCast(func() { time.Sleep(100 * time.Millisecond) }) {
		fmt.Println("This worked!")
	} else {
		fmt.Println("The channel was full already")
	}
}

func main() {
	actor := StartActor()
	actor.DoSomething()
	for i := 0; i < 20; i++ {
		actor.TryTo()
	}
}
