package report

import (
	"math"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/store"
)

// SummaryData encapsulates metrics aggregated over a reporting window.
type SummaryData struct {
	PeriodStart          time.Time
	PeriodEnd            time.Time
	PlanName             string
	TotalChecks          int
	OKChecks             int
	MeasurementErrors    int
	SlowDownloadChecks   int
	SlowUploadChecks     int
	MinDownloadMbps      *float64
	AvgDownloadMbps      *float64
	MaxDownloadMbps      *float64
	MinUploadMbps        *float64
	AvgUploadMbps        *float64
	MaxUploadMbps        *float64
	MinLatencyMs         *float64
	AvgLatencyMs         *float64
	MaxLatencyMs         *float64
	OutageCount          int
	TotalDowntimeSeconds int
	OpenBreachEpisode    *store.SLABreachEpisode
	OpenOutageEpisode    *store.OutageEpisode
	BreachEpisodes       []store.SLABreachEpisode
	OutageEpisodes       []store.OutageEpisode
	Checks               []store.NetworkCheck
}

// BuildSummaryData calculates aggregated metrics from checks and episodes in [start, end].
func BuildSummaryData(
	start, end time.Time,
	checks []store.NetworkCheck,
	breaches []store.SLABreachEpisode,
	outages []store.OutageEpisode,
	openBreach *store.SLABreachEpisode,
	openOutage *store.OutageEpisode,
	speedAlertDown, speedAlertUp float64,
	planName string,
) SummaryData {
	data := SummaryData{
		PeriodStart:       start,
		PeriodEnd:         end,
		PlanName:          planName,
		TotalChecks:       len(checks),
		OpenBreachEpisode: openBreach,
		OpenOutageEpisode: openOutage,
		BreachEpisodes:    breaches,
		OutageEpisodes:    outages,
		Checks:            checks,
	}

	var (
		sumDown, sumUp, sumLat       float64
		countDown, countUp, countLat int
		minDown, minUp, minLat       = math.MaxFloat64, math.MaxFloat64, math.MaxFloat64
		maxDown, maxUp, maxLat       = -1.0, -1.0, -1.0
	)

	for _, c := range checks {
		if c.Status == "measurement_error" {
			data.MeasurementErrors++
			continue
		}
		if c.Status == "ok" {
			data.OKChecks++
		}

		if c.DownloadMbps != nil {
			v := *c.DownloadMbps
			countDown++
			sumDown += v
			if v < minDown {
				minDown = v
			}
			if v > maxDown {
				maxDown = v
			}
			if v < speedAlertDown {
				data.SlowDownloadChecks++
			}
		}

		if c.UploadMbps != nil {
			v := *c.UploadMbps
			countUp++
			sumUp += v
			if v < minUp {
				minUp = v
			}
			if v > maxUp {
				maxUp = v
			}
			if v < speedAlertUp {
				data.SlowUploadChecks++
			}
		}

		if c.LatencyMs != nil {
			v := *c.LatencyMs
			countLat++
			sumLat += v
			if v < minLat {
				minLat = v
			}
			if v > maxLat {
				maxLat = v
			}
		}
	}

	if countDown > 0 {
		avg := sumDown / float64(countDown)
		data.AvgDownloadMbps = &avg
		data.MinDownloadMbps = &minDown
		data.MaxDownloadMbps = &maxDown
	}
	if countUp > 0 {
		avg := sumUp / float64(countUp)
		data.AvgUploadMbps = &avg
		data.MinUploadMbps = &minUp
		data.MaxUploadMbps = &maxUp
	}
	if countLat > 0 {
		avg := sumLat / float64(countLat)
		data.AvgLatencyMs = &avg
		data.MinLatencyMs = &minLat
		data.MaxLatencyMs = &maxLat
	}

	data.OutageCount = len(outages)
	for _, o := range outages {
		if o.DurationSeconds != nil {
			data.TotalDowntimeSeconds += *o.DurationSeconds
		} else if openOutage != nil && o.ID == openOutage.ID {
			// Currently open outage during period
			curSecs := int(end.Sub(o.StartedAt).Seconds())
			if curSecs > 0 {
				data.TotalDowntimeSeconds += curSecs
			}
		}
	}

	return data
}
