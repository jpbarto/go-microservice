package main

import (
	"flag"
	"log"
	"time"
)

const VERSION = "0.7.0"

// default values for cli flags
const ADAPTIVE_RETRIES = false      // whether to enable adaptive retries of http requests
const LOAD_SHED_THRESHOLD = 10000   // how many requests to accept before dropping requests
const CLIENT_DEADLINE_CUTOFF = 1000 // how long to wait before cancelling internal operation
const CLIENT_TIMEOUT = 200
const QUEUE_LENGTH = 10000
const THREAD_COUNT = 50
const HTTP_PORT = 80

func main() {
	adaptiveRetries := flag.Bool("adaptive", ADAPTIVE_RETRIES, "Enable adaptive retries")
	loadShedThreshold := flag.Int("shed", LOAD_SHED_THRESHOLD, "How many connections to accept before the system starts dropping them")
	clientDeadline := flag.Int("cutoff", int(CLIENT_DEADLINE_CUTOFF), "How long (milliseconds) to compute an answer before returning with incomplete work")
	clientTimeout := flag.Int("timeout", CLIENT_TIMEOUT, "How long (milliseconds) to wait for downstream services to respond.")
	queueLength := flag.Int("queue", QUEUE_LENGTH, "How many requests to accept into the backlog for processing")
	threadCount := flag.Int("thread", THREAD_COUNT, "How many threads to execute to process incoming requests")
	httpPort := flag.Int("port", HTTP_PORT, "Network port on which to listen for requests")
	forwardingUrl := flag.String("url", "", "URL of upstream service to call")

	flag.Parse()

	log.Printf("Service Software Version: %s", VERSION)
	log.Printf("Adaptive Retries: %v", *adaptiveRetries)
	log.Printf("Load Shed Threshold: %d", *loadShedThreshold)
	log.Printf("Client Deadline Cutoff: %d", *clientDeadline)
	log.Printf("Client Timeout: %d", *clientTimeout)
	log.Printf("Queue Length: %d", *queueLength)
	log.Printf("Worker Thread Count: %d", *threadCount)
	log.Printf("HTTP Port: %d", *httpPort)

	clock := NewClock()
	statman := NewStatsManager()
	server := NewServiceServer(*httpPort, *threadCount, *queueLength, *loadShedThreshold, statman, clock)

	if len(*forwardingUrl) > 1 {
		forwardingWorker := NewForwardingWorker(*forwardingUrl, *adaptiveRetries, time.Duration(*clientTimeout)*time.Millisecond, *threadCount, statman)
		server.setWorker(forwardingWorker)
		log.Printf("Forwarding to URL: %s", *forwardingUrl)
	} else {
		terminalWorker := NewTerminalWorker(*loadShedThreshold, time.Duration(*clientDeadline)*time.Millisecond, statman, clock)
		server.setWorker(terminalWorker)
		log.Printf("Terminal node, not forwarding")
	}

	statman.startReporter()

	log.Fatal(server.Serve())
}
