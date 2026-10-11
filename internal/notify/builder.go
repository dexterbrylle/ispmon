package notify

import (
	"fmt"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/report"
	"github.com/dexterbrylle/isp-speed-monitor/internal/sla"
	"github.com/dexterbrylle/isp-speed-monitor/internal/store"
)

// BuildSLABreachEvent constructs an Event for a newly declared SLA breach episode.
func BuildSLABreachEvent(
	planName string,
	eval *sla.EvaluationResult,
	thresholdDown, thresholdUp float64,
	windowHours int,
	violationPct float64,
	csvData []byte,
) Event {
	worstDownStr := "N/A"
	if eval.WorstDownloadMbps != nil {
		worstDownStr = fmt.Sprintf("%.1f Mbps", *eval.WorstDownloadMbps)
	}
	worstUpStr := "N/A"
	if eval.WorstUploadMbps != nil {
		worstUpStr = fmt.Sprintf("%.1f Mbps", *eval.WorstUploadMbps)
	}

	fields := []Field{
		{Name: "Contractual Minimum", Value: fmt.Sprintf("%.1f Mbps down / %.1f Mbps up", thresholdDown, thresholdUp), Inline: false},
		{Name: "Below Minimum", Value: fmt.Sprintf("%d / %d checks", eval.ViolatingCount, eval.TotalValidCount), Inline: true},
		{Name: "Violation Rate", Value: fmt.Sprintf("%.1f%%", eval.ViolationPct), Inline: true},
		{Name: "Evaluation Window", Value: fmt.Sprintf("%d hours", windowHours), Inline: true},
		{Name: "Worst Download", Value: worstDownStr, Inline: true},
		{Name: "Worst Upload", Value: worstUpStr, Inline: true},
	}

	// Find the Ookla result_url of the worst check (lowest download speed)
	var worstURL string
	var lowestDown float64 = 1e9
	for _, c := range eval.BreachedChecks {
		if c.DownloadMbps != nil && *c.DownloadMbps < lowestDown {
			lowestDown = *c.DownloadMbps
			if c.ResultURL != nil && *c.ResultURL != "" {
				worstURL = *c.ResultURL
			}
		}
	}
	if worstURL != "" {
		fields = append(fields, Field{
			Name:   "Speedtest Proof URL",
			Value:  fmt.Sprintf("[View Result Proof](%s)", worstURL),
			Inline: false,
		})
	}

	var attachments []Attachment
	if len(csvData) > 0 {
		attachments = append(attachments, Attachment{
			Name:     fmt.Sprintf("sla_breach_evidence_%s.csv", time.Now().UTC().Format("20060102_150405")),
			MIMEType: "text/csv",
			Data:     csvData,
		})
	}

	return Event{
		Kind:        EventSLABreach,
		Title:       fmt.Sprintf("⚠️ ISP SLA Breach Declared — %s", planName),
		Body:        fmt.Sprintf("Connection quality dropped below contractual thresholds for >%.1f%% of checks in the last %d-hour rolling window.", violationPct, windowHours),
		Fields:      fields,
		Attachments: attachments,
	}
}

// BuildSLAResolvedEvent constructs an Event when an active SLA breach episode clears.
func BuildSLAResolvedEvent(planName string, episode *store.SLABreachEpisode, endedAt time.Time) Event {
	durationDesc := "Unknown"
	if !episode.StartedAt.IsZero() {
		durSecs := int(endedAt.Sub(episode.StartedAt).Seconds())
		if durSecs >= 0 {
			hours := durSecs / 3600
			mins := (durSecs % 3600) / 60
			durationDesc = fmt.Sprintf("%dh %dm", hours, mins)
		}
	}

	fields := []Field{
		{Name: "Breach Episode ID", Value: fmt.Sprintf("%d", episode.ID), Inline: true},
		{Name: "Total Breach Duration", Value: durationDesc, Inline: true},
		{Name: "Peak Violation Rate", Value: fmt.Sprintf("%.1f%%", episode.ViolationPct), Inline: true},
	}

	return Event{
		Kind:   EventSLAResolved,
		Title:  fmt.Sprintf("✅ ISP SLA Breach Resolved — %s", planName),
		Body:   "Connection speeds have stabilized back within contractual SLA standards.",
		Fields: fields,
	}
}

// BuildOutageStartedEvent constructs an Event when a connectivity outage is detected.
func BuildOutageStartedEvent(planName string, reason string) Event {
	fields := []Field{
		{Name: "Failure Details", Value: reason, Inline: false},
	}

	return Event{
		Kind:   EventOutageStarted,
		Title:  fmt.Sprintf("🚨 Network Outage Detected — %s", planName),
		Body:   "Internet connectivity is completely down or unreachable.",
		Fields: fields,
	}
}

// BuildOutageRestoredEvent constructs an Event when connectivity is restored.
func BuildOutageRestoredEvent(planName string, durationSeconds int) Event {
	mins := durationSeconds / 60
	secs := durationSeconds % 60
	durStr := fmt.Sprintf("%dm %ds", mins, secs)

	fields := []Field{
		{Name: "Outage Duration", Value: durStr, Inline: true},
	}

	return Event{
		Kind:   EventOutageRestored,
		Title:  fmt.Sprintf("🌐 Connection Restored — %s", planName),
		Body:   fmt.Sprintf("Internet connectivity has been restored after %s.", durStr),
		Fields: fields,
	}
}

// BuildSummaryEvent constructs a periodic summary Event.
func BuildSummaryEvent(data report.SummaryData) Event {
	avgDown := "N/A"
	if data.AvgDownloadMbps != nil {
		avgDown = fmt.Sprintf("%.1f Mbps", *data.AvgDownloadMbps)
	}
	avgUp := "N/A"
	if data.AvgUploadMbps != nil {
		avgUp = fmt.Sprintf("%.1f Mbps", *data.AvgUploadMbps)
	}
	avgLat := "N/A"
	if data.AvgLatencyMs != nil {
		avgLat = fmt.Sprintf("%.1f ms", *data.AvgLatencyMs)
	}

	fields := []Field{
		{Name: "Total Checks", Value: fmt.Sprintf("%d (OK: %d, Errors: %d)", data.TotalChecks, data.OKChecks, data.MeasurementErrors), Inline: false},
		{Name: "Average Download", Value: avgDown, Inline: true},
		{Name: "Average Upload", Value: avgUp, Inline: true},
		{Name: "Average Latency", Value: avgLat, Inline: true},
		{Name: "Slow Checks (Down/Up)", Value: fmt.Sprintf("%d / %d", data.SlowDownloadChecks, data.SlowUploadChecks), Inline: true},
		{Name: "Outages", Value: fmt.Sprintf("%d (%dm %ds downtime)", data.OutageCount, data.TotalDowntimeSeconds/60, data.TotalDowntimeSeconds%60), Inline: true},
	}

	return Event{
		Kind:   EventSummary,
		Title:  fmt.Sprintf("📊 Periodic Network Summary — %s", data.PlanName),
		Body:   fmt.Sprintf("Performance overview for period %s to %s.", data.PeriodStart.Format("15:04"), data.PeriodEnd.Format("15:04 UTC")),
		Fields: fields,
	}
}

// BuildDailyReportEvent constructs an Event for the 24-hour daily report with CSV and PDF attachments.
func BuildDailyReportEvent(data report.SummaryData, csvBytes, pdfBytes []byte) Event {
	e := BuildSummaryEvent(data)
	e.Kind = EventDailyReport
	e.Title = fmt.Sprintf("📑 Daily Network & SLA Report — %s", data.PlanName)
	e.Body = "Attached is the full 24-hour network performance report (PDF summary and raw CSV checks evidence)."

	dateStr := data.PeriodEnd.Format("20060102")
	if len(pdfBytes) > 0 {
		e.Attachments = append(e.Attachments, Attachment{
			Name:     fmt.Sprintf("isp_daily_report_%s.pdf", dateStr),
			MIMEType: "application/pdf",
			Data:     pdfBytes,
		})
	}
	if len(csvBytes) > 0 {
		e.Attachments = append(e.Attachments, Attachment{
			Name:     fmt.Sprintf("isp_network_checks_%s.csv", dateStr),
			MIMEType: "text/csv",
			Data:     csvBytes,
		})
	}

	return e
}
