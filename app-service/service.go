package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/go-chi/httprate"
	"github.com/google/uuid"
)

const AGENT_VERSION = "1.0"

////
//// The following is a modified excerpt from the Chi middleware source
//// it was modified to remove the ability to detect and NOT process requests
//// from disconnected clients.  We want to be able to count disconnected
//// clients as part of application metrics, not outright disregard them.
////
// token represents a request that is being processed.
const (
	errCapacityExceeded = "Server capacity exceeded."
	errTimedOut         = "Timed out while waiting for a pending request to complete."
	errContextCanceled  = "Context was canceled."
)

type token struct{}

// throttler limits number of currently processed requests at a time.
type throttler struct {
	tokens         chan token
	backlogTokens  chan token
	retryAfterFn   func(ctxDone bool) time.Duration
	backlogTimeout time.Duration
}

// setRetryAfterHeaderIfNeeded sets Retry-After HTTP header if corresponding retryAfterFn option of throttler is initialized.
func (t throttler) setRetryAfterHeaderIfNeeded(w http.ResponseWriter, ctxDone bool) {
	if t.retryAfterFn == nil {
		return
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(t.retryAfterFn(ctxDone).Seconds())))
}

func HandlerThrottle(limit, backlogLimit int, backlogTimeout time.Duration) func(http.Handler) http.Handler {
	if limit < 1 {
		panic("chi/middleware: Throttle expects limit > 0")
	}

	if backlogLimit < 0 {
		panic("chi/middleware: Throttle expects backlogLimit to be positive")
	}

	t := throttler{
		tokens:         make(chan token, limit),
		backlogTokens:  make(chan token, limit+backlogLimit),
		backlogTimeout: backlogTimeout,
		retryAfterFn:   nil,
	}

	// Filling tokens.
	for i := 0; i < limit+backlogLimit; i++ {
		if i < limit {
			t.tokens <- token{}
		}
		t.backlogTokens <- token{}
	}

	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			select {

			case <-ctx.Done():
				t.setRetryAfterHeaderIfNeeded(w, true)
				http.Error(w, errContextCanceled, http.StatusTooManyRequests)
				return

			case btok := <-t.backlogTokens:
				timer := time.NewTimer(t.backlogTimeout)

				defer func() {
					t.backlogTokens <- btok
				}()

				select {
				case <-timer.C:
					t.setRetryAfterHeaderIfNeeded(w, false)
					http.Error(w, errTimedOut, http.StatusTooManyRequests)
					return
				// here is where we stripped the code which detects
				// disconnected clients
				case tok := <-t.tokens:
					defer func() {
						timer.Stop()
						t.tokens <- tok
					}()
					next.ServeHTTP(w, r)
				}
				return

			default:
				t.setRetryAfterHeaderIfNeeded(w, false)
				http.Error(w, errCapacityExceeded, http.StatusTooManyRequests)
				return
			}
		}

		return http.HandlerFunc(fn)
	}
}

////
//// End the modified Chi source
////

type worker interface {
	process() (string, float64, error)
}

type BaseWorker struct{}

func (b BaseWorker) process() (string, float64, error) {
	return "no worker configured", 0.0, nil
}

type ServiceServer struct {
	agentId           uuid.UUID
	listenAddress     string
	threadCount       int
	queueLength       int
	loadShedThreshold int
	router            chi.Router
	server            http.Server
	worker            worker
	statman           *StatsManager
	clock             *Clock
}

type HandlerResponse struct {
	RequestId     string
	AgentId       string
	AgentVersion  string
	QoS           float64
	Message       string
	RecvTimestamp string
	TxTimestamp   string
}

func NewServiceServer(listenPort int, threadCount int, queueLength int, loadShedThreshold int, statman *StatsManager, clock *Clock) *ServiceServer {
	s := new(ServiceServer)
	s.agentId = uuid.New()
	s.listenAddress = fmt.Sprintf("0.0.0.0:%d", listenPort)
	s.threadCount = threadCount
	s.queueLength = queueLength
	s.loadShedThreshold = loadShedThreshold
	s.router = chi.NewRouter()
	s.server = http.Server{Addr: s.listenAddress}
	s.worker = BaseWorker{}
	s.statman = statman
	s.clock = clock
	return s
}

func (s *ServiceServer) defaultHandler(w http.ResponseWriter, r *http.Request) {
	startTime := s.clock.GetMillis() //time.Now()
	connOpen := true

	// count the connection that has been accepted by a worker thread
	s.statman.incrAcceptedConns()
	// count the receipt of a client request, representing that work is about to begin
	s.statman.incrReceivedRqsts()

	// call the worker to process the request
	msg, qos, err := s.worker.process()
	// record the QoS returned from the worker; a negative qos represents an error took place
	if qos >= 0.0 {
		s.statman.recordQoS(qos)
	}
	// if the worker didn't have an error count the completion of the request processing
	if err == nil {
		s.statman.incrProcessedRqsts()
	}

	// check if the client is still waiting for a response
	ctx := r.Context()
	select {
	case <-ctx.Done():
		connOpen = false
	default:
	}

	// if the client is still waiting for a response send a response and make a note
	if connOpen {
		// prepare the response for the client and send it
		response := HandlerResponse{
			RequestId:    uuid.New().String(),
			AgentId:      s.agentId.String(),
			AgentVersion: AGENT_VERSION,
			QoS:          qos,
			Message:      msg,
		}
		responseJson, _ := json.Marshal(response)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(responseJson)

		s.statman.incrProcessedConns()
	}

	// record overall latency to process the request and note the connection is closing
	s.statman.recordLatency(s.clock.Since(startTime)) // time.Now().Sub(startTime).Milliseconds())
	s.statman.incrClosedConns()
}

func (s *ServiceServer) setWorker(w worker) {
	s.worker = w
}

func (s *ServiceServer) Serve() error {
	log.Printf("Agent %s starting up", s.agentId)
	log.Printf("Listening on: %s", s.listenAddress)

	// limit handler execution to no more than threadCount clients at a time per workshop specification
	s.router.Use(httprate.LimitAll(s.loadShedThreshold, 1*time.Second))
	// can't use the Chi middleware to throttle requests and manage the backlog - it detects disconnected
	// clients and never sends them to the handler.  Use a modified throttle instead.
	s.router.Use(HandlerThrottle(s.threadCount, s.queueLength, 600*time.Second))
	s.router.Get("/", s.defaultHandler)

	return http.ListenAndServe(s.listenAddress, s.router)
}
