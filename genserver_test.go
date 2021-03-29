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

package genserver

import (
	"strings"
	"testing"
	"time"
)

func someblockingFunctionRRZZ() {
	time.Sleep(time.Second * 5)
}

func spawnWorkers(i int) {
	for ; i > 0; i-- {
		go func() {
			time.Sleep(time.Hour)
		}()
	}
}

func TestDeadlockTrace(t *testing.T) {
	// spawning some addtl. coroutines to get a different number
	spawnWorkers(53)
	srv := New("Deadlock")
	// spawning some addtl. coroutines to get a different number
	spawnWorkers(59)

	t.Logf("Got name %s", srv.Name())

	var trace string
	srv.DeadlockCallback = func(server *GenServer, t string) {
		trace = t
	}
	srv.DeadlockTimeout = 1 * time.Second
	srv.Call(func() {
		someblockingFunctionRRZZ()
	})

	if trace == "" {
		t.Errorf("Deadlock trace should have been produced")
	}
	if !strings.Contains(trace, "someblockingFunctionRRZZ") {
		t.Errorf("Deadlock trace should include call to 'someblockingFunctionRRZZ'")
	}
}

func TestSelfCall(t *testing.T) {
	srv := New("Selfcall")
	result := make(chan bool, 1)
	go srv.Call(func() {
		srv.Call(func() {
			result <- true
		})
	})

	timer := time.NewTimer(5 * time.Second)
	select {
	case <-result:
		// all fine
	case <-timer.C:
		t.Errorf("Self calls should be executed")
	}
}

func TestTimeout(t *testing.T) {
	srv := New("TimeoutClient")
	result := make(chan bool, 1)
	ret := srv.CallTimeout(func() {
		time.Sleep(1 * time.Second)
		result <- true
	}, 100*time.Millisecond)

	if ret == nil {
		t.Errorf("Expected timeout message")
	}
}
