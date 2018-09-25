package announcer

import (
	"time"

	"github.com/cenkalti/backoff"
	"github.com/cenkalti/rain/internal/logger"
	"github.com/cenkalti/rain/internal/peerlist"
	"github.com/cenkalti/rain/internal/tracker"
)

const stopEventTimeout = time.Minute

type Announcer struct {
	url          string
	log          logger.Logger
	completedC   chan struct{}
	peerList     *peerlist.PeerList
	tracker      tracker.Tracker
	backoff      backoff.BackOff
	nextAnnounce time.Duration
	requests     chan *Request
}

type Request struct {
	Response chan Response
}

type Response struct {
	Transfer tracker.Transfer
}

func New(trk tracker.Tracker, requests chan *Request, completedC chan struct{}, pl *peerlist.PeerList, l logger.Logger) *Announcer {
	return &Announcer{
		tracker:    trk,
		log:        l,
		completedC: completedC,
		peerList:   pl,
		requests:   requests,
		backoff: &backoff.ExponentialBackOff{
			InitialInterval:     5 * time.Second,
			RandomizationFactor: 0.5,
			Multiplier:          2,
			MaxInterval:         30 * time.Minute,
			MaxElapsedTime:      0, // never stop
			Clock:               backoff.SystemClock,
		},
	}
}

func (a *Announcer) Run(stopC chan struct{}) {
	a.backoff.Reset()
	a.announce(tracker.EventStarted, stopC)
	for {
		select {
		case <-time.After(a.nextAnnounce):
			a.announce(tracker.EventNone, stopC)
		case <-a.completedC:
			a.announce(tracker.EventCompleted, stopC)
			a.completedC = nil
		case <-stopC:
			//a.announceStopAndClose() // TODO make async, don't wait
			return
		}
	}
}

func (a *Announcer) announce(e tracker.Event, stopC chan struct{}) {
	req := &Request{
		Response: make(chan Response),
	}
	select {
	case a.requests <- req:
	case <-stopC:
		return
	}
	var resp Response
	select {
	case resp = <-req.Response:
	case <-stopC:
		return
	}
	r, err := a.tracker.Announce(resp.Transfer, e, stopC)
	if err != nil {
		a.log.Errorln("announce error:", err)
		a.nextAnnounce = a.backoff.NextBackOff()
	} else {
		a.backoff.Reset()
		a.nextAnnounce = r.Interval
		select {
		case a.peerList.NewPeers <- r.Peers:
		case <-stopC:
		}
	}
}

func (a *Announcer) announceStopAndClose() {
	stopC := make(chan struct{})
	go func() {
		<-time.After(stopEventTimeout)
		close(stopC)
	}()
	a.announce(tracker.EventStopped, stopC)
	a.tracker.Close()
}
