package genserver

import (
	"fmt"
	"sync"
)

type Reply struct {
	fun    func(reply *Reply) bool
	c      chan bool
	rounds int
	label  string
	mutex  sync.Mutex
}

func (reply *Reply) SetLabel(label string) {
	reply.mutex.Lock()
	defer reply.mutex.Unlock()

	reply.label = label
}

func (reply *Reply) GetLabel() string {
	reply.mutex.Lock()
	defer reply.mutex.Unlock()

	return reply.label
}

func (reply *Reply) Abort() {
	reply.mutex.Lock()
	defer reply.mutex.Unlock()

	reply.fun = nil
	select {
	case reply.c <- false:
	default:
	}
}

func (reply *Reply) ReRun() {
	reply.mutex.Lock()
	defer reply.mutex.Unlock()

	if reply.fun == nil {
		fmt.Printf("GenServer WARNING ReRun() called on already executed reply")
		return
	}

	fun := reply.fun
	reply.mutex.Unlock()
	ret := fun(reply)
	reply.mutex.Lock()

	if ret {
		reply.fun = nil
		select {
		case reply.c <- true:
		default:
		}
	} else {
		reply.rounds++
	}
}

func (reply *Reply) GetRounds() int {
	reply.mutex.Lock()
	defer reply.mutex.Unlock()

	return reply.rounds
}
