package provider

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Dreamacro/clash/common/atomic"
	"github.com/Dreamacro/clash/common/batch"
	"github.com/Dreamacro/clash/common/singledo"
	"github.com/Dreamacro/clash/common/utils"
	C "github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/log"
)

const (
	defaultURLTestTimeout = time.Second * 5
)

type HealthCheckOption struct {
	URL      string
	Interval uint
}

type HealthCheck struct {
	url             string
	proxies         []C.Proxy
	interval        uint
	lazy            bool
	lastTouch       *atomic.Int64
	done            chan struct{}
	singleDo        *singledo.Single[struct{}]
	statusCodeRange []utils.Range[uint16]
}

func (hc *HealthCheck) process() {
	ticker := time.NewTicker(time.Duration(hc.interval) * time.Second)
	for {
		select {
		case <-ticker.C:
			now := time.Now().Unix()
			if !hc.lazy || now-hc.lastTouch.Load() < int64(hc.interval) {
				hc.check()
			} else {
				log.Debugln("Skip once health check because we are lazy")
			}
		case <-hc.done:
			ticker.Stop()
			return
		}
	}
}

func (hc *HealthCheck) setProxy(proxies []C.Proxy) {
	hc.proxies = proxies
}

func (hc *HealthCheck) auto() bool {
	return hc.interval != 0
}

func (hc *HealthCheck) touch() {
	hc.lastTouch.Store(time.Now().Unix())
}

func (hc *HealthCheck) check() {
	_, _, _ = hc.singleDo.Do(func() (struct{}, error) {
		id := utils.NewUUIDV4().String()
		log.Debugln("Start New Health Checking {%s}", id)
		b, _ := batch.New[bool](context.Background(), batch.WithConcurrencyNum[bool](10))
		for _, proxy := range hc.proxies {
			p := proxy
			b.Go(p.Name(), func() (bool, error) {
				ctx, cancel := context.WithTimeout(context.Background(), defaultURLTestTimeout)
				defer cancel()
				log.Debugln("Health Checking %s {%s}", p.Name(), id)
				_, _ = p.URLTest(ctx, hc.url, hc.statusCodeRange)
				log.Debugln("Health Checked %s : %t %d ms {%s}", p.Name(), p.Alive(), p.LastDelay(), id)
				return false, nil
			})
		}

		b.Wait()
		log.Debugln("Finish A Health Checking {%s}", id)
		return struct{}{}, nil
	})
}

func (hc *HealthCheck) close() {
	hc.done <- struct{}{}
}

func NewHealthCheck(proxies []C.Proxy, url string, interval uint, lazy bool, statusCodeRange string) *HealthCheck {
	var parsedStatusCodeRange, err = utils.NewIntRangeList(strings.Split(statusCodeRange, "/"), errors.New("parse status code range error"))
	if err != nil {
		parsedStatusCodeRange = *new([]utils.Range[uint16])
	}
	return &HealthCheck{
		proxies:         proxies,
		url:             url,
		interval:        interval,
		lazy:            lazy,
		lastTouch:       atomic.NewInt64(0),
		done:            make(chan struct{}, 1),
		singleDo:        singledo.NewSingle[struct{}](time.Second),
		statusCodeRange: parsedStatusCodeRange,
	}
}
