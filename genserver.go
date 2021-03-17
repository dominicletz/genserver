// Copyright 2021 Dominic Letz
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License. See the AUTHORS file
// for names of contributors.

// Package genserver is a set of actor model helper functions
// https://www.gophercon.co.uk/videos/2016/an-actor-model-in-go/
// Ensure all accesses are wrapped in port.cmdChan <- func() { ... }
package genserver

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

// GenServer structure
type GenServer struct {
	Terminate        func()
	DeadlockTimeout  time.Duration
	DeadlockCallback func(*GenServer, string)

	label         string
	id            int64
	cmdChan       chan func()
	isShutdown    bool
	shutdownTimer *time.Timer
}

// New creates a new genserver
// Assign the Terminate function to define a callback just before the worker stops
func New(label string) *GenServer {
	server := &GenServer{
		label:            label,
		cmdChan:          make(chan func(), 10),
		isShutdown:       false,
		DeadlockTimeout:  30 * time.Second,
		DeadlockCallback: DefaultDeadlockCallback,
	}
	go server.loop()
	server.Call(func() { server.id = goroutineID() })
	return server
}

// DefaultDeadlockCallback is the default handler for deadlock detection
func DefaultDeadlockCallback(server *GenServer, trace string) {
	if len(trace) > 0 {
		fmt.Fprintf(os.Stderr, "GenServer WARNING timeout in %s\nGenServer stuck in\n%s\n", server.Name(), trace)
	} else {
		fmt.Fprintf(os.Stderr, "GenServer WARNING timeout in %s\nGenServer couldn't find stacktrace\n", server.Name())
	}
}

// Name returns the label and goid of this GenServer
func (server *GenServer) Name() string {
	return fmt.Sprintf("%s:%d", server.label, server.id)
}

func (server *GenServer) loop() {
	for server.shutdownTimer == nil {
		fun := <-server.cmdChan
		fun()
	}
	for !server.isShutdown {
		select {
		case fun := <-server.cmdChan:
			fun()
		case <-server.shutdownTimer.C:
			server.isShutdown = true
		}
	}
	if server.Terminate != nil {
		server.Terminate()
	}
}

// Shutdown sends a shutdown signal to the server.
// It will still operate for lingerTimer before stopping
func (server *GenServer) Shutdown(lingerTimer time.Duration) {
	server.Call(func() {
		if server.isShutdown {
			return
		}
		server.shutdownTimer = time.NewTimer(lingerTimer)
	})
}

// Call executes a synchronous call operation
func (server *GenServer) Call(fun func()) {
	if server.id == goroutineID() {
		fun()
		return
	}
	timer := time.NewTimer(server.DeadlockTimeout)
	// timer.Stop() see here for details on why
	// https://medium.com/@oboturov/golang-time-after-is-not-garbage-collected-4cbc94740082
	defer timer.Stop()
	done := make(chan bool)
	server.cmdChan <- func() {
		fun()
		done <- true
	}
	select {
	case <-timer.C:
		buf := make([]byte, 100000)
		length := len(buf)
		for length == len(buf) {
			buf := make([]byte, len(buf)*2)
			length = runtime.Stack(buf, true)
		}
		traces := strings.Split(string(buf[:length]), "\n\n")
		prefix := fmt.Sprintf("goroutine %d ", server.id)
		var trace string
		for _, t := range traces {
			if strings.HasPrefix(t, prefix) {
				trace = t
				break
			}
		}
		if cb := server.DeadlockCallback; cb != nil {
			cb(server, trace)
		}

	case <-done:
		return
	}
	<-done
}

// TryToCast is a non blocking send
func (server *GenServer) TryToCast(fun func()) bool {
	select {
	case server.cmdChan <- fun:
		return true
	default:
		return false
	}
}

// Cast executes an asynchrounous operation
func (server *GenServer) Cast(fun func()) {
	server.cmdChan <- fun
}
