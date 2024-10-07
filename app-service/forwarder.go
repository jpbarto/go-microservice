package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

const FORWARDING_SLEEP_TIME = 10 * time.Millisecond

type AdaptiveRetryClient struct {
	requestTimeout   time.Duration // how long to wait before timing out an http request
	enableAdaptive   bool          // use adaptive retries with a token bucket, if false simply do retries
	minRetryWaitTime time.Duration // minimum duration to wait between retries
	maxRetryWaitTime time.Duration // maximum duration to wait between retries
	httpClient       *http.Client  // http client for sending requests
	retryLimit       int
	retryBucket      *rate.Limiter
}

func (r *AdaptiveRetryClient) get(url string) (*http.Response, error) {
	var resp *http.Response
	var err error

	// if the retry client is uninitialized, init it now
	if r.httpClient == nil {
		r.httpClient = &http.Client{
			Timeout: r.requestTimeout,
		}
		r.retryLimit = 2
	}

	// send a GET request to the url
	// if it fails and there are retries available then retry with a random wait period
	resp, err = r.httpClient.Get(url)
	for retry := 0; (err != nil || resp.StatusCode >= 400) &&
		retry < r.retryLimit; retry++ {
		// if using adaptive retries, are there tokens available for a retry
		// if not using adaptive retries just do the retry
		if (r.enableAdaptive && r.retryBucket.Allow()) || (!r.enableAdaptive) {
			sleepDuration := time.Duration(rand.Int63n(r.maxRetryWaitTime.Nanoseconds()-r.minRetryWaitTime.Nanoseconds()) + r.minRetryWaitTime.Nanoseconds())
			time.Sleep(sleepDuration)
			resp, err = r.httpClient.Get(url)
		}
	}

	return resp, err
}

type ForwardingWorker struct {
	clientTimeout time.Duration
	httpClient    *AdaptiveRetryClient
	forwardingUrl string
	statman       *StatsManager
}

func NewForwardingWorker(
	forwardingUrl string,
	enableAdaptive bool,
	clientTimeout time.Duration,
	clientCount int,
	statman *StatsManager,
) *ForwardingWorker {

	f := new(ForwardingWorker)

	f.forwardingUrl = forwardingUrl

	// configure the worker's http client with retry
	f.clientTimeout = clientTimeout
	f.httpClient = &AdaptiveRetryClient{
		requestTimeout:   clientTimeout,
		enableAdaptive:   enableAdaptive,
		minRetryWaitTime: time.Duration(5) * time.Millisecond,
		maxRetryWaitTime: time.Duration(50) * time.Millisecond,
		retryBucket:      rate.NewLimiter(1, 20),
	}

	f.statman = statman

	return f
}

func (f ForwardingWorker) process() (string, float64, error) {
	time.Sleep(FORWARDING_SLEEP_TIME)

	// call the forwarding url via the circuit breaker
	resp, err := f.httpClient.get(f.forwardingUrl)
	if err != nil || resp.StatusCode >= 400 {
		if err == nil {
			err = fmt.Errorf("Upstream response code: %v %v", resp.StatusCode, resp.Status)
		}
		return "Error getting URL", -1.0, err
	}

	defer resp.Body.Close()
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "Error getting URL", -1.0, err
	}

	// retrieve the QoS from the upstream response
	var this_qos = 0.0
	if respBody != nil {
		var urlResp HandlerResponse
		json.Unmarshal(respBody, &urlResp)
		this_qos = urlResp.QoS
	} else {
		return "NO RESPONSE", -1.0, errors.New("No QoS found")
	}

	return string(respBody), this_qos, nil
}
