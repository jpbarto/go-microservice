package main

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

const TERMINAL_SLEEP_TIME int64 = 15 //  * time.Millisecond

var busyWorkCalls uint64 = 205008

type TerminalWorker struct {
	loadShedThreshold    int   // how many requests to accept before turning requests away, 0 to disable
	clientDeadlineCutoff int64 // how long to generate a response before replying with a half-finished answer, 0 to disable
	statman              *StatsManager
	clock                *Clock
}

func NewTerminalWorker(
	loadShedThreshold int,
	clientDeadlineCutoff time.Duration,
	statman *StatsManager,
	clock *Clock,
) *TerminalWorker {

	t := new(TerminalWorker)

	t.loadShedThreshold = loadShedThreshold
	t.clientDeadlineCutoff = clientDeadlineCutoff.Milliseconds()

	t.statman = statman
	t.clock = clock

	return t
}

func (t TerminalWorker) process() (string, float64, error) {
	var workDone uint64
	processStartTime := t.clock.GetMillis() // time.Now()

	// until this has run for TERMINAL_SLEEP_TIME ms AND has done enough work, make binary trees
	for timeDiff := t.clock.Since(processStartTime); timeDiff < t.clientDeadlineCutoff &&
		(workDone < busyWorkCalls || timeDiff < TERMINAL_SLEEP_TIME); timeDiff = t.clock.Since(processStartTime) {

		workDone = workDone + makeBinaryTrees()
	}

	qos := float64(workDone) / float64(busyWorkCalls)
	return "Rooted", math.Min(1.0, qos), nil
}

/**
 * Busy Work
 */
type Node struct {
	next *Next
}

type Next struct {
	left, right Node
}

func createTree(depth int) Node {
	if depth > 1 {
		return Node{&Next{createTree(depth - 1), createTree(depth - 1)}}
	}
	return Node{&Next{Node{}, Node{}}}
}

func checkTree(node Node) int {
	sum := 1
	current := node.next
	for current != nil {
		sum += checkTree(current.right) + 1
		current = current.left.next
	}
	return sum
}

func makeBinaryTrees() uint64 {
	const minDepth = 4 //4
	const maxDepth = 6 // 6
	//var longLivedTree Node
	var group sync.WaitGroup
	var workDone uint64

	group.Add(1)
	go func() {
		chk := checkTree(createTree(maxDepth + 1))
		atomic.AddUint64(&workDone, uint64(chk))
		group.Done()
	}()

	for halfDepth := minDepth / 2; halfDepth < maxDepth/2+1; halfDepth++ {
		iters := 1 << (maxDepth - (halfDepth * 2) + minDepth)
		group.Add(1)
		go func(depth, iters, chk int) {
			for i := 0; i < iters; i++ {
				chk_val := checkTree(createTree(depth))
				chk += chk_val
				atomic.AddUint64(&workDone, uint64(chk_val))
			}
			group.Done()
		}(halfDepth*2, iters, 0)
	}

	group.Wait()

	return workDone
}
