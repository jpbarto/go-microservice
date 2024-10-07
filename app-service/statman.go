package main

import (
	"log"
	"math"
	"sort"
	"time"

	"github.com/shirou/gopsutil/cpu"
	"gonum.org/v1/gonum/stat"
)

const REPORT_INTERVAL uint = 4 // report every 4 seconds

type StatsManager struct {
	connAccepted  uint
	connProcessed uint
	connClosed    uint
	rqstReceived  uint
	rqstProcessed uint
	rqstLatencies []float64
	qosValues     []float64
}

func NewStatsManager() *StatsManager {
	s := new(StatsManager)
	return s
}

func (s *StatsManager) incrAcceptedConns() {
	s.connAccepted++
}

func (s *StatsManager) incrProcessedConns() {
	s.connProcessed++
}

func (s *StatsManager) incrClosedConns() {
	s.connClosed++
}

func (s *StatsManager) incrReceivedRqsts() {
	s.rqstReceived++
}

func (s *StatsManager) incrProcessedRqsts() {
	s.rqstProcessed++
}

func (s *StatsManager) recordLatency(l int64) {
	s.rqstLatencies = append(s.rqstLatencies, float64(l))
}

func (s *StatsManager) recordQoS(q float64) {
	s.qosValues = append(s.qosValues, q)
}

func getCpuUsage() int {
	percent, err := cpu.Percent(time.Second, false)
	if err != nil {
		log.Fatal(err)
	}
	return int(math.Ceil(percent[0]))
}

func (s *StatsManager) reportStatistics() {
	var latencies = []float64{0}
	if len(s.rqstLatencies) > 0 {
		latencies = s.rqstLatencies
		sort.Float64s(latencies)
	}
	var p50 = int64(stat.Quantile(0.5, stat.Empirical, latencies, nil))
	var p90 = int64(stat.Quantile(0.9, stat.Empirical, latencies, nil))
	var p100 = int64(stat.Quantile(1.0, stat.Empirical, latencies, nil))

	if len(s.qosValues) < 1 {
		s.qosValues = []float64{1.0}
	}

	var service = 1.0
	if s.connClosed > 0 {
		service = float64(s.rqstProcessed) / float64(s.rqstReceived)
	}
	var client = 1.0
	if s.connClosed > 0 {
		client = float64(s.connProcessed) / float64(s.connClosed)
	}

	cpuUsage := getCpuUsage()

	log.Printf(time.Now().Format(time.UnixDate))
	//log.Printf("Conn Accepted %d", connAccepted)
	//log.Printf("Conn Processed %d", connProcessed)
	//log.Printf("Conn Closed %d", connClosed)
	//log.Printf("Rqst Received %d", rqstReceived)
	//log.Printf("Rqst Processed %d", rqstProcessed)

	log.Printf("Connections: %d", (s.connAccepted / REPORT_INTERVAL))
	// log.Printf("Connections: %d", (connAccepted/REPORT_INTERVAL)) // alternative avg conn per interval
	//log.Printf("Transactions: %d", rqstProcessed)
	// log.Printf("Throughput: %.3f TPS", (float32(s.connProcessed) / float32(REPORT_INTERVAL)))
	log.Printf("Throughput: %.3f TPS", (float32(s.connClosed) / float32(REPORT_INTERVAL)))
	log.Printf("Latency mean: %d ms", int64(stat.Mean(latencies, nil)))
	log.Printf("Latency P50/P90/P100: %d / %d / %d ms", p50, p90, p100)
	log.Printf("Service: %.3f", math.Min(1.0, service))
	log.Printf("Client: %.3f", math.Min(1.0, client))
	log.Printf("QoS: %.3f", stat.Mean(s.qosValues, nil))
	log.Printf("CPU: %d %%", cpuUsage)
	log.Printf("---------------------------------")

	s.connAccepted = 0
	s.connProcessed = 0
	s.connClosed = 0
	s.rqstReceived = 0
	s.rqstProcessed = 0
	s.rqstLatencies = s.rqstLatencies[:0]
	s.qosValues = s.qosValues[:0]
}

func (s *StatsManager) startReporter() {
	reportingTicker := time.NewTicker(time.Duration(REPORT_INTERVAL) * time.Second)
	go func() {
		for {
			select {
			case <-reportingTicker.C:
				s.reportStatistics()
			}
		}
	}()
}
