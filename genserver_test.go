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
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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

type item struct {
}

type state struct {
	srv            *GenServer
	importantThing *item
	waiters        []*Reply
}

func TestReply(t *testing.T) {
	server := &state{
		srv:            New("ReplyTest"),
		importantThing: nil,
	}

	for i := 0; i < 10; i++ {
		var result *item
		done := make(chan struct{}, 1)
		go func() {
			time.Sleep(time.Duration(i%3) * time.Millisecond * 10)

			server.srv.Call2(func(r *Reply) bool {
				if server.importantThing != nil {
					result = server.importantThing
					return true
				} else {
					server.waiters = append(server.waiters, r)
					return false
				}
			})
			done <- struct{}{}
		}()

		time.Sleep(time.Duration(i%5) * time.Millisecond * 10)
		server.srv.Call(func() {
			server.importantThing = &item{}
			for _, w := range server.waiters {
				w.ReRun()
			}
			server.waiters = []*Reply{}
		})

		<-done
		if result == nil {
			t.Errorf("Result should be set")
		}
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

func TestReplyLabel(t *testing.T) {
	srv := New("ReplyLabelServer")
	ret := srv.Call2Timeout(func(r *Reply) bool {
		r.SetLabel("AwaitingReplyLabel")
		return false
	}, 100*time.Millisecond)

	if ret == nil {
		t.Errorf("Expected timeout message")
	}

	if !strings.Contains(ret.Error(), "Label: AwaitingReplyLabel") {
		t.Errorf("Expected timeout message to contain label")
	}
}

func TestReplyAbort(t *testing.T) {
	srv := New("ReplyAbortServer")
	ret := srv.Call2Timeout(func(r *Reply) bool {
		r.Abort()
		return false
	}, 100*time.Millisecond)

	fmt.Printf("ret: %v\n", ret)
	if ret == nil {
		t.Errorf("Expected timeout message")
	}
}

func TestDead(t *testing.T) {
	srv := New("DeadClient")
	srv.Shutdown(0)
	// Because shutdown is delivered in a cast we use the first empty call to
	// ensure the shutdown has been delivered
	srv.Call(func() {})
	ret := srv.Call(func() {})

	if ret == nil {
		t.Errorf("Expected dead message")
	}
}

// TestAbortCalledTwiceInside tests calling Abort() twice inside the call context
func TestAbortCalledTwiceInside(t *testing.T) {
	srv := New("AbortTwiceInside")
	abortCount := 0
	done := make(chan struct{}, 1)

	err := srv.Call2Timeout(func(r *Reply) bool {
		// First abort
		r.Abort()
		abortCount++
		// Second abort - should be safe (no-op)
		r.Abort()
		abortCount++
		done <- struct{}{}
		return false // This should be ignored since we aborted
	}, 100*time.Millisecond)

	<-done
	if err == nil {
		t.Errorf("Expected error from abort")
	}
	if !strings.Contains(err.Error(), "shutting down") {
		t.Errorf("Expected 'shutting down' error, got: %v", err)
	}
	if abortCount != 2 {
		t.Errorf("Expected abort to be called twice, got %d", abortCount)
	}
}

// TestAbortCalledTwiceOutside tests calling Abort() twice from outside the call context
func TestAbortCalledTwiceOutside(t *testing.T) {
	srv := New("AbortTwiceOutside")
	var reply *Reply
	replyReady := make(chan struct{}, 1)
	abortDone := make(chan struct{}, 1)

	// Start a call that will wait
	go func() {
		srv.Call2Timeout(func(r *Reply) bool {
			reply = r
			replyReady <- struct{}{}
			// Wait to be aborted
			time.Sleep(200 * time.Millisecond)
			return false
		}, 500*time.Millisecond)
		abortDone <- struct{}{}
	}()

	// Wait for reply to be available
	<-replyReady
	time.Sleep(10 * time.Millisecond) // Ensure we're outside the call context

	// Call Abort twice from outside
	if reply != nil {
		reply.Abort()
		reply.Abort() // Should be safe to call twice
	}

	select {
	case <-abortDone:
		// Good, abort worked
	case <-time.After(1 * time.Second):
		t.Errorf("Abort should have completed")
	}
}

// TestReRunCalledTwice tests calling ReRun() twice - second call should be no-op
func TestReRunCalledTwice(t *testing.T) {
	srv := New("ReRunTwice")
	runCount := 0
	var storedReply *Reply
	var mu sync.Mutex

	// Start a call that stores the reply
	go func() {
		srv.Call2Timeout(func(r *Reply) bool {
			mu.Lock()
			runCount++
			if storedReply == nil {
				storedReply = r
			}
			mu.Unlock()
			return false // Return false to allow ReRun
		}, 1*time.Second)
	}()

	// Wait for reply to be stored
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	reply := storedReply
	mu.Unlock()

	if reply != nil {
		// Call ReRun twice from within the server context
		srv.Call(func() {
			// First ReRun should work
			reply.ReRun()
			// Second ReRun should be a no-op (fun is nil after first ReRun completes)
			reply.ReRun()
		})
	}

	mu.Lock()
	finalRunCount := runCount
	mu.Unlock()

	// Should have run at least once (initial + potentially one ReRun)
	if finalRunCount < 1 {
		t.Errorf("Expected at least 1 run, got %d", finalRunCount)
	}
}

// TestAbortAfterCompletion tests calling Abort() after the reply has completed successfully
func TestAbortAfterCompletion(t *testing.T) {
	srv := New("AbortAfterComplete")
	var reply *Reply
	completed := make(chan struct{}, 1)

	err := srv.Call2Timeout(func(r *Reply) bool {
		reply = r
		completed <- struct{}{}
		return true // Complete immediately
	}, 500*time.Millisecond)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Wait a bit to ensure completion
	<-completed
	time.Sleep(10 * time.Millisecond)

	// Try to abort after completion - should be safe (no-op)
	if reply != nil {
		reply.Abort()
		reply.Abort() // Should still be safe
	}
}

// TestAbortDuringExecution tests calling Abort() while ReRun() is executing
func TestAbortDuringExecution(t *testing.T) {
	srv := New("AbortDuringExec")
	var reply *Reply
	execStarted := make(chan struct{}, 1)
	execBlocked := make(chan struct{}, 1)
	abortCalled := make(chan struct{}, 1)

	go func() {
		srv.Call2Timeout(func(r *Reply) bool {
			reply = r
			execStarted <- struct{}{}
			// Block execution
			execBlocked <- struct{}{}
			time.Sleep(200 * time.Millisecond)
			return true
		}, 1*time.Second)
	}()

	// Wait for execution to start
	<-execStarted
	<-execBlocked
	time.Sleep(10 * time.Millisecond)

	// Call Abort while execution is happening
	if reply != nil {
		go func() {
			reply.Abort()
			abortCalled <- struct{}{}
		}()
	}

	select {
	case <-abortCalled:
		// Abort completed
	case <-time.After(500 * time.Millisecond):
		t.Errorf("Abort should have completed")
	}
}

// TestConcurrentAbort tests multiple goroutines calling Abort() concurrently
func TestConcurrentAbort(t *testing.T) {
	srv := New("ConcurrentAbort")
	var reply *Reply
	replyReady := make(chan struct{}, 1)
	abortsDone := make(chan int, 10)

	// Start a call that will wait
	go func() {
		srv.Call2Timeout(func(r *Reply) bool {
			reply = r
			replyReady <- struct{}{}
			time.Sleep(300 * time.Millisecond)
			return false
		}, 1*time.Second)
	}()

	<-replyReady
	time.Sleep(10 * time.Millisecond)

	// Have multiple goroutines call Abort concurrently
	abortCount := 10
	for i := 0; i < abortCount; i++ {
		go func() {
			if reply != nil {
				reply.Abort()
				abortsDone <- 1
			}
		}()
	}

	// Wait for all aborts to complete
	completed := 0
	timeout := time.After(1 * time.Second)
	for completed < abortCount {
		select {
		case <-abortsDone:
			completed++
		case <-timeout:
			t.Errorf("Not all aborts completed, only %d/%d", completed, abortCount)
			return
		}
	}
}

// TestConcurrentReRun tests multiple goroutines calling ReRun() concurrently from server context
func TestConcurrentReRun(t *testing.T) {
	srv := New("ConcurrentReRun")
	runCount := 0
	var mu sync.Mutex
	var reply *Reply
	replyReady := make(chan struct{}, 1)

	// Start a call that stores the reply
	go func() {
		srv.Call2Timeout(func(r *Reply) bool {
			mu.Lock()
			runCount++
			if reply == nil {
				reply = r
				replyReady <- struct{}{}
			}
			mu.Unlock()
			return false // Allow ReRun
		}, 1*time.Second)
	}()

	<-replyReady
	time.Sleep(10 * time.Millisecond)

	if reply != nil {
		// Have multiple server calls trigger ReRun concurrently
		var wg sync.WaitGroup
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				srv.Call(func() {
					reply.ReRun()
				})
			}()
		}
		wg.Wait()
	}

	mu.Lock()
	finalRunCount := runCount
	mu.Unlock()

	// Should have run multiple times (initial + ReRuns)
	if finalRunCount < 1 {
		t.Errorf("Expected at least 1 run, got %d", finalRunCount)
	}
}

// TestSetLabelConcurrent tests concurrent SetLabel() calls
func TestSetLabelConcurrent(t *testing.T) {
	srv := New("SetLabelConcurrent")
	var reply *Reply

	err := srv.Call2Timeout(func(r *Reply) bool {
		reply = r
		return false // Keep waiting
	}, 100*time.Millisecond)

	if reply != nil {
		var wg sync.WaitGroup
		labels := []string{"label1", "label2", "label3", "label4", "label5"}
		for _, label := range labels {
			wg.Add(1)
			go func(l string) {
				defer wg.Done()
				reply.SetLabel(l)
			}(label)
		}
		wg.Wait()

		// GetLabel should return one of the labels (last write wins)
		finalLabel := reply.GetLabel()
		found := false
		for _, l := range labels {
			if finalLabel == l {
				found = true
				break
			}
		}
		if !found && finalLabel != "" {
			t.Errorf("Expected label to be one of %v, got %s", labels, finalLabel)
		}
	}

	if err == nil {
		t.Errorf("Expected timeout error")
	}
}

// TestGetLabelConcurrent tests concurrent GetLabel() calls
func TestGetLabelConcurrent(t *testing.T) {
	srv := New("GetLabelConcurrent")
	var reply *Reply

	err := srv.Call2Timeout(func(r *Reply) bool {
		reply = r
		r.SetLabel("test-label")
		return false
	}, 100*time.Millisecond)

	if reply != nil {
		var wg sync.WaitGroup
		results := make(chan string, 10)
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- reply.GetLabel()
			}()
		}
		wg.Wait()
		close(results)

		// All results should be the same
		first := ""
		for result := range results {
			if first == "" {
				first = result
			}
			if result != first {
				t.Errorf("Expected all GetLabel() calls to return same value, got %s and %s", first, result)
			}
		}
	}

	if err == nil {
		t.Errorf("Expected timeout error")
	}
}

// TestGetRoundsConcurrent tests concurrent GetRounds() calls
func TestGetRoundsConcurrent(t *testing.T) {
	srv := New("GetRoundsConcurrent")
	var reply *Reply
	rounds := 0

	err := srv.Call2Timeout(func(r *Reply) bool {
		reply = r
		rounds++
		return false // Keep waiting to accumulate rounds
	}, 200*time.Millisecond)

	// Call ReRun multiple times to increment rounds
	if reply != nil {
		for i := 0; i < 5; i++ {
			reply.ReRun()
			time.Sleep(10 * time.Millisecond)
		}

		// Concurrent GetRounds calls
		var wg sync.WaitGroup
		results := make(chan int, 10)
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- reply.GetRounds()
			}()
		}
		wg.Wait()
		close(results)

		// All results should be the same
		first := -1
		for result := range results {
			if first == -1 {
				first = result
			}
			if result != first {
				t.Errorf("Expected all GetRounds() calls to return same value, got %d and %d", first, result)
			}
		}
		if first < 5 {
			t.Errorf("Expected at least 5 rounds, got %d", first)
		}
	}

	if err == nil {
		t.Errorf("Expected timeout error")
	}
}

// TestAbortAndReRunRace tests race condition between Abort() and ReRun()
func TestAbortAndReRunRace(t *testing.T) {
	srv := New("AbortReRunRace")
	var reply atomic.Value // Use atomic.Value to avoid race on the pointer itself
	replyReady := make(chan struct{}, 1)

	go func() {
		srv.Call2Timeout(func(r *Reply) bool {
			reply.Store(r)
			replyReady <- struct{}{}
			time.Sleep(200 * time.Millisecond)
			return false
		}, 500*time.Millisecond)
	}()

	<-replyReady
	time.Sleep(10 * time.Millisecond)

	// Race between Abort and ReRun
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		r := reply.Load()
		if r != nil {
			r.(*Reply).Abort()
		}
	}()
	go func() {
		defer wg.Done()
		r := reply.Load()
		if r != nil {
			r.(*Reply).ReRun()
		}
	}()
	wg.Wait()
	// Should not panic or deadlock
}

// TestSetLabelMultipleTimes tests calling SetLabel() multiple times
func TestSetLabelMultipleTimes(t *testing.T) {
	srv := New("SetLabelMultiple")
	var reply *Reply

	err := srv.Call2Timeout(func(r *Reply) bool {
		reply = r
		return false
	}, 100*time.Millisecond)

	if reply != nil {
		// Set label multiple times
		reply.SetLabel("label1")
		reply.SetLabel("label2")
		reply.SetLabel("label3")

		label := reply.GetLabel()
		if label != "label3" {
			t.Errorf("Expected label to be 'label3', got '%s'", label)
		}
	}

	if err == nil {
		t.Errorf("Expected timeout error")
	}
}

// TestGetRoundsIncrements tests that GetRounds() correctly increments
func TestGetRoundsIncrements(t *testing.T) {
	srv := New("GetRoundsIncrement")
	var reply *Reply

	err := srv.Call2Timeout(func(r *Reply) bool {
		reply = r
		return false // This increments rounds to 1
	}, 500*time.Millisecond)

	if reply != nil {
		initial := reply.GetRounds()
		// Initial call returns false, so rounds should be 1
		if initial != 1 {
			t.Errorf("Expected initial rounds to be 1 (after return false), got %d", initial)
		}

		// Call ReRun multiple times with return false
		// Each ReRun that returns false increments rounds
		for i := 0; i < 3; i++ {
			reply.ReRun()
			rounds := reply.GetRounds()
			// Expected: 1 (initial) + (i+1) ReRuns = i+2
			expected := i + 2
			if rounds != expected {
				t.Errorf("Expected rounds to be %d after %d ReRun calls, got %d", expected, i+1, rounds)
			}
		}
	}

	if err == nil {
		t.Errorf("Expected timeout error")
	}
}

// TestAbortInsideThenOutside tests Abort() called inside then outside call context
func TestAbortInsideThenOutside(t *testing.T) {
	srv := New("AbortInsideOutside")
	var reply *Reply
	insideAbortDone := make(chan struct{}, 1)

	err := srv.Call2Timeout(func(r *Reply) bool {
		reply = r
		r.Abort()
		insideAbortDone <- struct{}{}
		return false
	}, 100*time.Millisecond)

	<-insideAbortDone
	time.Sleep(10 * time.Millisecond)

	// Now abort from outside
	if reply != nil {
		reply.Abort() // Should be safe even though already aborted
	}

	if err == nil {
		t.Errorf("Expected error from abort")
	}
}

// TestReRunAfterAbort tests calling ReRun() after Abort()
func TestReRunAfterAbort(t *testing.T) {
	srv := New("ReRunAfterAbort")
	var reply atomic.Value // Use atomic.Value to avoid race on the pointer itself
	aborted := make(chan struct{}, 1)

	go func() {
		srv.Call2Timeout(func(r *Reply) bool {
			reply.Store(r)
			time.Sleep(100 * time.Millisecond)
			return false
		}, 500*time.Millisecond)
	}()

	time.Sleep(20 * time.Millisecond)
	r := reply.Load()
	if r != nil {
		r.(*Reply).Abort()
		aborted <- struct{}{}
	}

	<-aborted
	time.Sleep(10 * time.Millisecond)

	// Try to ReRun after abort - should be safe (no-op since fun is nil)
	r = reply.Load()
	if r != nil {
		r.(*Reply).ReRun() // Should handle gracefully
	}
}

// TestChannelFullScenario tests behavior when reply channel might be full
func TestChannelFullScenario(t *testing.T) {
	srv := New("ChannelFull")
	var reply *Reply

	// The reply channel is buffered with size 1, so we need to ensure
	// we test the select default case in Abort()
	err := srv.Call2Timeout(func(r *Reply) bool {
		reply = r
		return true // Complete immediately, filling the channel
	}, 500*time.Millisecond)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Now the channel should have a value, try to abort (should hit default case)
	if reply != nil {
		reply.Abort() // Should not block, should hit default case
		reply.Abort() // Should still work
	}
}

// TestMultipleReRunWithFalse tests multiple ReRun() calls that return false
func TestMultipleReRunWithFalse(t *testing.T) {
	srv := New("MultipleReRunFalse")
	runCount := 0
	var reply *Reply

	err := srv.Call2Timeout(func(r *Reply) bool {
		runCount++
		if reply == nil {
			reply = r
		}
		return false // Always return false to allow ReRun (increments rounds to 1)
	}, 500*time.Millisecond)

	if reply != nil {
		// Call ReRun multiple times (each returns false, incrementing rounds)
		for i := 0; i < 5; i++ {
			reply.ReRun()
			time.Sleep(10 * time.Millisecond)
		}

		rounds := reply.GetRounds()
		// Expected: 1 (initial return false) + 5 (ReRun calls) = 6
		if rounds != 6 {
			t.Errorf("Expected 6 rounds after initial call and 5 ReRun calls, got %d", rounds)
		}
	}

	if err == nil {
		t.Errorf("Expected timeout error")
	}
	if runCount != 6 { // Initial + 5 ReRuns
		t.Errorf("Expected 6 runs, got %d", runCount)
	}
}

// TestReplyMethodsOutsideContext tests all reply methods when called outside call context
func TestReplyMethodsOutsideContext(t *testing.T) {
	srv := New("ReplyMethodsOutside")
	var reply *Reply
	replyReady := make(chan struct{}, 1)

	go func() {
		srv.Call2Timeout(func(r *Reply) bool {
			reply = r
			r.SetLabel("test-label")
			replyReady <- struct{}{}
			time.Sleep(200 * time.Millisecond)
			return false
		}, 1*time.Second)
	}()

	<-replyReady
	time.Sleep(10 * time.Millisecond)

	// Now we're outside the call context, test all methods
	if reply != nil {
		// Test GetLabel
		label := reply.GetLabel()
		if label != "test-label" {
			t.Errorf("Expected label 'test-label', got '%s'", label)
		}

		// Test SetLabel
		reply.SetLabel("new-label")
		label = reply.GetLabel()
		if label != "new-label" {
			t.Errorf("Expected label 'new-label', got '%s'", label)
		}

		// Test GetRounds
		rounds := reply.GetRounds()
		if rounds != 0 {
			t.Errorf("Expected 0 rounds, got %d", rounds)
		}

		// Test Abort
		reply.Abort()
		reply.Abort() // Should be safe to call twice

		// Test ReRun after abort
		reply.ReRun() // Should handle gracefully (fun is nil)
	}
}
