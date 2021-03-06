package collector

import (
	"time"

	"github.com/globalsign/mgo"
	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rwynn/gtm"
)

var (
	oplogEntryCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace,
		Subsystem: "oplogtail",
		Name:      "entry_count",
		Help:      "The total number of entries observed in the oplog by ns/op",
	}, []string{"ns", "op"})
	oplogEntrySize = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace,
		Subsystem: "oplogtail",
		Name:      "entry_size",
		Help:      "The total size of entries observed in the oplog by ns/op",
	}, []string{"ns", "op"})
	oplogTailError = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: Namespace,
		Subsystem: "oplogtail",
		Name:      "tail_error",
		Help:      "The total number of errors while tailing the oplog",
	})
)

var tailer *OplogTailStats

type OplogTailStats struct{}

func (o *OplogTailStats) Start(session *mgo.Session) {
	// Override the socket timeout for oplog tailing
	// Here we want a long-running socket, otherwise we cause lots of locks
	// which seriously impede oplog performance
	timeout := time.Second * 120
	session.SetSocketTimeout(timeout)

	defer session.Close()
	session.SetMode(mgo.Monotonic, true)

    // We want to include all oplog metrics, as such we'll include migrate entries
    // which are the entries with `fromMigrate`
	opts := gtm.DefaultOptions()
	opts.IncludeMigrate = true
	// If the mongoD has not joined a replicaSet, gtm.Start will crash when it
	// try to get the oplog collection name. Setting the name here will prevent that.
	// newer version of gtm fixed this and it always use 'oplog.rs'.
	oplogName := "oplog.rs"
	opts.OpLogCollectionName = &oplogName

	ctx := gtm.Start(session, opts)
	defer ctx.Stop()

	// ctx.OpC is a channel to read ops from
	// ctx.ErrC is a channel to read errors from
	// ctx.Stop() stops all go routines started by gtm.Start
	for {
		// loop forever receiving events
		select {
		case err := <-ctx.ErrC:
			oplogTailError.Add(1)
			glog.Errorf("Error getting entry from oplog: %v", err)
		case op := <-ctx.OpC:
			oplogEntryCount.WithLabelValues(op.Namespace, op.Operation).Add(1)
			oplogEntrySize.WithLabelValues(op.Namespace, op.Operation).Add(float64(op.DataSize))
		}
	}
}

// Export exports metrics to Prometheus
func (status *OplogTailStats) Export(ch chan<- prometheus.Metric) {
	oplogEntryCount.Collect(ch)
	oplogEntrySize.Collect(ch)
	oplogTailError.Collect(ch)
}

// Describe describes metrics collected
func (status *OplogTailStats) Describe(ch chan<- *prometheus.Desc) {
	oplogEntryCount.Describe(ch)
	oplogEntrySize.Describe(ch)
	oplogTailError.Describe(ch)
}

func GetOplogTailStats(session *mgo.Session) *OplogTailStats {
	if tailer == nil {
		tailer = &OplogTailStats{}
		// Start a tailer with a copy of the session (to avoid messing with the other metrics in the session)
		go tailer.Start(session.Copy())
	}

	return tailer
}
