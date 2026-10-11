package report

import (
	"bytes"
	"fmt"
	"time"

	"github.com/go-pdf/fpdf"
)

// BuildPDF generates a PDF report summarizing performance metrics, incidents, and checks.
// Pure function taking SummaryData and returning bytes without file I/O.
func BuildPDF(data SummaryData, loc *time.Location) ([]byte, error) {
	if loc == nil {
		if data.Location != nil {
			loc = data.Location
		} else if data.PeriodEnd.Location() != nil {
			loc = data.PeriodEnd.Location()
		} else {
			loc = time.UTC
		}
	}

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(12, 12, 12)
	pdf.SetAutoPageBreak(true, 12)
	pdf.AddPage()

	// Title
	pdf.SetFont("Arial", "B", 16)
	pdf.CellFormat(0, 8, "ISP Performance & Evidence Report", "", 1, "L", false, 0, "")

	pdf.SetFont("Arial", "", 10)
	pdf.SetTextColor(100, 100, 100)
	periodStr := fmt.Sprintf("Plan: %s | Period: %s to %s",
		data.PlanName,
		data.PeriodStart.In(loc).Format("2006-01-02 15:04 MST"),
		data.PeriodEnd.In(loc).Format("2006-01-02 15:04 MST"),
	)
	pdf.CellFormat(0, 6, periodStr, "", 1, "L", false, 0, "")
	pdf.Ln(4)

	// Section 1: Summary Overview
	pdf.SetTextColor(0, 0, 0)
	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(0, 6, "1. Executive Summary", "B", 1, "L", false, 0, "")
	pdf.Ln(2)

	pdf.SetFont("Arial", "", 9)
	formatFloat := func(f *float64, unit string) string {
		if f == nil {
			return "N/A"
		}
		return fmt.Sprintf("%.2f %s", *f, unit)
	}

	colW1 := 46.0
	colW2 := 46.0

	// Summary key-values table
	summaryRows := [][]string{
		{"Total Checks:", fmt.Sprintf("%d", data.TotalChecks), "Avg Download:", formatFloat(data.AvgDownloadMbps, "Mbps")},
		{"Successful Checks:", fmt.Sprintf("%d", data.OKChecks), "Min/Max Download:", fmt.Sprintf("%s / %s", formatFloat(data.MinDownloadMbps, "Mbps"), formatFloat(data.MaxDownloadMbps, "Mbps"))},
		{"Measurement Errors:", fmt.Sprintf("%d", data.MeasurementErrors), "Avg Upload:", formatFloat(data.AvgUploadMbps, "Mbps")},
		{"Slow Checks (Down/Up):", fmt.Sprintf("%d / %d", data.SlowDownloadChecks, data.SlowUploadChecks), "Min/Max Upload:", fmt.Sprintf("%s / %s", formatFloat(data.MinUploadMbps, "Mbps"), formatFloat(data.MaxUploadMbps, "Mbps"))},
		{"Outage Episodes:", fmt.Sprintf("%d", data.OutageCount), "Avg Latency:", formatFloat(data.AvgLatencyMs, "ms")},
		{"Total Downtime:", fmt.Sprintf("%dm %ds", data.TotalDowntimeSeconds/60, data.TotalDowntimeSeconds%60), "Min/Max Latency:", fmt.Sprintf("%s / %s", formatFloat(data.MinLatencyMs, "ms"), formatFloat(data.MaxLatencyMs, "ms"))},
	}

	for _, row := range summaryRows {
		pdf.SetFont("Arial", "B", 9)
		pdf.CellFormat(colW1, 5, row[0], "", 0, "L", false, 0, "")
		pdf.SetFont("Arial", "", 9)
		pdf.CellFormat(colW2, 5, row[1], "", 0, "L", false, 0, "")
		pdf.SetFont("Arial", "B", 9)
		pdf.CellFormat(colW1, 5, row[2], "", 0, "L", false, 0, "")
		pdf.SetFont("Arial", "", 9)
		pdf.CellFormat(colW2, 5, row[3], "", 1, "L", false, 0, "")
	}
	pdf.Ln(4)

	// Section 2: SLA Breach & Outage Episodes
	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(0, 6, "2. SLA Breach & Outage Episodes", "B", 1, "L", false, 0, "")
	pdf.Ln(2)

	zoneAbbr := data.PeriodEnd.In(loc).Format("MST")

	if len(data.BreachEpisodes) == 0 && len(data.OutageEpisodes) == 0 {
		pdf.SetFont("Arial", "I", 9)
		pdf.CellFormat(0, 5, "No SLA breach or connectivity outage episodes recorded during this period.", "", 1, "L", false, 0, "")
	} else {
		if len(data.BreachEpisodes) > 0 {
			pdf.SetFont("Arial", "B", 10)
			pdf.CellFormat(0, 5, "SLA Breach Episodes:", "", 1, "L", false, 0, "")
			pdf.SetFont("Arial", "B", 8)
			pdf.CellFormat(36, 5, fmt.Sprintf("Started (%s)", zoneAbbr), "1", 0, "L", false, 0, "")
			pdf.CellFormat(36, 5, fmt.Sprintf("Ended (%s)", zoneAbbr), "1", 0, "L", false, 0, "")
			pdf.CellFormat(28, 5, "Worst Down", "1", 0, "R", false, 0, "")
			pdf.CellFormat(28, 5, "Worst Up", "1", 0, "R", false, 0, "")
			pdf.CellFormat(28, 5, "Violation %", "1", 0, "R", false, 0, "")
			pdf.CellFormat(30, 5, "Checks", "1", 1, "R", false, 0, "")

			pdf.SetFont("Arial", "", 8)
			for _, ep := range data.BreachEpisodes {
				endStr := "Ongoing (Open)"
				if ep.EndedAt != nil {
					endStr = ep.EndedAt.In(loc).Format("01-02 15:04")
				}
				pdf.CellFormat(36, 5, ep.StartedAt.In(loc).Format("01-02 15:04"), "1", 0, "L", false, 0, "")
				pdf.CellFormat(36, 5, endStr, "1", 0, "L", false, 0, "")
				pdf.CellFormat(28, 5, formatFloat(ep.WorstDownloadMbps, "Mbps"), "1", 0, "R", false, 0, "")
				pdf.CellFormat(28, 5, formatFloat(ep.WorstUploadMbps, "Mbps"), "1", 0, "R", false, 0, "")
				pdf.CellFormat(28, 5, fmt.Sprintf("%.1f%%", ep.ViolationPct), "1", 0, "R", false, 0, "")
				pdf.CellFormat(30, 5, fmt.Sprintf("%d / %d", ep.ViolatingCheckCount, ep.TotalCheckCount), "1", 1, "R", false, 0, "")
			}
			pdf.Ln(2)
		}

		if len(data.OutageEpisodes) > 0 {
			pdf.SetFont("Arial", "B", 10)
			pdf.CellFormat(0, 5, "Connectivity Outage Episodes:", "", 1, "L", false, 0, "")
			pdf.SetFont("Arial", "B", 8)
			pdf.CellFormat(36, 5, fmt.Sprintf("Started (%s)", zoneAbbr), "1", 0, "L", false, 0, "")
			pdf.CellFormat(36, 5, fmt.Sprintf("Ended (%s)", zoneAbbr), "1", 0, "L", false, 0, "")
			pdf.CellFormat(24, 5, "Duration", "1", 0, "R", false, 0, "")
			pdf.CellFormat(90, 5, "Reason", "1", 1, "L", false, 0, "")

			pdf.SetFont("Arial", "", 8)
			for _, o := range data.OutageEpisodes {
				endStr := "Ongoing (Open)"
				durStr := "Active"
				if o.EndedAt != nil {
					endStr = o.EndedAt.In(loc).Format("01-02 15:04")
					if o.DurationSeconds != nil {
						durStr = fmt.Sprintf("%dm %ds", *o.DurationSeconds/60, *o.DurationSeconds%60)
					}
				}
				pdf.CellFormat(36, 5, o.StartedAt.In(loc).Format("01-02 15:04"), "1", 0, "L", false, 0, "")
				pdf.CellFormat(36, 5, endStr, "1", 0, "L", false, 0, "")
				pdf.CellFormat(24, 5, durStr, "1", 0, "R", false, 0, "")

				reason := o.Reason
				if len(reason) > 55 {
					reason = reason[:52] + "..."
				}
				pdf.CellFormat(90, 5, reason, "1", 1, "L", false, 0, "")
			}
			pdf.Ln(2)
		}
	}
	pdf.Ln(3)

	// Section 3: Performance Measurements Table
	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(0, 6, "3. Network Checks Record", "B", 1, "L", false, 0, "")
	pdf.Ln(2)

	pdf.SetFont("Arial", "B", 8)
	pdf.CellFormat(32, 5, fmt.Sprintf("Time (%s)", zoneAbbr), "1", 0, "L", false, 0, "")
	pdf.CellFormat(24, 5, "Status", "1", 0, "L", false, 0, "")
	pdf.CellFormat(26, 5, "Download", "1", 0, "R", false, 0, "")
	pdf.CellFormat(26, 5, "Upload", "1", 0, "R", false, 0, "")
	pdf.CellFormat(22, 5, "Latency", "1", 0, "R", false, 0, "")
	pdf.CellFormat(56, 5, "Server / Error", "1", 1, "L", false, 0, "")

	pdf.SetFont("Arial", "", 8)
	for _, c := range data.Checks {
		tsStr := c.Timestamp.In(loc).Format("01-02 15:04")
		statusStr := c.Status
		if c.BelowThreshold {
			statusStr = "BELOW SLA"
		}

		downStr := "-"
		if c.DownloadMbps != nil {
			downStr = fmt.Sprintf("%.1f M", *c.DownloadMbps)
		}
		upStr := "-"
		if c.UploadMbps != nil {
			upStr = fmt.Sprintf("%.1f M", *c.UploadMbps)
		}
		latStr := "-"
		if c.LatencyMs != nil {
			latStr = fmt.Sprintf("%.1f ms", *c.LatencyMs)
		}

		infoStr := ""
		if c.ServerName != nil {
			infoStr = *c.ServerName
		}
		if c.Error != nil && *c.Error != "" {
			infoStr = *c.Error
		}
		if len(infoStr) > 36 {
			infoStr = infoStr[:33] + "..."
		}

		pdf.CellFormat(32, 4.5, tsStr, "1", 0, "L", false, 0, "")
		pdf.CellFormat(24, 4.5, statusStr, "1", 0, "L", false, 0, "")
		pdf.CellFormat(26, 4.5, downStr, "1", 0, "R", false, 0, "")
		pdf.CellFormat(26, 4.5, upStr, "1", 0, "R", false, 0, "")
		pdf.CellFormat(22, 4.5, latStr, "1", 0, "R", false, 0, "")
		pdf.CellFormat(56, 4.5, infoStr, "1", 1, "L", false, 0, "")
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("failed generating pdf: %w", err)
	}

	return buf.Bytes(), nil
}
