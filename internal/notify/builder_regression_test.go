package notify

import (
	"github.com/dexterbrylle/isp-speed-monitor/internal/sla"
	"strings"
	"testing"
)

func TestBreachUsesConfiguredViolationPercentage(t *testing.T) {
	e := BuildSLABreachEvent("plan", &sla.EvaluationResult{}, 100, 100, 24, 37.5, nil)
	if !strings.Contains(e.Body, ">37.5%") {
		t.Fatalf("configured limit missing: %s", e.Body)
	}
}
